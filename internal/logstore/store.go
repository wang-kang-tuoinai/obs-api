package logstore

import "context"


type LogStore interface {
	QueryStats(ctx context.Context, q StatsQuery) (*StatsResult, error)
	QueryTemplates(ctx context.Context, q TemplatesQuery) ([]TemplateStat, error)
}
