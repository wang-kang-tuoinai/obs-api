package tracestore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type statsProvider struct {
	mu           sync.Mutex
	ops          []string
	discoveryErr error
	listCalls    int
	queries      []TraceQuery
	find         func(context.Context, TraceQuery) (TraceBatch, error)
}

func (p *statsProvider) GetTrace(context.Context, string) (*Trace, error) { return nil, nil }
func (p *statsProvider) GetOperations(context.Context, string) ([]string, error) {
	p.listCalls++
	return p.ops, p.discoveryErr
}
func (p *statsProvider) FindTraces(ctx context.Context, q TraceQuery) (TraceBatch, error) {
	p.mu.Lock()
	p.queries = append(p.queries, q)
	p.mu.Unlock()
	return p.find(ctx, q)
}

func TestCollectStatsOperationBudgetsAndIsolation(t *testing.T) {
	a := span("a", "", "user", "server", "GET /a", 100, 20)
	b := span("b", "a", "user", "server", "PUT /b", 110, 10)
	unlisted := span("c", "a", "user", "server", "DELETE /c", 120, 5)
	shared := built(t, "shared", a, b, unlisted)
	p := &statsProvider{ops: []string{"PUT /b", "GET /a", "GET /a", "", "  "}}
	p.find = func(_ context.Context, q TraceQuery) (TraceBatch, error) {
		raw := 2
		if q.Operation == "GET /a" {
			raw = q.Limit
		}
		return TraceBatch{Traces: []*Trace{shared, shared}, RawCount: raw}, nil
	}
	q := TraceQuery{Service: "user", Start: 0, End: 1000}
	r, err := CollectStats(context.Background(), p, q, DefaultStatsOptions())
	if err != nil || r.Stats.TotalCalls != 2 || r.FetchedTraces != 1 || len(r.OperationQueries) != 2 || r.PerOperationLimit != 1500 {
		t.Fatalf("%+v %v", r, err)
	}
	if !r.OperationQueries[0].LimitReached || *r.OperationQueries[0].RawTraceCount != 1500 || r.OperationQueries[1].LimitReached {
		t.Fatalf("%+v", r.OperationQueries)
	}
	if !strings.Contains(r.OperationQueries[0].Message, "5000") {
		t.Fatal("missing focused query guidance")
	}
	for _, query := range p.queries {
		if query.Limit != 1500 || query.Service != "user" || query.Start != 0 || query.End != 1000 {
			t.Fatalf("%+v", query)
		}
	}
	q.Operation = "GET /a"
	r, err = CollectStats(context.Background(), p, q, DefaultStatsOptions())
	if err != nil || p.listCalls != 1 || r.PerOperationLimit != 5000 || r.Stats.TotalCalls != 1 || *r.OperationQueries[0].RawTraceCount != 5000 || !strings.Contains(r.OperationQueries[0].Message, "缩小") {
		t.Fatalf("focused: %+v %v", r, err)
	}
}

func TestCollectStatsPartialFailuresEmptyAndSkipped(t *testing.T) {
	p := &statsProvider{ops: []string{"a", "b", "c"}}
	p.find = func(_ context.Context, q TraceQuery) (TraceBatch, error) {
		if q.Operation == "b" {
			return TraceBatch{}, errors.New("unavailable")
		}
		return TraceBatch{}, nil
	}
	opts := DefaultStatsOptions()
	opts.MaxOperations = 2
	r, err := CollectStats(context.Background(), p, TraceQuery{Service: "user"}, opts)
	if err != nil || r.Stats.TotalCalls != 0 || r.OperationQueries[0].RawTraceCount == nil || *r.OperationQueries[0].RawTraceCount != 0 || r.OperationQueries[1].Status != OperationFailed || r.OperationQueries[1].RawTraceCount != nil || r.OperationQueries[2].Status != OperationSkipped {
		t.Fatalf("partial: %+v %v", r, err)
	}
	if !strings.Contains(strings.Join(r.Notices, " "), "不完整") {
		t.Fatal(r.Notices)
	}
	p.ops = []string{"b"}
	if r, err := CollectStats(context.Background(), p, TraceQuery{Service: "user"}, opts); err == nil || r == nil {
		t.Fatal("all failures treated as empty success")
	}
	p.ops = nil
	r, err = CollectStats(context.Background(), p, TraceQuery{Service: "user"}, opts)
	if err != nil || r.OperationQueries == nil || len(r.OperationQueries) != 0 || r.Stats.Entrypoints == nil {
		t.Fatalf("empty: %+v %v", r, err)
	}
	p.discoveryErr = errors.New("directory failed")
	if r, err := CollectStats(context.Background(), p, TraceQuery{Service: "user"}, opts); err == nil || r != nil {
		t.Fatal("directory failure swallowed")
	}
}

func TestCollectStatsBoundedConcurrencyAndCancellation(t *testing.T) {
	p := &statsProvider{ops: []string{"a", "b", "c", "d"}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	active, maximum := 0, 0
	started := make(chan struct{}, 3)
	p.find = func(ctx context.Context, _ TraceQuery) (TraceBatch, error) {
		mu.Lock()
		active++
		maximum = max(maximum, active)
		mu.Unlock()
		started <- struct{}{}
		<-ctx.Done()
		mu.Lock()
		active--
		mu.Unlock()
		return TraceBatch{}, ctx.Err()
	}
	done := make(chan *StatsCollection, 1)
	go func() { r, _ := CollectStats(ctx, p, TraceQuery{Service: "user"}, DefaultStatsOptions()); done <- r }()
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("workers not started")
		}
	}
	cancel()
	select {
	case r := <-done:
		if maximum != 3 || r.OperationQueries[3].Status != OperationSkipped || r.OperationQueries[3].RawTraceCount != nil {
			t.Fatalf("%+v max=%d", r, maximum)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation did not stop queries")
	}
	// 总预算到期也须取消正在执行的查询，并标记未启动的操作。
	opts := DefaultStatsOptions()
	opts.Concurrency = 1
	opts.Timeout = 10 * time.Millisecond
	p.find = func(ctx context.Context, _ TraceQuery) (TraceBatch, error) {
		<-ctx.Done()
		return TraceBatch{}, ctx.Err()
	}
	r, err := CollectStats(context.Background(), p, TraceQuery{Service: "user"}, opts)
	if err == nil || r.OperationQueries[0].Status != OperationFailed || r.OperationQueries[1].Status != OperationSkipped {
		t.Fatalf("timeout: %+v %v", r, err)
	}
}
