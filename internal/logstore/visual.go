package logstore

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type HistogramProvider interface {
	QueryHistogram(context.Context, HistogramQuery) (*Histogram, error)
}
type LogVisualResponse struct {
	Service        string      `json:"service"`
	Method         string      `json:"method"`
	Route          string      `json:"route"`
	Window         LogWindow   `json:"window"`
	DataWindow     *LogWindow  `json:"data_window"`
	BucketMs       int64       `json:"bucket_ms"`
	Buckets        []LogBucket `json:"buckets"`
	Summary        *LogCounts  `json:"summary"`
	Loading        bool        `json:"loading"`
	Initialized    bool        `json:"initialized"`
	Stale          bool        `json:"stale"`
	UpdatedAt      int64       `json:"updated_at_ms"`
	DataAsOf       int64       `json:"data_as_of_ms"`
	RefreshSeconds int         `json:"refresh_seconds"`
	Error          string      `json:"error,omitempty"`
	Notices        []string    `json:"notices"`
}
type LogVisualOptions struct {
	Refresh, HistoryRefresh, Idle, TTL, Timeout, Tick time.Duration
	MaxCaches                                         int
}

func DefaultLogVisualOptions() LogVisualOptions {
	return LogVisualOptions{15 * time.Second, time.Minute, time.Minute, 10 * time.Minute, 5 * time.Second, time.Second, 8}
}

type logVisualCell struct {
	key                      HistogramQuery
	access, attempt, updated time.Time
	loading                  bool
	result                   *Histogram
	err                      string
}

// LogVisualCache owns an independent single worker; Jaeger cannot block log refreshes.
type LogVisualCache struct {
	mu       sync.Mutex
	provider HistogramProvider
	opts     LogVisualOptions
	cells    map[HistogramQuery]*logVisualCell
	jobs     chan *logVisualCell
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewLogVisualCache(p HistogramProvider, opts LogVisualOptions) *LogVisualCache {
	ctx, cancel := context.WithCancel(context.Background())
	c := &LogVisualCache{provider: p, opts: opts, cells: map[HistogramQuery]*logVisualCell{}, jobs: make(chan *logVisualCell, opts.MaxCaches), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go c.run()
	return c
}
func (c *LogVisualCache) Close() { c.cancel(); <-c.done }
func (c *LogVisualCache) enqueue(cell *logVisualCell, now time.Time) {
	if cell.loading || c.ctx.Err() != nil || (!cell.attempt.IsZero() && now.Sub(cell.attempt) < c.opts.Refresh) {
		return
	}
	// 历史窗口1min刷一次
	if cell.key.EndMs != 0 && cell.result != nil && cell.err == "" && now.Sub(cell.updated) < c.opts.HistoryRefresh {
		return
	}
	select {
	case c.jobs <- cell:
		cell.loading = true
		cell.attempt = now
	default:
	}
}
func (c *LogVisualCache) tick(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, cell := range c.cells {
		if !cell.loading && now.Sub(cell.access) > c.opts.TTL {
			delete(c.cells, key)
			continue
		}
		if key.EndMs == 0 && now.Sub(cell.access) <= c.opts.Idle {
			c.enqueue(cell, now)
		}
	}
}
func (c *LogVisualCache) run() {
	defer close(c.done)
	ticker := time.NewTicker(c.opts.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case now := <-ticker.C:
			c.tick(now)
		case cell := <-c.jobs:
			if c.ctx.Err() != nil {
				return
			}
			q := cell.key
			// 滚动窗口实时刷新
			if q.EndMs == 0 {
				q.EndMs = time.Now().UnixMilli()
				q.StartMs = q.EndMs - VisualWindowMs
			}
			ctx, cancel := context.WithTimeout(c.ctx, c.opts.Timeout)
			result, err := c.provider.QueryHistogram(ctx, q)
			cancel()
			c.mu.Lock()
			cell.loading = false
			if err != nil || result == nil {
				cell.err = "日志聚合查询失败，请检查观测数据库后重试"
			} else {
				cell.result = result
				cell.updated = time.Now()
				cell.err = ""
			}
			c.mu.Unlock()
		}
	}
}

// 深拷贝函数
func copyCounts(v LogCounts) LogCounts {
	r := LogCounts{Total: v.Total, ByLevel: make(map[string]int64, len(v.ByLevel))}
	for k, n := range v.ByLevel {
		r.ByLevel[k] = n
	}
	return r
}
func (c *LogVisualCache) Snapshot(q HistogramQuery) (LogVisualResponse, error) {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	cell := c.cells[q]
	if cell == nil {
		if len(c.cells) >= c.opts.MaxCaches {
			// 找出没有被加载并且最久没有访问的缓存，删除它
			var oldest *logVisualCell
			for _, v := range c.cells {
				if !v.loading && (oldest == nil || v.access.Before(oldest.access)) {
					oldest = v
				}
			}
			if oldest == nil {
				return LogVisualResponse{}, fmt.Errorf("日志图表查询繁忙，请稍后重试")
			}
			delete(c.cells, oldest.key)
		}
		cell = &logVisualCell{key: q}
		c.cells[q] = cell
	}
	cell.access = now
	c.enqueue(cell, now)
	window := LogWindow{q.StartMs, q.EndMs}
	if q.EndMs == 0 {
		window.EndMs = now.UnixMilli()
		window.StartMs = window.EndMs - VisualWindowMs
	}
	r := LogVisualResponse{Service: q.Service, Method: q.Method, Route: q.Route, Window: window, BucketMs: VisualBucketMs, Buckets: []LogBucket{}, Loading: cell.loading, Initialized: cell.result != nil, Error: cell.err, Notices: []string{}, RefreshSeconds: int(c.opts.Refresh / time.Second)}
	r.Stale = cell.result == nil || cell.err != ""
	if cell.result != nil {
		w := cell.result.Window
		r.DataWindow = &w
		r.DataAsOf = w.EndMs
		r.UpdatedAt = cell.updated.UnixMilli()
		summary := copyCounts(cell.result.Summary)
		r.Summary = &summary
		for _, bucket := range cell.result.Buckets {
			r.Buckets = append(r.Buckets, LogBucket{LogWindow: bucket.LogWindow, LogCounts: copyCounts(bucket.LogCounts)})
		}
		if q.EndMs == 0 {
			r.Stale = r.Stale || now.UnixMilli()-w.EndMs > int64(2*c.opts.Refresh/time.Millisecond)
		} else {
			r.Stale = r.Stale || now.Sub(cell.updated) > 2*c.opts.HistoryRefresh
		}
		if w != window {
			r.Notices = append(r.Notices, "缓存统计对应已查询窗口；尚未覆盖的时间段不能视为零日志")
		}
		if w.StartMs%VisualBucketMs != 0 || w.EndMs%VisualBucketMs != 0 {
			r.Notices = append(r.Notices, "首尾时间桶可能不足 10 秒，计数只包含实际查询范围")
		}
	}
	return r, nil
}
