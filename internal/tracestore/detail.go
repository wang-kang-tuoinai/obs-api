package tracestore

import "fmt"

// Detail 模型仅用于对外展示，不修改内部 Trace/Span。
type TraceDetail struct {
	TraceID           string      `json:"trace_id"`
	RootOperation     string      `json:"root_operation"`
	StartMs           int64       `json:"start_ms"`
	DurationMs        float64     `json:"duration_ms"`
	Status            string      `json:"status"`
	SpanCount         int         `json:"span_count"`
	ReturnedSpanCount int         `json:"returned_span_count"`
	Truncated         bool        `json:"truncated"`
	Root              *SpanDetail `json:"root,omitempty"`
	Warnings          []string    `json:"warnings,omitempty"`
}

type SpanDetail struct {
	SpanID        string         `json:"span_id"`
	Service       string         `json:"service"`
	Operation     string         `json:"operation"`
	Kind          string         `json:"kind"`
	StartOffsetMs float64        `json:"start_offset_ms"`
	DurationMs    float64        `json:"duration_ms"`
	SelfMs        float64        `json:"self_ms"`
	Status        string         `json:"status"`
	StatusDesc    string         `json:"status_desc,omitempty"`
	Error         string         `json:"error,omitempty"`
	Attrs         map[string]any `json:"attrs,omitempty"`
	Children      []*SpanDetail  `json:"children,omitempty"`
}

// BuildDetail 按树的先序遍历保留最多 maxSpans 个节点，始终保留祖先路径。
// 状态和耗时来自完整分析结果，裁剪仅影响展示。
func BuildDetail(t *Trace, maxSpans int) *TraceDetail {
	if t == nil {
		return nil
	}
	if maxSpans < 1 || maxSpans > 200 {
		maxSpans = 50
	}
	d := &TraceDetail{TraceID: t.TraceID, RootOperation: t.RootOperation, DurationMs: t.DurationMs,
		Status: t.Status, SpanCount: t.SpanCount, Warnings: append([]string(nil), t.Warnings...)}
	if t.Root == nil {
		d.Warnings = append(d.Warnings, "未获取到根 Span，无法展示链路")
		return d
	}
	d.StartMs = t.Root.StartMs
	var convert func(*Span) *SpanDetail
	convert = func(s *Span) *SpanDetail {
		if s == nil {
			return nil
		}
		if d.ReturnedSpanCount >= maxSpans {
			d.Truncated = true
			return nil
		}
		d.ReturnedSpanCount++
		n := &SpanDetail{SpanID: s.SpanID, Service: s.Service, Operation: s.Operation, Kind: s.Kind,
			StartOffsetMs: float64(spanStartUs(s)-spanStartUs(t.Root)) / 1000,
			DurationMs:    s.DurationMs, SelfMs: s.SelfMs, Status: s.Status, StatusDesc: s.StatusDesc, Error: s.Error}
		if len(s.Attrs) > 0 {
			n.Attrs = make(map[string]any, len(s.Attrs))
			for k, v := range s.Attrs {
				n.Attrs[k] = v
			}
		}
		for _, c := range s.Children {
			if child := convert(c); child != nil {
				n.Children = append(n.Children, child)
			}
		}
		return n
	}
	d.Root = convert(t.Root)
	if d.Truncated {
		d.Warnings = append(d.Warnings, fmt.Sprintf("展示已裁剪为 %d 个 Span，可能省略错误节点；状态和自身耗时仍基于完整已解析树", maxSpans))
	}
	return d
}
