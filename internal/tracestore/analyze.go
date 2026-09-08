package tracestore

import (
	"fmt"
	"math"
	"sort"
)

// ErrNotEntrypoint 表示该 Trace 的根 span 不是服务入口（Kind != "server"）。
// 常见于连接池拨号、内部定时任务等非 HTTP 链路。
var ErrNotEntrypoint = fmt.Errorf("root span is not a server entrypoint")

// BuildTrace 从扁平的 span 列表组装成带树结构和分析结果的 Trace。
// 输入必须是已归一化的 span（jaeger.go 负责），本函数与数据源无关。
func BuildTrace(traceID string, spans []*Span) (*Trace, error) {
	if len(spans) == 0 {
		return nil, fmt.Errorf("trace %s: no spans", traceID)
	}

	root, orphans := buildTree(spans)
	if root == nil {
		return nil, fmt.Errorf("trace %s: no root span found", traceID)
	}
	if root.Kind != "server" {
		return nil, ErrNotEntrypoint
	}

	computeSelfMs(root)
	status := classifyStatus(root)
	origin := findErrorOrigin(root)

	t := &Trace{
		TraceID:       traceID,
		RootOperation: root.Operation,
		DurationMs:    root.DurationMs,
		Status:        status,
		SpanCount:     len(spans),
		Root:          root,
	}
	if origin != nil {
		t.ErrorOrigin = origin.Operation
		t.ErrorDesc = firstNonEmpty(origin.Error, origin.StatusDesc)
	}
	if orphans > 0 {
		t.Warnings = append(t.Warnings,
			fmt.Sprintf("%d 个 span 的父节点不在本次返回中，已从树中丢弃", orphans))
	}
	return t, nil
}

// buildTree 把扁平 span 列表挂成树，返回根节点和孤儿数量。
// 根判定双判据：ParentSpanID 为空，且优先选 Kind == "server" 的节点。
func buildTree(spans []*Span) (root *Span, orphans int) {
	byID := make(map[string]*Span, len(spans))
	for _, s := range spans {
		byID[s.SpanID] = s
	}

	for _, s := range spans {
		if s.ParentSpanID == "" {
			if root == nil || s.Kind == "server" {
				root = s
			}
			continue
		}
		if p, ok := byID[s.ParentSpanID]; ok {
			p.Children = append(p.Children, s)
		} else {
			// 父 span 不在这批数据里（trace 被截断），当作孤儿丢弃
			orphans++
		}
	}

	// 按 StartMs 排序，让 Agent 看到的顺序和时间轴一致
	sortChildren(root)
	return root, orphans
}

// sortChildren 递归地按 StartMs 对每层 Children 排序。
func sortChildren(s *Span) {
	if s == nil {
		return
	}
	sort.Slice(s.Children, func(i, j int) bool {
		return s.Children[i].StartMs < s.Children[j].StartMs
	})
	for _, c := range s.Children {
		sortChildren(c)
	}
}

// computeSelfMs 递归计算每个 span 的自身耗时（DurationMs 减去所有直接子节点之和）。
// 子 span 并发时加总可能超过父 span 总耗时，兜底为 0 避免负数误导 Agent。
func computeSelfMs(s *Span) {
	if s == nil {
		return
	}
	var childSum float64
	for _, c := range s.Children {
		computeSelfMs(c)
		childSum += c.DurationMs
	}
	s.SelfMs = math.Round((s.DurationMs-childSum)*1000) / 1000
	if s.SelfMs < 0 {
		s.SelfMs = 0 // 并发子 span 可能导致负数
	}
}

// classifyStatus 根据根节点及其后代判断整条链路的状态。
// failed：根节点本身出错；degraded：后代有错但根正常；ok：无错。
//
// HTTP 状态码过滤规则（优先于 span status）：
//   - 4xx (400-499)：由于4xx说明错误已经被服务处理，并且可以避免mysql中间件对正常的键重复记error
//   - 5xx 或其他非 4xx：继续走常规的 isErrorSpan / hasErrorDescendant 判断。
//
// isErrorSpan 判断单个 span 是否出错：Status 标记为 error，或从 logs 提取到了异常消息。
func isErrorSpan(s *Span) bool {
	return s.Status == "error" || s.Error != ""
}

func classifyStatus(root *Span) string {
	// HTTP 4xx：客户端侧错误，不视为服务端 failed/degraded。
	if code, ok := root.Attrs["http.response.status_code"].(float64); ok && code >= 400 && code < 500 {
		return "ok"
	}
	if isErrorSpan(root) {
		return "failed"
	}
	if hasErrorDescendant(root) {
		return "degraded"
	}
	return "ok"
}

// hasErrorDescendant 深度优先遍历，检查是否存在 status == "error" 的后代。
func hasErrorDescendant(s *Span) bool {
	for _, c := range s.Children {
		if isErrorSpan(c) || hasErrorDescendant(c) {
			return true
		}
	}
	return false
}

// findErrorOrigin 返回第一个（按 StartMs 最早）出错的叶子或内部 span，
// 优先选没有出错子节点的 error span（即错误最初发生的节点，而非传播路径上的中间层）。
func findErrorOrigin(s *Span) *Span {
	if s == nil {
		return nil
	}
	for _, c := range s.Children {
		if origin := findErrorOrigin(c); origin != nil {
			return origin
		}
	}
	if isErrorSpan(s) {
		return s
	}
	return nil
}

// firstNonEmpty 返回参数中第一个非空字符串。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- 聚合统计 ----

// StatsResult 是 Aggregate 的输出，描述一批 Trace 的整体健康状况。
type StatsResult struct {
	TotalTraces int              `json:"total_traces"`
	ByStatus    map[string]int   `json:"by_status"`   // ok / degraded / failed
	Entrypoints []EntrypointStat `json:"entrypoints"` // 按调用量降序
}

// EntrypointStat 描述某个入口操作（root operation）的统计数据。
type EntrypointStat struct {
	Operation string  `json:"operation"`
	Count     int     `json:"count"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
	Failed    int     `json:"failed"`
	Degraded  int     `json:"degraded"`
}

const smallSampleThreshold = 20

// Aggregate 将一批 Trace 按 RootOperation 分组并计算耗时百分位与状态分布。
// 返回聚合结果和可附加到响应 notices 的提示列表。
func Aggregate(traces []*Trace) (*StatsResult, []string) {
	byStatus := map[string]int{"ok": 0, "degraded": 0, "failed": 0}
	grouped := map[string][]*Trace{}

	for _, t := range traces {
		byStatus[t.Status]++
		grouped[t.RootOperation] = append(grouped[t.RootOperation], t)
	}

	var notices []string
	eps := make([]EntrypointStat, 0, len(grouped))
	for op, ts := range grouped {
		durations := make([]float64, 0, len(ts))
		var failed, degraded int
		for _, t := range ts {
			durations = append(durations, t.DurationMs)
			switch t.Status {
			case "failed":
				failed++
			case "degraded":
				degraded++
			}
		}
		sort.Float64s(durations)
		if len(durations) < smallSampleThreshold {
			notices = append(notices,
				fmt.Sprintf("操作 %q 样本量较小（%d 条），百分位仅供参考", op, len(durations)))
		}
		eps = append(eps, EntrypointStat{
			Operation: op,
			Count:     len(ts),
			P50Ms:     percentile(durations, 0.50),
			P95Ms:     percentile(durations, 0.95),
			P99Ms:     percentile(durations, 0.99),
			Failed:    failed,
			Degraded:  degraded,
		})
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].Count > eps[j].Count })

	return &StatsResult{
		TotalTraces: len(traces),
		ByStatus:    byStatus,
		Entrypoints: eps,
	}, notices
}

// percentile 在已升序排列的切片上计算第 p 百分位数（p ∈ [0,1]），结果保留 3 位小数。
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return math.Round(sorted[idx]*1000) / 1000
}
