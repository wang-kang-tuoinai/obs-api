package logstore

import "context"

type StatsQuery struct {
	Service string
	Route   string
	Level   string
	Start   int64 // 秒级
	End     int64
	TopN    int // top_templates 取几条
}

type LevelCount struct {
	Level string
	Count int64
}

type TemplateCount struct {
	Template string
	Level    string
	Count    int64
}

type StatsResult struct {
	ByLevel      []LevelCount
	TopTemplates []TemplateCount
}

type LogStore interface {
	QueryStats(ctx context.Context, q StatsQuery) (*StatsResult, error)
}
