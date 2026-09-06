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

	root, orphans := buildTree(spans)
	if root == nil {
		return nil, fmt.Errorf("trace %s: no root span found", traceID)
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
// isErrorSpan 判断单个 span 是否出错：Status 标记为 error，或从 logs 提取到了异常消息。
func isErrorSpan(s *Span) bool {
	return s.Status == "error" || s.Error != ""
}

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
