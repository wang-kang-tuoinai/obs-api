package handler

import (
	"obs-api/internal/tracestore"
)

type TraceHandler struct {
	provider     tracestore.TraceProvider
	statsOptions tracestore.StatsOptions
}

func NewTraceHandler(provider tracestore.TraceProvider, options ...tracestore.StatsOptions) *TraceHandler {
	statsOptions := tracestore.DefaultStatsOptions()
	if len(options) > 0 {
		statsOptions = options[0]
	}
	return &TraceHandler{provider: provider, statsOptions: statsOptions}
}
