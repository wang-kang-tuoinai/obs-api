package tracestore

import "context"

type Span struct {
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
	Service       string         `json:"service"`
	Operation     string         `json:"operation"`
	Kind          string         `json:"kind"`
	StartMs       int64          `json:"start_ms"`
	StartUs       int64          `json:"-"` // 内部保留 Jaeger 微秒精度；旧数据回退到 StartMs
	DurationMs    float64        `json:"duration_ms"`
	SelfMs        float64        `json:"self_ms"`
	Status        string         `json:"status"`
	StatusDesc    string         `json:"status_desc,omitempty"`
	Error         string         `json:"error,omitempty"`
	Attrs         map[string]any `json:"attrs,omitempty"`
	Children      []*Span        `json:"children,omitempty"`
	ExpectedError string         `json:"-"` // 派生标记；原始 status/error 不改写。
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
	Roots         []*Span  `json:"-"` // 包含父节点缺失的独立片段。
	Spans         []*Span  `json:"-"`
	Incomplete    bool     `json:"incomplete"`
}

type TraceProvider interface {
	GetTrace(ctx context.Context, traceID string) (*Trace, error)
	GetOperations(ctx context.Context, service string) ([]string, error) // 仅 server，未按时间窗口过滤。
	FindTraces(ctx context.Context, q TraceQuery) (TraceBatch, error)
}

// TraceBatch 保留原始候选数量，不能用解析成功的数量判断是否触及上限。
type TraceBatch struct {
	Traces   []*Trace
	RawCount int
	Notices  []string
}
