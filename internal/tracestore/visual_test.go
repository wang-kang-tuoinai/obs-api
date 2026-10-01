package tracestore

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type visualProvider struct {
	ops     []string
	queries []TraceQuery
	find    func(TraceQuery) (TraceBatch, error)
	dirErr  error
}

func (p *visualProvider) GetTrace(context.Context, string) (*Trace, error) { return nil, nil }
func (p *visualProvider) GetOperations(context.Context, string) ([]string, error) {
	return p.ops, p.dirErr
}
func (p *visualProvider) FindTraces(_ context.Context, q TraceQuery) (TraceBatch, error) {
	p.queries = append(p.queries, q)
	return p.find(q)
}
func newManualVisual(p TraceProvider) (*VisualCache, *visualCell) {
	m := &VisualCache{provider: p, opts: DefaultVisualOptions(), ctx: context.Background(), cells: map[visualKey]*visualCell{}, jobs: make(chan *visualCell, 8)}
	c := &visualCell{key: visualKey{service: "user"}, points: map[pointKey]VisualPoint{}, attempt: time.Now()}
	m.cells[c.key] = c
	return m, c
}
func visualTrace(t *testing.T, id, op string, start int64, failed bool) *Trace {
	t.Helper()
	s := &Span{SpanID: "entry", Kind: "server", Service: "user", Operation: op, StartMs: start, DurationMs: 30, Status: "ok"}
	spans := []*Span{s}
	if failed {
		spans = append(spans, &Span{SpanID: "db", ParentSpanID: "entry", Service: "db", Status: "error", Error: "timeout", StartMs: start})
	}
	trace, err := BuildTrace(id, spans)
	if err != nil {
		t.Fatal(err)
	}
	return trace
}
func TestVisualOverlapUpsertAndFullReloadAfterGap(t *testing.T) {
	now := time.Now()
	start := now.Add(-30 * time.Second).UnixMilli()
	round := 0
	p := &visualProvider{ops: []string{"GET", "POST", "GET"}}
	p.find = func(q TraceQuery) (TraceBatch, error) {
		if q.Operation == "POST" {
			return TraceBatch{Traces: []*Trace{visualTrace(t, "post", "POST", start, false)}, RawCount: 1}, nil
		}
		trace := visualTrace(t, "same", "GET", start, round > 0)
		// A second server entry in the same Trace must not be collapsed by trace_id.
		s := &Span{SpanID: "entry2", Kind: "server", Service: "user", Operation: "GET", StartMs: start + 1, DurationMs: 2}
		trace.Roots = append(trace.Roots, s)
		return TraceBatch{Traces: []*Trace{trace}, RawCount: 1}, nil
	}
	m, c := newManualVisual(p)
	m.refresh(c, now)
	if len(c.points) != 3 || len(p.queries) != 2 || p.queries[0].Start != now.Add(-15*time.Minute).UnixMilli() {
		t.Fatalf("first load points=%d queries=%+v", len(c.points), p.queries)
	}
	round++
	p.queries = nil
	m.refresh(c, now.Add(15*time.Second))
	if len(c.points) != 3 || c.points[pointKey{"same", "entry"}].Status != "degraded" || p.queries[0].Start != now.Add(-105*time.Second).UnixMilli() {
		t.Fatalf("overlap: %+v %+v", c.points, p.queries)
	}
	filtered, err := m.Snapshot("user", "POST", 0, 0)
	if err != nil || len(filtered.Points) != 1 || filtered.ByStatus["ok"] != 1 {
		t.Fatalf("filter=%+v %v", filtered, err)
	}
	if len(p.queries) != 2 {
		t.Fatal("operation filter queried Jaeger")
	}
	p.queries = nil
	m.refresh(c, now.Add(3*time.Minute))
	if p.queries[0].Start != now.Add(-12*time.Minute).UnixMilli() {
		t.Fatal("idle gap did not trigger full reload", p.queries)
	}
}
func TestVisualFailureDoesNotDeleteAndPartialPersists(t *testing.T) {
	now := time.Now()
	p := &visualProvider{ops: []string{"GET"}}
	p.find = func(q TraceQuery) (TraceBatch, error) {
		return TraceBatch{Traces: []*Trace{visualTrace(t, "old", "GET", now.Add(-3*time.Minute).UnixMilli(), false)}, RawCount: 1500}, nil
	}
	m, c := newManualVisual(p)
	m.refresh(c, now)
	if !c.partial {
		t.Fatal("cap not exposed")
	}
	p.find = func(TraceQuery) (TraceBatch, error) { return TraceBatch{}, errors.New("offline") }
	m.refresh(c, now.Add(15*time.Second))
	if len(c.points) != 1 || c.err == "" {
		t.Fatal("failure erased cache or hidden")
	}
	p.find = func(TraceQuery) (TraceBatch, error) { return TraceBatch{}, nil }
	m.refresh(c, now.Add(30*time.Second))
	if len(c.points) != 1 || !c.partial {
		t.Fatal("absence erased old point or incremental refresh cleared earlier gap")
	}
	m.refresh(c, now.Add(6*time.Minute))
	if c.partial {
		t.Fatal("clean full refresh did not clear query warnings")
	}
}
func TestVisualExpiryCapAndNewOperation(t *testing.T) {
	now := time.Now()
	p := &visualProvider{ops: []string{"GET"}, find: func(TraceQuery) (TraceBatch, error) { return TraceBatch{}, nil }}
	m, c := newManualVisual(p)
	m.opts.MaxPoints = 2
	c.points[pointKey{"expired", "entry"}] = VisualPoint{TraceID: "expired", EntrySpanID: "entry", StartMs: now.Add(-16 * time.Minute).UnixMilli()}
	m.refresh(c, now)
	if len(c.points) != 0 {
		t.Fatal("old point not evicted")
	}
	p.ops = []string{"GET", "NEW"}
	p.queries = nil
	m.refresh(c, now.Add(15*time.Second))
	if p.queries[0].Start != now.Add(-105*time.Second).UnixMilli() || p.queries[1].Start != now.Add(-885*time.Second).UnixMilli() {
		t.Fatalf("new operation scope=%+v", p.queries)
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprint(i)
		c.points[pointKey{id, "s"}] = VisualPoint{TraceID: id, EntrySpanID: "s", StartMs: now.UnixMilli() + int64(i)}
	}
	m.refresh(c, now.Add(30*time.Second))
	if len(c.points) != 2 || !c.partial {
		t.Fatal("point cap not applied")
	}
}

type blockedVisualProvider struct {
	calls   atomic.Int32
	release chan struct{}
}

func (p *blockedVisualProvider) GetTrace(context.Context, string) (*Trace, error) { return nil, nil }
func (p *blockedVisualProvider) GetOperations(ctx context.Context, _ string) ([]string, error) {
	p.calls.Add(1)
	select {
	case <-p.release:
		return []string{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (p *blockedVisualProvider) FindTraces(context.Context, TraceQuery) (TraceBatch, error) {
	return TraceBatch{}, nil
}
func TestVisualSharedJobBoundedCacheAndClose(t *testing.T) {
	p := &blockedVisualProvider{release: make(chan struct{})}
	opts := DefaultVisualOptions()
	opts.MaxCaches = 1
	m := NewVisualCache(p, opts)
	for i := 0; i < 20; i++ {
		r, err := m.Snapshot("user", "GET", 0, 0)
		if err != nil || !r.Loading {
			t.Fatalf("snapshot=%+v %v", r, err)
		}
	}
	if _, err := m.Snapshot("other", "", 0, 0); err == nil {
		t.Fatal("busy cache limit not enforced")
	}
	deadline := time.Now().Add(time.Second)
	for p.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if p.calls.Load() != 1 {
		t.Fatal("duplicate refresh", p.calls.Load())
	}
	m.Close() // cancellation must unblock provider and stop worker
}

func TestVisualHistoricalWindowAndDirectoryFailure(t *testing.T) {
	p := &visualProvider{ops: []string{"GET"}, find: func(TraceQuery) (TraceBatch, error) { return TraceBatch{}, nil }}
	m, c := newManualVisual(p)
	delete(m.cells, c.key)
	c.key = visualKey{"user", 1000, 5000}
	m.cells[c.key] = c
	m.refresh(c, time.Now())
	if p.queries[0].Start != 1000 || p.queries[0].End != 5000 {
		t.Fatal(p.queries)
	}
	p.dirErr = errors.New("directory offline")
	m.refresh(c, time.Now())
	if c.err == "" || !c.partial {
		t.Fatal("directory failure hidden")
	}
}

// Synthetic workload only: isolates local parsing/tree analysis, excludes Jaeger/network cost.
func BenchmarkVisual4500Traces(b *testing.B) {
	spans := make([]*Span, 20)
	spans[0] = &Span{SpanID: "0", Kind: "server", Service: "user", Operation: "GET", StartMs: 1000, DurationMs: 30}
	for i := 1; i < len(spans); i++ {
		spans[i] = &Span{SpanID: fmt.Sprint(i), ParentSpanID: "0", Service: "user", Operation: "child", StartMs: 1000, DurationMs: 1, Attrs: map[string]any{"component": "mysql"}}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i < 4500; i++ {
			trace, err := BuildTrace("t", spans)
			if err != nil {
				b.Fatal(err)
			}
			_ = classifyStatus(trace.Root)
		}
	}
}
