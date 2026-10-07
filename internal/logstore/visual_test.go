package logstore

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type histogramFunc func(context.Context, HistogramQuery) (*Histogram, error)

func (f histogramFunc) QueryHistogram(ctx context.Context, q HistogramQuery) (*Histogram, error) {
	return f(ctx, q)
}
func histogramResult(q HistogramQuery) *Histogram {
	return &Histogram{Window: LogWindow{q.StartMs, q.EndMs}, Summary: LogCounts{Total: 2, ByLevel: map[string]int64{"INFO": 2}}, Buckets: []LogBucket{{LogWindow: LogWindow{q.StartMs, q.EndMs}, LogCounts: LogCounts{Total: 2, ByLevel: map[string]int64{"INFO": 2}}}}}
}
func awaitLog(t *testing.T, c *LogVisualCache, q HistogramQuery, predicate func(LogVisualResponse) bool) LogVisualResponse {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err := c.Snapshot(q)
		if err != nil {
			t.Fatal(err)
		}
		if predicate(r) {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("cache did not reach expected state")
	return LogVisualResponse{}
}
func TestLogVisualCoalescesAndCopiesSnapshots(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	p := histogramFunc(func(ctx context.Context, q HistogramQuery) (*Histogram, error) {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return histogramResult(q), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	c := NewLogVisualCache(p, DefaultLogVisualOptions())
	defer c.Close()
	q := HistogramQuery{Service: "svc"}
	r, _ := c.Snapshot(q)
	if !r.Loading || r.Initialized || r.Summary != nil || r.DataWindow != nil || r.Buckets == nil {
		t.Fatal(r)
	}
	<-entered
	for i := 0; i < 10; i++ {
		c.Snapshot(q)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	close(release)
	r = awaitLog(t, c, q, func(r LogVisualResponse) bool { return r.Initialized })
	r.Summary.ByLevel["INFO"] = 100
	r.Buckets[0].ByLevel["INFO"] = 100
	next, _ := c.Snapshot(q)
	if next.Summary.ByLevel["INFO"] != 2 || next.Buckets[0].ByLevel["INFO"] != 2 {
		t.Fatal("snapshot aliases cache")
	}
	if next.DataWindow.EndMs != next.DataAsOf || next.DataWindow.EndMs-next.DataWindow.StartMs != VisualWindowMs {
		t.Fatal(next)
	}
}
func TestLogVisualRefreshFailureKeepsDataThenRecovers(t *testing.T) {
	var fails atomic.Bool
	c := NewLogVisualCache(histogramFunc(func(_ context.Context, q HistogramQuery) (*Histogram, error) {
		if fails.Load() {
			return nil, errors.New("secret database error")
		}
		return histogramResult(q), nil
	}), DefaultLogVisualOptions())
	defer c.Close()
	q := HistogramQuery{Service: "svc", StartMs: 10000, EndMs: 20000}
	first := awaitLog(t, c, q, func(r LogVisualResponse) bool { return r.Initialized })
	fails.Store(true)
	c.mu.Lock()
	c.cells[q].updated = time.Now().Add(-2 * time.Minute)
	c.cells[q].attempt = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	failed := awaitLog(t, c, q, func(r LogVisualResponse) bool { return r.Error != "" })
	if !failed.Stale || !failed.Initialized || failed.Summary.Total != 2 || *failed.DataWindow != *first.DataWindow || failed.Error == "secret database error" {
		t.Fatal(failed)
	}
	fails.Store(false)
	c.mu.Lock()
	c.cells[q].attempt = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	recovered := awaitLog(t, c, q, func(r LogVisualResponse) bool { return !r.Loading && r.Error == "" })
	if recovered.Stale {
		t.Fatal("old historical date treated as stale")
	}
}
func TestLogVisualInitialFailureIsNotZero(t *testing.T) {
	c := NewLogVisualCache(histogramFunc(func(context.Context, HistogramQuery) (*Histogram, error) { return nil, errors.New("offline") }), DefaultLogVisualOptions())
	defer c.Close()
	r := awaitLog(t, c, HistogramQuery{Service: "svc"}, func(r LogVisualResponse) bool { return r.Error != "" })
	if r.Initialized || r.Summary != nil || r.DataWindow != nil || !r.Stale {
		t.Fatal(r)
	}
}
func TestLogVisualCapacityCancelAndIdleExpiry(t *testing.T) {
	opts := DefaultLogVisualOptions()
	opts.MaxCaches = 1
	entered := make(chan struct{})
	c := NewLogVisualCache(histogramFunc(func(ctx context.Context, q HistogramQuery) (*Histogram, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}), opts)
	c.Snapshot(HistogramQuery{Service: "one"})
	<-entered
	if _, err := c.Snapshot(HistogramQuery{Service: "two"}); err == nil {
		t.Fatal("loading cache evicted")
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel SQL")
	}

	c = NewLogVisualCache(histogramFunc(func(_ context.Context, q HistogramQuery) (*Histogram, error) { return histogramResult(q), nil }), opts)
	defer c.Close()
	q := HistogramQuery{Service: "one"}
	awaitLog(t, c, q, func(r LogVisualResponse) bool { return r.Initialized })
	c.mu.Lock()
	c.cells[q].access = time.Now().Add(-2 * opts.Idle)
	c.cells[q].attempt = time.Now().Add(-time.Minute)
	c.mu.Unlock()
	c.tick(time.Now())
	c.mu.Lock()
	loading := c.cells[q].loading
	c.mu.Unlock()
	if loading {
		t.Fatal("idle cache refreshed")
	}
	c.tick(time.Now().Add(opts.TTL))
	c.mu.Lock()
	n := len(c.cells)
	c.mu.Unlock()
	if n != 0 {
		t.Fatal("cache not expired")
	}
}
