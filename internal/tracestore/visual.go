package tracestore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// VisualPoint owns only scalar summary fields, never references a Trace or Span tree.
type VisualPoint struct {
	TraceID     string  `json:"trace_id"`
	EntrySpanID string  `json:"entry_span_id"`
	Service     string  `json:"service"`
	Operation   string  `json:"operation"`
	StartMs     int64   `json:"start_ms"`
	DurationMs  float64 `json:"duration_ms"`
	Status      string  `json:"status"`
	Incomplete  bool    `json:"incomplete"`
}
type VisualWindow struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
}
type VisualResponse struct {
	Service        string               `json:"service"`
	Operation      string               `json:"operation"`
	Window         VisualWindow         `json:"window"`
	Points         []VisualPoint        `json:"points"`
	Operations     []string             `json:"operations"`
	ByStatus       map[string]int       `json:"by_status"`
	Loading        bool                 `json:"loading"`
	Initialized    bool                 `json:"initialized"`
	Stale          bool                 `json:"stale"`
	Partial        bool                 `json:"partial"`
	UpdatedAt      int64                `json:"updated_at_ms"`
	DataAsOf       int64                `json:"data_as_of_ms"`
	Error          string               `json:"error,omitempty"`
	Notices        []string             `json:"notices"`
	Queries        []OperationQueryMeta `json:"operation_queries"`
	RefreshSeconds int                  `json:"refresh_seconds"`
	PointLimit     int                  `json:"point_limit"`
}
type visualKey struct {
	service    string
	start, end int64
}
type pointKey struct{ trace, span string }
type visualCell struct {
	key                                  visualKey // 包含service+start+end
	points                               map[pointKey]VisualPoint
	ops                                  []string
	queries                              []OperationQueryMeta
	notices                              []string
	access, attempt, updated, full, asOf time.Time
	loading, initialized, partial        bool
	err                                  string
}
type VisualOptions struct {
	Window    time.Duration // rolling window length: how far back the rolling window looks
	Refresh   time.Duration // minimum interval between refreshes of a cell
	Lookback  time.Duration // incremental refresh lookback: only re-query this recent span when not doing a full refresh
	Reconcile time.Duration // full reconciliation interval: re-query the whole window when exceeded
	Idle      time.Duration // how recently a rolling window must be accessed to keep auto-refreshing
	TTL       time.Duration // cache cell lifetime; evicted when not accessed for this long
	Timeout   time.Duration // per-refresh timeout covering all operation queries

	MaxCaches      int // max cached window cells (service + window combinations)
	MaxPoints      int // max entry sample points retained per cell
	MaxOperations  int // max operations queried per refresh cycle
	CandidateLimit int // per-operation trace candidate limit
}

func DefaultVisualOptions() VisualOptions {
	return VisualOptions{Window: 15 * time.Minute, Refresh: 15 * time.Second, Lookback: 2 * time.Minute,
		Reconcile: 5 * time.Minute, Idle: time.Minute, TTL: 10 * time.Minute, Timeout: 90 * time.Second,
		MaxCaches: 8, MaxPoints: 20000, MaxOperations: 30, CandidateLimit: 1500}
}

// One worker serializes operation queries across all visual caches. Agent tools are independent.
type VisualCache struct {
	mu       sync.Mutex
	provider TraceProvider
	opts     VisualOptions
	cells    map[visualKey]*visualCell
	jobs     chan *visualCell
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewVisualCache(provider TraceProvider, opts VisualOptions) *VisualCache {
	ctx, cancel := context.WithCancel(context.Background())
	m := &VisualCache{provider: provider, opts: opts, cells: map[visualKey]*visualCell{}, jobs: make(chan *visualCell, opts.MaxCaches), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	go m.run()
	return m
}
func (m *VisualCache) Close() { m.cancel(); <-m.done }
func (m *VisualCache) enqueue(c *visualCell, now time.Time) {
	if c.loading || (!c.attempt.IsZero() && now.Sub(c.attempt) < m.opts.Refresh) {
		return
	}
	select {
	case m.jobs <- c:
		c.loading = true
		c.attempt = now
	default:
	}
}
func (m *VisualCache) run() {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case c := <-m.jobs:
			m.refresh(c, time.Now())
		case now := <-ticker.C:
			m.mu.Lock()
			for key, c := range m.cells {
				if !c.loading && now.Sub(c.access) > m.opts.TTL {
					delete(m.cells, key)
					continue
				}
				if key.end == 0 && now.Sub(c.access) <= m.opts.Idle {
					m.enqueue(c, now)
				}
			}
			m.mu.Unlock()
		}
	}
}

// start/end == 0 selects the rolling window; fixed historical windows share their own bounded cache.
func (m *VisualCache) Snapshot(service, operation string, start, end int64) (VisualResponse, error) {
	now := time.Now()
	key := visualKey{service, start, end}
	m.mu.Lock()
	defer m.mu.Unlock()
	// 取出这个key对应的缓存
	c := m.cells[key]
	// 不存在
	if c == nil {
		// 缓存达到最大数量
		if len(m.cells) >= m.opts.MaxCaches {
			// 找到最久没有被访问且没有被加载的cache并删除
			var oldest *visualCell
			for _, v := range m.cells {
				if !v.loading && (oldest == nil || v.access.Before(oldest.access)) {
					oldest = v
				}
			}
			if oldest == nil {
				return VisualResponse{}, fmt.Errorf("图表查询繁忙，请稍后重试")
			}
			delete(m.cells, oldest.key)
		}
		c = &visualCell{key: key, points: map[pointKey]VisualPoint{}}
		m.cells[key] = c
	}
	c.access = now
	if !c.initialized || key.end == 0 || c.err != "" || now.Sub(c.updated) >= time.Minute {
		m.enqueue(c, now)
	}
	if end == 0 {
		end = now.UnixMilli()
		start = now.Add(-m.opts.Window).UnixMilli()
	}
	r := VisualResponse{Service: service, Operation: operation, Window: VisualWindow{start, end}, Points: []VisualPoint{}, Operations: append([]string{}, c.ops...),
		ByStatus: map[string]int{"ok": 0, "degraded": 0, "failed": 0}, Loading: c.loading, Initialized: c.initialized, Partial: c.partial,
		Stale: !c.initialized || c.err != "" || (key.end == 0 && now.Sub(c.asOf) > 2*m.opts.Refresh),
		Error: c.err, Notices: append([]string{}, c.notices...), Queries: append([]OperationQueryMeta{}, c.queries...), RefreshSeconds: int(m.opts.Refresh.Seconds()), PointLimit: m.opts.MaxPoints}
	if !c.updated.IsZero() {
		r.UpdatedAt = c.updated.UnixMilli()
	}
	if !c.asOf.IsZero() {
		r.DataAsOf = c.asOf.UnixMilli()
	}
	for k, p := range c.points {
		if p.StartMs < start {
			delete(c.points, k)
			continue
		}
		if p.StartMs <= end && (operation == "" || p.Operation == operation) {
			r.Points = append(r.Points, p)
			r.ByStatus[p.Status]++
		}
	}
	sort.Slice(r.Points, func(i, j int) bool { return pointLess(r.Points[i], r.Points[j]) })
	return r, nil
}
func pointLess(a, b VisualPoint) bool {
	if a.StartMs != b.StartMs {
		return a.StartMs < b.StartMs
	}
	if a.TraceID != b.TraceID {
		return a.TraceID < b.TraceID
	}
	return a.EntrySpanID < b.EntrySpanID
}
func capPoints(points map[pointKey]VisualPoint, limit int) bool {
	if len(points) <= limit {
		return false
	}
	items := make([]VisualPoint, 0, len(points))
	for _, p := range points {
		items = append(items, p)
	}
	sort.Slice(items, func(i, j int) bool { return pointLess(items[i], items[j]) })
	for _, p := range items[:len(items)-limit] {
		delete(points, pointKey{p.TraceID, p.EntrySpanID})
	}
	return true
}
func uniqueNotices(items []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range items {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
			if len(out) == 60 {
				out = append(out, "其余重复或过多提示已省略，请缩小范围核查")
				break
			}
		}
	}
	return out
}
func (m *VisualCache) refresh(c *visualCell, now time.Time) {
	ctx, cancel := context.WithTimeout(m.ctx, m.opts.Timeout)
	defer cancel()
	m.mu.Lock()
	end := c.key.end
	start := c.key.start
	full := !c.initialized || c.key.end != 0 || now.Sub(c.full) >= m.opts.Reconcile || now.Sub(c.asOf) >= m.opts.Lookback
	knownOps := map[string]bool{}
	for _, op := range c.ops {
		knownOps[op] = true
	}
	if end == 0 {
		end = now.UnixMilli()
		start = now.Add(-m.opts.Window).UnixMilli()
		if !full {
			start = now.Add(-m.opts.Lookback).UnixMilli()
		}
	}
	m.mu.Unlock()
	ops, err := m.provider.GetOperations(ctx, c.key.service)
	if err != nil {
		m.mu.Lock()
		c.loading = false
		c.err = "服务入口目录查询失败：" + err.Error()
		c.partial = true
		c.notices = uniqueNotices(append(c.notices, c.err))
		m.mu.Unlock()
		return
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, op := range ops {
		if strings.TrimSpace(op) != "" && !seen[op] {
			seen[op] = true
			clean = append(clean, op)
		}
	}
	sort.Strings(clean)
	updates := map[pointKey]VisualPoint{}
	queries := []OperationQueryMeta{}
	notices := []string{}
	partial := false
	successes := 0
	if len(clean) == 0 {
		partial = true
		notices = append(notices, "Jaeger 未返回 server 入口目录；不能据此断言服务正常")
	}
	for i, op := range clean {
		meta := OperationQueryMeta{Operation: op, Status: OperationSkipped}
		if i >= m.opts.MaxOperations || ctx.Err() != nil {
			partial = true
			meta.Message = "本轮操作数量或时间预算已耗尽"
			queries = append(queries, meta)
			notices = append(notices, "部分 operation 未查询，面板不完整")
			continue
		}
		// The batch and its trees stay within this helper; only scalar summaries escape.
		queryStart := start
		// 新冒出来的operation用全量窗口补全历史
		if c.key.end == 0 && !knownOps[op] {
			queryStart = now.Add(-m.opts.Window).UnixMilli()
		}
		points, batchNotices, raw, queryErr := m.queryPoints(ctx, TraceQuery{Service: c.key.service, Operation: op, Start: queryStart, End: end, Limit: m.opts.CandidateLimit})
		if queryErr != nil {
			partial = true
			meta.Status = OperationFailed
			meta.Message = queryErr.Error()
			notices = append(notices, fmt.Sprintf("%s 查询失败，保留已有摘要", op))
		} else {
			successes++
			meta.Status = OperationSuccess
			meta.RawTraceCount = &raw
			meta.LimitReached = raw >= m.opts.CandidateLimit
			if meta.LimitReached {
				partial = true
				notices = append(notices, fmt.Sprintf("%s 候选达到 %d，可能截断；请缩小历史窗口，接口筛选本身不会补齐数据", op, m.opts.CandidateLimit))
			}
			if len(batchNotices) > 0 {
				partial = true
				notices = append(notices, batchNotices...)
			}
			for _, p := range points {
				updates[pointKey{p.TraceID, p.EntrySpanID}] = p
			}
			if capPoints(updates, m.opts.MaxPoints) {
				partial = true
				notices = append(notices, "达到面板点数上限，仅保留较新的入口样本")
			}
		}
		queries = append(queries, meta)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c.loading = false
	c.ops = clean
	c.queries = queries
	c.err = ""
	if len(clean) > 0 && successes == 0 {
		c.err = "本轮所有入口查询失败或未执行，保留上次数据"
		partial = true
	}
	cutoff := c.key.start
	if c.key.end == 0 {
		cutoff = time.Now().Add(-m.opts.Window).UnixMilli()
	}
	for k, p := range c.points {
		if p.StartMs < cutoff {
			delete(c.points, k)
		}
	}
	for k, p := range updates {
		if p.StartMs >= cutoff {
			c.points[k] = p
		}
	}
	if capPoints(c.points, m.opts.MaxPoints) {
		partial = true
		notices = append(notices, "达到面板点数上限，仅保留较新的入口样本")
	}
	if full && !partial {
		c.partial = false
		c.notices = nil
	}
	c.partial = c.partial || partial
	c.notices = uniqueNotices(append(c.notices, notices...))
	if full {
		c.full = now
	}
	if successes > 0 || len(clean) == 0 {
		c.initialized = true
		c.updated = time.Now()
		c.asOf = time.UnixMilli(end)
	}
}
func (m *VisualCache) queryPoints(ctx context.Context, q TraceQuery) ([]VisualPoint, []string, int, error) {
	batch, err := m.provider.FindTraces(ctx, q)
	if err != nil {
		return nil, nil, 0, err
	}
	entries, warnings := SelectServiceEntries(batch.Traces, SearchOptions{Service: q.Service, Operation: q.Operation, StartMs: q.Start, EndMs: q.End})
	points := make([]VisualPoint, 0, len(entries))
	for _, entry := range entries {
		s, t := entry.Span, entry.Trace
		points = append(points, VisualPoint{TraceID: t.TraceID, EntrySpanID: s.SpanID, Service: s.Service, Operation: s.Operation, StartMs: s.StartMs, DurationMs: s.DurationMs, Status: classifyStatus(s), Incomplete: t.Incomplete})
	}
	return points, append(batch.Notices, warnings...), batch.RawCount, nil
}
