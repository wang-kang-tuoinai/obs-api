package handler

import (
	"context"

	"obs-api/internal/logstore"
	"obs-api/internal/tracestore"
)

type ServiceLister interface {
	GetServices(context.Context) ([]string, error)
}
type VisualHandler struct {
	cache          *tracestore.VisualCache
	services       ServiceLister
	defaultService string
	logs           *logstore.LogVisualCache
}

func NewVisualHandler(cache *tracestore.VisualCache, services ServiceLister, defaultService string, logs *logstore.LogVisualCache) *VisualHandler {
	return &VisualHandler{cache, services, defaultService, logs}
}
