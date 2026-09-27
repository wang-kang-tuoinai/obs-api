package tracestore

import "sort"

// SearchOptions 是已由调用方校验的检索条件；时间使用毫秒，Limit 必须为正。
type SearchOptions struct {
	Service       string
	Operation     string
	StartMs       int64
	EndMs         int64
	Status        string
	MinDurationMs float64
	Sort          string
	Limit         int
}

type ErrorSummary struct {
	Service   string `json:"service"`
	SpanID    string `json:"span_id"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
}

type TraceSummary struct {
	EntrySpanID  string        `json:"entry_span_id"`
	TraceID      string        `json:"trace_id"`
	Service      string        `json:"service"`
	Operation    string        `json:"operation"`
	StartMs      int64         `json:"start_ms"`
	DurationMs   float64       `json:"duration_ms"`
	Status       string        `json:"status"`
	ErrorSummary *ErrorSummary `json:"error_summary,omitempty"`
	Warnings     []string      `json:"warnings,omitempty"`
}

type SearchResult struct {
	Items          []TraceSummary
	MatchedCount   int
	HasMoreMatches bool
}

// Search 在已获取候选内筛选、排序并生成摘要，不请求数据源、不修改输入切片。
func Search(traces []*Trace, q SearchOptions) *SearchResult {
	matches := make([]TraceSummary, 0)
	seen := map[[2]string]bool{}
	for _, t := range traces {
		if t == nil {
			continue
		}
		var visit func(*Span)
		visit = func(s *Span) {
			if s.Kind == "server" && matchesEntry(s, q) && s.DurationMs >= q.MinDurationMs {
				status := classifyStatus(s)
				key := [2]string{t.TraceID, s.SpanID}
				if (q.Status == "" || q.Status == status) && !seen[key] {
					seen[key] = true
					item := TraceSummary{TraceID: t.TraceID, EntrySpanID: s.SpanID, Service: s.Service,
						Operation: s.Operation, StartMs: s.StartMs, DurationMs: s.DurationMs,
						Status: status, Warnings: append([]string(nil), t.Warnings...)}
					if origin := findErrorOrigin(s); origin != nil {
						item.ErrorSummary = &ErrorSummary{Service: origin.Service, SpanID: origin.SpanID,
							Operation: shortTraceText(origin.Operation), Message: shortTraceText(firstNonEmpty(origin.Error, origin.StatusDesc))}
					}
					matches = append(matches, item)
				}
			}
			for _, child := range s.Children {
				visit(child)
			}
		}
		for _, root := range traceRoots(t) {
			visit(root)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if q.Sort == "duration_desc" && a.DurationMs != b.DurationMs {
			return a.DurationMs > b.DurationMs
		}
		if a.StartMs != b.StartMs {
			return a.StartMs > b.StartMs
		}
		if a.TraceID != b.TraceID {
			return a.TraceID < b.TraceID
		}
		return a.EntrySpanID < b.EntrySpanID
	})
	matched := len(matches)
	if len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}

	return &SearchResult{Items: matches, MatchedCount: matched, HasMoreMatches: matched > len(matches)}
}

func matchesEntry(s *Span, q SearchOptions) bool {
	return s.Service == q.Service && (q.Operation == "" || s.Operation == q.Operation) && s.StartMs >= q.StartMs && s.StartMs <= q.EndMs
}

func shortTraceText(s string) string {
	r := []rune(s)
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return s
}
