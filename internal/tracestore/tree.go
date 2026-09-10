package tracestore

import "sort"

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
