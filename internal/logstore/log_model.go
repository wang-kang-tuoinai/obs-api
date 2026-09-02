package logstore

import "encoding/json"

const LevelError = "ERROR"

type StatsQuery struct {
	Service string
	Route   string
	Method  string
	Level   string
	Start   int64 // 秒级
	End     int64
	TopN    int // top_templates 取几条
}

type TemplateItem struct {
	Template string
	Level    string
	Count    int64
}

type StatsResult struct {
	ByLevel      map[string]int64
	TopTemplates []TemplateItem
}

type Window struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"`
}
type StatsSummary struct {
	Window       Window           `json:"window"`
	Total        int64            `json:"total"`
	ErrorCount   int64            `json:"error_count"`
	ErrorRate    float64          `json:"error_rate"`
	ByLevel      map[string]int64 `json:"by_level"`
	TopTemplates []TemplateItem   `json:"top_templates"`
}

type LogStatsResponse struct {
	Summary     StatsSummary `json:"summary"`
	GeneratedAt int64        `json:"generated_at"`
}

type TemplatesQuery struct {
	Service string
	Level   string
	Route   string
	Method  string
	Start   int64 // 秒级
	End     int64
	Limit   int // 返回模板数上限
}

type TemplateSample struct {
	Ts      int64           `json:"ts"`
	TraceID string          `json:"trace_id"`
	Attrs   json.RawMessage `json:"attrs"`
}

type TemplateStat struct {
	Template  string         `json:"template"`
	Count     int64          `json:"count"`
	FirstSeen int64          `json:"first_seen"`
	LastSeen  int64          `json:"last_seen"`
	Sample    TemplateSample `json:"sample"`
}

type TemplatesResponse struct {
	Items []TemplateStat `json:"items"`
}

type SearchQuery struct {
	Service  string
	Level    string
	Route    string
	Method   string
	TraceID  string
	Template string
	Keyword  string
	Start    int64 // 秒级
	End      int64
	Limit    int
	CursorTs int64  // 游标 ts（毫秒）
	CursorID uint64 // 游标 id
}

type LogItem struct {
	Ts       int64           `json:"ts"`
	Level    string          `json:"level"`
	Service  string          `json:"service"`
	Route    string          `json:"route"`
	Method   string          `json:"method"`
	Template string          `json:"template"`
	Attrs    json.RawMessage `json:"attrs"`
	TraceID  string          `json:"trace_id"`
}

type SearchResult struct {
	Items      []LogItem `json:"items"`
	NextCursor *string   `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
}
