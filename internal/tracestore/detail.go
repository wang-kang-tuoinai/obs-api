package tracestore

import "fmt"

// Detail 模型仅用于对外展示，不修改内部 Trace/Span。
type TraceDetail struct {
	TraceID           string        `json:"trace_id"`
	RootOperation     string        `json:"root_operation"`
	StartMs           int64         `json:"start_ms"`
	DurationMs        float64       `json:"duration_ms"`
	Status            string        `json:"status"`
	SpanCount         int           `json:"span_count"`
	ReturnedSpanCount int           `json:"returned_span_count"`
	Truncated         bool          `json:"truncated"`
	Root              *SpanDetail   `json:"root,omitempty"`
	Fragments         []*SpanDetail `json:"fragments,omitempty"` // 除唯一全局根外的独立片段（父 Span 缺失的孤儿或额外顶层 Span）；无唯一根时全部在此
	Incomplete        bool          `json:"incomplete"` // 数据不完整：缺唯一全局根，或存在父 Span 缺失的孤儿
	Warnings          []string      `json:"warnings,omitempty"`
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
	ExpectedError string         `json:"expected_error,omitempty"`
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
		Status: t.Status, SpanCount: t.SpanCount, Incomplete: t.Incomplete, Warnings: append([]string(nil), t.Warnings...)}
	roots := traceRoots(t)
	var referenceUs int64
	if t.Root != nil {
		d.StartMs, referenceUs = t.Root.StartMs, spanStartUs(t.Root)
	} else if len(roots) > 0 {
		d.StartMs, referenceUs = roots[0].StartMs, spanStartUs(roots[0])
		d.Warnings = append(d.Warnings, "无唯一全局根，fragments 展示独立片段；start_ms 和偏移参考最早片段开始时间")
	}
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
			StartOffsetMs: float64(spanStartUs(s)-referenceUs) / 1000,
			DurationMs:    s.DurationMs, SelfMs: s.SelfMs, Status: s.Status, StatusDesc: s.StatusDesc, Error: s.Error, ExpectedError: s.ExpectedError}
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
	for _, r := range roots {
		if r != t.Root {
			if fragment := convert(r); fragment != nil {
				d.Fragments = append(d.Fragments, fragment)
			}
		}
	}
	if d.Truncated {
		d.Warnings = append(d.Warnings, fmt.Sprintf("展示已裁剪为 %d 个 Span，可能省略错误节点；状态和自身耗时仍基于完整已解析树", maxSpans))
	}
	return d
}
