package handler

import "obs-api/internal/tracestore"

// TraceStatsResponse 是统计接口的成功响应。
type TraceStatsResponse struct {
	Service string                  `json:"service"`
	Stats   *tracestore.StatsResult `json:"stats"`
	Meta    TraceStatsMeta          `json:"meta"`
	Notices []string                `json:"notices"`
}

type TraceStatsMeta struct {
	Window        TraceWindow `json:"window"`
	FetchLimit    int         `json:"fetch_limit"`
	FetchedTraces int         `json:"fetched_traces"`
}

// TraceWindow 使用秒级 Unix 时间戳，与 HTTP 查询参数一致。
type TraceWindow struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}

type TraceSearchMeta struct {
	Window         TraceWindow `json:"window"`
	FetchLimit     int         `json:"fetch_limit"`
	FetchedCount   int         `json:"fetched_count"`
	MatchedCount   int         `json:"matched_count"`
	ReturnedCount  int         `json:"returned_count"`
	HasMoreMatches bool        `json:"has_more_matches"`
}

type TraceSearchResponse struct {
	Items   []tracestore.TraceSummary `json:"items"`
	Meta    TraceSearchMeta           `json:"meta"`
	Notices []string                  `json:"notices"`
}
