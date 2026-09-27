package tracestore

import (
	"fmt"
	"math"
	"sort"
)

// BuildTrace 从扁平的 span 列表组装成带树结构和分析结果的 Trace。
// 输入必须是已归一化的 span（jaeger.go 负责），本函数与数据源无关。
func BuildTrace(traceID string, spans []*Span) (*Trace, error) {
	if len(spans) == 0 {
		return nil, fmt.Errorf("trace %s: no spans", traceID)
	}

	nodes, roots, root, orphans, err := buildForest(spans)
	if err != nil {
		return nil, fmt.Errorf("trace %s: %w", traceID, err)
	}
	for _, r := range roots {
		markExpectedDuplicates(r, "")
		computeSelfMs(r)
	}
	t := &Trace{TraceID: traceID, SpanCount: len(nodes), Root: root, Roots: roots,
		Spans: nodes, Status: "unknown", Incomplete: root == nil || orphans > 0}
	if root != nil {
		t.RootOperation, t.DurationMs, t.Status = root.Operation, root.DurationMs, classifyStatus(root)
		if origin := findErrorOrigin(root); origin != nil {
			t.ErrorOrigin, t.ErrorDesc = origin.Operation, firstNonEmpty(origin.Error, origin.StatusDesc)
		}
	} else {
		t.Warnings = append(t.Warnings, "无法确定唯一全局根入口；保留独立片段，顶层状态为 unknown，不纳入根入口统计")
	}
	if orphans > 0 {
		t.Warnings = append(t.Warnings,
			fmt.Sprintf("%d 个 Span 缺少父节点，已保留为独立片段；分类仅基于已采集节点", orphans))
	}
	for _, s := range nodes {
		if s.Service == "" {
			t.Warnings = append(t.Warnings, "部分 Span 缺少 service，无法完整归属下游错误；不根据操作名或错误文本猜测服务")
			break
		}
	}
	return t, nil
}

// spanStartUs 保留源时间精度，兼容仅设置 StartMs 的调用方。
func spanStartUs(s *Span) int64 {
	if s.StartUs != 0 {
		return s.StartUs
	}
	return s.StartMs * 1000
}

// computeSelfMs 扣除直接子节点在父时间范围内的区间并集。
// 未覆盖时间可能包含未埋点等待，并非 CPU 时间。
func computeSelfMs(s *Span) {
	if s == nil {
		return
	}
	type interval struct{ start, end float64 }
	intervals := make([]interval, 0, len(s.Children))
	for _, c := range s.Children {
		if c == nil {
			continue
		}
		computeSelfMs(c)
		offset := float64(spanStartUs(c)-spanStartUs(s)) / 1000
		start, end := math.Max(0, offset), math.Min(s.DurationMs, offset+c.DurationMs)
		if end > start {
			intervals = append(intervals, interval{start, end})
		}
	}
	sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
	var covered, right float64
	for _, v := range intervals {
		if v.end > right {
			covered += v.end - math.Max(right, v.start)
			right = v.end
		}
	}
	s.SelfMs = math.Round(math.Max(0, s.DurationMs-covered)*1000) / 1000
}

// isErrorSpan 是统计、分类和摘要共用的有效错误判定；原始 Span 仍完整保留。
func isErrorSpan(s *Span) bool {
	return s != nil && (httpStatus(s) >= 500 || (s.ExpectedError == "" && (s.Status == "error" || s.Error != "")))
}

// classifyStatus 只观察选中入口及后代；4xx 不再提前返回 ok。
func classifyStatus(root *Span) string {
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
