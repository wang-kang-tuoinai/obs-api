package tracestore

// ServiceEntry 关联服务入口与原 Trace，不修改原始树或全局 Root。
type ServiceEntry struct {
	Trace *Trace
	Span  *Span
}

// SelectServiceEntries 共用 stats/search 的基础筛选，仅使用 service、operation 和入口时间。
// 不做状态、耗时、排序或数量截断；缺少全局根不影响可识别的 server 入口。
func SelectServiceEntries(traces []*Trace, q SearchOptions) ([]ServiceEntry, []string) {
	entries := make([]ServiceEntry, 0)
	notices := make([]string, 0)
	seen := map[[2]string]bool{}
	seenNotice := map[string]bool{}
	for _, t := range traces {
		if t == nil {
			continue
		}
		// warning去重
		for _, warning := range t.Warnings {
			if !seenNotice[warning] {
				seenNotice[warning] = true
				notices = append(notices, warning)
			}
		}
		// DFS找server入口，按 (trace_id, entry_span_id) 去重
		var visit func(*Span)
		visit = func(s *Span) {
			if s == nil {
				return
			}
			key := [2]string{t.TraceID, s.SpanID}
			if s.Kind == "server" && matchesEntry(s, q) && !seen[key] {
				seen[key] = true
				entries = append(entries, ServiceEntry{Trace: t, Span: s})
			}
			for _, child := range s.Children {
				visit(child)
			}
		}
		for _, root := range traceRoots(t) {
			visit(root)
		}
	}
	return entries, notices
}
