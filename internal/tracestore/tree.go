package tracestore

import (
	"fmt"
	"sort"
)

// buildForest 保留所有可用片段，不将缺失父节点的 Span 冒充全局根。
// 复制节点后建树，重复分析不会重复挂载 Children，也不改写输入。
func buildForest(input []*Span) (spans, roots []*Span, root *Span, orphans int, err error) {
	byID := make(map[string]*Span, len(input))
	for _, s := range input {
		if s == nil || s.SpanID == "" {
			return nil, nil, nil, 0, fmt.Errorf("empty span or span_id")
		}
		if byID[s.SpanID] != nil {
			return nil, nil, nil, 0, fmt.Errorf("duplicate span_id %s", s.SpanID)
		}
		n := *s
		n.Children, n.ExpectedError = nil, ""
		byID[n.SpanID] = &n
		spans = append(spans, &n)
	}
	// 先校验父链，避免环导致后续递归无法结束。
	state := map[string]int{}
	var visit func(*Span) error
	visit = func(s *Span) error {
		if state[s.SpanID] == 1 {
			return fmt.Errorf("cycle at span %s", s.SpanID)
		}
		if state[s.SpanID] == 2 {
			return nil
		}
		state[s.SpanID] = 1
		if p := byID[s.ParentSpanID]; p != nil {
			if err := visit(p); err != nil {
				return err
			}
		}
		state[s.SpanID] = 2
		return nil
	}
	for _, s := range spans {
		if err := visit(s); err != nil {
			return nil, nil, nil, 0, err
		}
	}
	rootCount := 0
	for _, s := range spans {
		if s.ParentSpanID == "" {
			root, rootCount = s, rootCount+1
			roots = append(roots, s)
		} else if p := byID[s.ParentSpanID]; p != nil {
			p.Children = append(p.Children, s)
		} else {
			orphans++
			roots = append(roots, s)
		}
	}
	if rootCount != 1 {
		root = nil
	}
	sortSpans(spans)
	sortSpans(roots)
	for _, r := range roots {
		sortChildren(r)
	}
	return spans, roots, root, orphans, nil
}

func sortSpans(spans []*Span) {
	sort.Slice(spans, func(i, j int) bool {
		if spanStartUs(spans[i]) != spanStartUs(spans[j]) {
			return spanStartUs(spans[i]) < spanStartUs(spans[j])
		}
		return spans[i].SpanID < spans[j].SpanID
	})
}

func sortChildren(s *Span) {
	if s == nil {
		return
	}
	sortSpans(s.Children)
	for _, c := range s.Children {
		sortChildren(c)
	}
}

func traceRoots(t *Trace) []*Span {
	if len(t.Roots) > 0 {
		return t.Roots
	}
	if t.Root != nil {
		return []*Span{t.Root}
	}
	return nil
}
