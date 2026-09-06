package tracestore

import "context"
type Span struct {
	TraceID      string         `json:"trace_id,omitempty"`
	SpanID       string         `json:"span_id"`
	ParentSpanID string         `json:"parent_span_id,omitempty"`
	Service      string         `json:"service"`
	Operation    string         `json:"operation"`
	Kind         string         `json:"kind"`
	StartMs      int64          `json:"start_ms"`
	DurationMs   float64        `json:"duration_ms"`
	SelfMs       float64        `json:"self_ms"`
	Status       string         `json:"status"`
	StatusDesc   string         `json:"status_desc,omitempty"`
	Error        string         `json:"error,omitempty"`
	Attrs        map[string]any `json:"attrs,omitempty"`
	Children     []*Span        `json:"children,omitempty"`
}

type Trace struct {
	TraceID       string   `json:"trace_id"`
	RootOperation string   `json:"root_operation"`
	DurationMs    float64  `json:"duration_ms"`
	Status        string   `json:"status"` // ok/degraded/failed
	ErrorOrigin   string   `json:"error_origin,omitempty"`
	ErrorDesc     string   `json:"error_desc,omitempty"`
	SpanCount     int      `json:"span_count"`
	Root          *Span    `json:"root,omitempty"`
	Warnings      []string `json:"warnings,omitempty"`
}



type TraceProvider interface {
    GetTrace(ctx context.Context, traceID string) (*Trace, error)
    // FindTraces(ctx context.Context, q TraceQuery) ([]*Trace, error)
}