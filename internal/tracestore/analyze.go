package tracestore

import (
	"fmt"
	"math"
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

// findErrorOrigin 按已排序子树深度优先查找，优先返回后代中的代表性出错节点。
// 该节点不保证是全局最早错误，也不代表已经确认的根因。
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
