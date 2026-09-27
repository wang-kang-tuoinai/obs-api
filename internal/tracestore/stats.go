package tracestore

import (
	"fmt"
	"math"
	"sort"
)

// ---- 聚合统计 ----

// StatsResult 是 Aggregate 的输出，描述一批 Trace 的整体健康状况。
type StatsResult struct {
	TotalTraces int              `json:"total_traces"`
	ByStatus    map[string]int   `json:"by_status"`   // ok / degraded / failed
	Entrypoints []EntrypointStat `json:"entrypoints"` // 按调用量降序
}

// EntrypointStat 描述某个入口操作（root operation）的统计数据。
type EntrypointStat struct {
	Service                 string                   `json:"service"`
	Operation               string                   `json:"operation"`
	Count                   int                      `json:"count"`
	P50Ms                   float64                  `json:"p50_ms"`
	P95Ms                   float64                  `json:"p95_ms"`
	P99Ms                   float64                  `json:"p99_ms"`
	Failed                  int                      `json:"failed"`
	Degraded                int                      `json:"degraded"`
	DownstreamErrorServices []DownstreamErrorService `json:"downstream_error_services"`
}

type DownstreamErrorService struct {
	Service      string `json:"service"`
	RequestCount int    `json:"request_count"`
}

// FilterRootEntries 只保留唯一、明确的 server 根入口，Jaeger 的任意 Span 匹配不足以证明入口匹配。
func FilterRootEntries(traces []*Trace, q SearchOptions) ([]*Trace, []string) {
	matched := make([]*Trace, 0)
	var notices []string
	seen := map[string]bool{}
	for _, t := range traces {
		if t == nil {
			continue
		}
		r := t.Root
		if r == nil || r.ParentSpanID != "" || r.Kind != "server" {
			notices = append(notices, fmt.Sprintf("Trace %s 未识别到唯一 server 根入口，未纳入入口统计", t.TraceID))
			continue
		}
		if !matchesEntry(r, q) || seen[t.TraceID] {
			continue
		}
		seen[t.TraceID] = true
		matched = append(matched, t)
	}
	return matched, notices
}

const smallSampleThreshold = 20

// Aggregate 将已筛选根入口按 service/operation 分组，计算百分位、状态和下游错误请求数。
// 返回聚合结果和可附加到响应 notices 的提示列表。
func Aggregate(traces []*Trace) (*StatsResult, []string) {
	byStatus := map[string]int{"ok": 0, "degraded": 0, "failed": 0}
	type entryKey struct{ service, operation string }
	grouped := map[entryKey][]*Trace{}
	total := 0

	for _, t := range traces {
		if t == nil || t.Root == nil {
			continue
		}
		byStatus[classifyStatus(t.Root)]++
		key := entryKey{t.Root.Service, t.Root.Operation}
		grouped[key] = append(grouped[key], t)
		total++
	}

	var notices []string
	eps := make([]EntrypointStat, 0, len(grouped))
	for op, ts := range grouped {
		durations := make([]float64, 0, len(ts))
		var failed, degraded int
		serviceCounts := map[string]int{}
		for _, t := range ts {
			notices = append(notices, t.Warnings...)
			durations = append(durations, t.Root.DurationMs)
			// 按入口请求去重；同一请求可同时影响多个下游服务。
			seen := map[string]bool{}
			var collect func(*Span)
			collect = func(s *Span) {
				if isErrorSpan(s) && s.Service != "" && s.Service != t.Root.Service {
					seen[s.Service] = true
				}
				for _, c := range s.Children {
					collect(c)
				}
			}
			for _, c := range t.Root.Children {
				collect(c)
			}
			for service := range seen {
				serviceCounts[service]++
			}
			switch classifyStatus(t.Root) {
			case "failed":
				failed++
			case "degraded":
				degraded++
			}
		}
		sort.Float64s(durations)
		if len(durations) < smallSampleThreshold {
			notices = append(notices,
				fmt.Sprintf("服务 %q 操作 %q 样本量较小（%d 条），百分位仅供参考", op.service, op.operation, len(durations)))
		}
		downstream := make([]DownstreamErrorService, 0, len(serviceCounts))
		for service, count := range serviceCounts {
			downstream = append(downstream, DownstreamErrorService{service, count})
		}
		sort.Slice(downstream, func(i, j int) bool {
			if downstream[i].RequestCount != downstream[j].RequestCount {
				return downstream[i].RequestCount > downstream[j].RequestCount
			}
			return downstream[i].Service < downstream[j].Service
		})
		eps = append(eps, EntrypointStat{
			Service:                 op.service,
			Operation:               op.operation,
			DownstreamErrorServices: downstream,
			Count:                   len(ts),
			P50Ms:                   percentile(durations, 0.50),
			P95Ms:                   percentile(durations, 0.95),
			P99Ms:                   percentile(durations, 0.99),
			Failed:                  failed,
			Degraded:                degraded,
		})
	}
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].Count != eps[j].Count {
			return eps[i].Count > eps[j].Count
		}
		if eps[i].Service != eps[j].Service {
			return eps[i].Service < eps[j].Service
		}
		return eps[i].Operation < eps[j].Operation
	})

	return &StatsResult{
		TotalTraces: total,
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
