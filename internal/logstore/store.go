package logstore

import "context"


type LogStore interface {
	QueryStats(ctx context.Context, q StatsQuery) (*StatsResult, error)
}
