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
	Operation string `json:"operation"`
	Message   string `json:"message"`
}

type TraceSummary struct {
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
	matches := make([]*Trace, 0)
	for _, t := range traces {
		if t == nil || t.Root == nil {
			continue
		}
		// TODO 现在是单体服务，根据t.Root.Service != q.Service判断没问题，
		// 如果是多服务，根 Span 所属服务不一定是查询指定的服务，后续需明确下游入口语义。
		// Jaeger may match any span; enforce the public root-entrypoint contract here.
		if t.Root.Service != q.Service || (q.Operation != "" && t.RootOperation != q.Operation) || t.Root.StartMs < q.StartMs || t.Root.StartMs > q.EndMs {
			continue
		}
		if (q.Status != "" && t.Status != q.Status) || t.DurationMs < q.MinDurationMs {
			continue
		}
		matches = append(matches, t)
	}
	sort.Slice(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if q.Sort == "duration_desc" && a.DurationMs != b.DurationMs {
			return a.DurationMs > b.DurationMs
		}
		if a.Root.StartMs != b.Root.StartMs {
			return a.Root.StartMs > b.Root.StartMs
		}
		return a.TraceID < b.TraceID
	})
	matched := len(matches)
	if len(matches) > q.Limit {
		matches = matches[:q.Limit]
	}

	items := make([]TraceSummary, 0, len(matches))
	for _, t := range matches {
		item := TraceSummary{
			TraceID: t.TraceID, Service: t.Root.Service, Operation: t.RootOperation,
			StartMs: t.Root.StartMs, DurationMs: t.DurationMs, Status: t.Status,
			Warnings: t.Warnings,
		}
		if t.ErrorOrigin != "" {
			item.ErrorSummary = &ErrorSummary{Operation: shortTraceText(t.ErrorOrigin), Message: shortTraceText(t.ErrorDesc)}
		}
		items = append(items, item)
	}
	return &SearchResult{Items: items, MatchedCount: matched, HasMoreMatches: matched > len(items)}
}

func shortTraceText(s string) string {
	r := []rune(s)
	if len(r) > 240 {
		return string(r[:240]) + "…"
	}
	return s
}
