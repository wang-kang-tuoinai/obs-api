package logstore

const LevelError = "ERROR"

type StatsQuery struct {
	Service string
	Route   string
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
