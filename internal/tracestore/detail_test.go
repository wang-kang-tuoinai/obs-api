package tracestore

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestSelfTimeUnion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration float64
		children []*Span
		want     float64
	}{
		{"parallel", 100, []*Span{{StartMs: 10, DurationMs: 60}, {StartMs: 10, DurationMs: 60}}, 40},
		{"partial overlap", 100, []*Span{{StartMs: 10, DurationMs: 50}, {StartMs: 40, DurationMs: 40}}, 30},
		{"outside parent", 100, []*Span{{StartMs: -10, DurationMs: 20}, {StartMs: 90, DurationMs: 50}, {StartMs: 150, DurationMs: 20}}, 80},
		{"microseconds", 1, []*Span{{StartUs: 100, DurationMs: 0.5}, {StartUs: 300, DurationMs: 0.5}}, 0.3},
		{"leaf", 5, nil, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Span{DurationMs: tc.duration, Children: tc.children}
			computeSelfMs(s)
			if math.Abs(s.SelfMs-tc.want) > 0.000001 {
				t.Fatalf("got %v want %v", s.SelfMs, tc.want)
			}
		})
	}
	child := &Span{DurationMs: 60, Children: []*Span{{DurationMs: 20}}}
	root := &Span{DurationMs: 100, Children: []*Span{child}}
	computeSelfMs(root)
	if root.SelfMs != 40 || child.SelfMs != 40 {
		t.Fatal(root.SelfMs, child.SelfMs)
	}
}

func TestDetailProjection(t *testing.T) {
	s := &Span{SpanID: "root", TraceID: "trace", StartMs: 1000, StartUs: 1000100, DurationMs: 10, Attrs: map[string]any{"http.response.status_code": 500}, Children: []*Span{
		{SpanID: "child", ParentSpanID: "root", StartUs: 1000250, Status: "error", StatusDesc: "create failed", Error: "MySQLError: duplicate", DurationMs: 1},
	}}
	tr := &Trace{TraceID: "trace", Root: s, SpanCount: 2, ErrorOrigin: "db", ErrorDesc: "duplicate", Warnings: []string{"original"}}
	d := BuildDetail(tr, 50)
	if d.ReturnedSpanCount != 2 || d.Truncated || d.Root.Children[0].StartOffsetMs != 0.15 {
		t.Fatal(d)
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, field := range []string{"parent_span_id", "error_origin", "error_desc"} {
		if strings.Contains(text, field) {
			t.Fatal(text)
		}
	}
	if strings.Count(text, `"trace_id"`) != 1 || !strings.Contains(text, `"status_desc":"create failed"`) || !strings.Contains(text, `"error":"MySQLError: duplicate"`) {
		t.Fatal(text)
	}
	d.Root.Attrs["new"] = "value"
	if _, ok := s.Attrs["new"]; ok {
		t.Fatal("projection mutated source attrs")
	}
	small := BuildDetail(tr, 1)
	if !small.Truncated || small.ReturnedSpanCount != 1 || len(small.Root.Children) != 0 || len(tr.Warnings) != 1 || len(s.Children) != 1 {
		t.Fatal("bad truncation")
	}
}

func TestJaegerPreservesMicroseconds(t *testing.T) {
	s := toSpan(jaegerSpan{StartTime: 1234567, Duration: 125}, nil)
	if s.StartUs != 1234567 || s.StartMs != 1234 || s.DurationMs != 0.125 {
		t.Fatal(s)
	}
}
