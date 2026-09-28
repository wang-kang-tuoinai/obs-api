package tracestore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type StatsOptions struct {
	PerOperationLimit int
	FocusedLimit      int
	Concurrency       int
	MaxOperations     int
	Timeout           time.Duration
}

func DefaultStatsOptions() StatsOptions {
	return StatsOptions{PerOperationLimit: 1500, FocusedLimit: 5000, Concurrency: 3, MaxOperations: 30, Timeout: 25 * time.Second}
}

type OperationQueryStatus string

const (
	OperationSuccess OperationQueryStatus = "success"
	OperationFailed  OperationQueryStatus = "failed"
	OperationSkipped OperationQueryStatus = "skipped"
)

type OperationQueryMeta struct {
	Operation     string               `json:"operation"`
	Status        OperationQueryStatus `json:"status"`
	RawTraceCount *int                 `json:"raw_trace_count"`
	LimitReached  bool                 `json:"limit_reached"`
	Message       string               `json:"message,omitempty"`
}

type StatsCollection struct {
	Stats             *StatsResult
	PerOperationLimit int
	FetchedTraces     int
	OperationQueries  []OperationQueryMeta
	Notices           []string
}

// CollectStats 按 operation 隔离候选预算；明确 operation 时不依赖目录查询。
// 全部失败返回结果及 error，调用方仍可展示每个 operation 的失败原因。
func CollectStats(ctx context.Context, provider TraceProvider, q TraceQuery, options StatsOptions) (*StatsCollection, error) {
	if options.PerOperationLimit < 1 || options.FocusedLimit <= options.PerOperationLimit || options.FocusedLimit > 5000 || options.Concurrency < 1 || options.MaxOperations < 1 || options.Timeout <= 0 {
		return nil, fmt.Errorf("stats 查询配置无效")
	}
	ctx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	limit := options.PerOperationLimit
	operations := []string{q.Operation}
	if q.Operation == "" {
		var err error
		operations, err = provider.GetOperations(ctx, q.Service)
		if err != nil {
			return nil, fmt.Errorf("获取服务入口列表失败: %w", err)
		}
		unique := make(map[string]bool)
		clean := make([]string, 0, len(operations))
		for _, op := range operations {
			if strings.TrimSpace(op) != "" && !unique[op] {
				unique[op] = true
				clean = append(clean, op)
			}
		}
		operations = clean
		sort.Strings(operations)
	} else {
		limit = options.FocusedLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := &StatsCollection{PerOperationLimit: limit, OperationQueries: make([]OperationQueryMeta, len(operations)), Notices: make([]string, 0)}
	for i, op := range operations {
		result.OperationQueries[i] = OperationQueryMeta{Operation: op, Status: OperationSkipped, Message: "超过本次 operation 查询数量上限，请指定该 operation 单独查询"}
	}
	count := min(len(operations), options.MaxOperations)
	type outcome struct {
		batch     TraceBatch
		err       error
		attempted bool
	}
	outcomes := make([]outcome, count)
	jobs := make(chan int, count)
	for i := 0; i < count; i++ {
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for worker := 0; worker < min(options.Concurrency, count); worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				if ctx.Err() != nil {
					continue
				}
				query := q
				query.Operation, query.Limit = operations[i], limit
				batch, err := provider.FindTraces(ctx, query)
				outcomes[i] = outcome{batch: batch, err: err, attempted: true}
			}
		}()
	}
	workers.Wait()
	entries := make([]ServiceEntry, 0)
	seenEntry := map[[2]string]bool{}
	traceIDs := map[string]bool{}
	seenNotice := map[string]bool{}
	addNotice := func(message string) {
		if !seenNotice[message] {
			seenNotice[message] = true
			result.Notices = append(result.Notices, message)
		}
	}
	successes := 0
	for i, op := range operations {
		meta := &result.OperationQueries[i]
		if i >= count {
			addNotice(fmt.Sprintf("操作 %q 未查询：%s", op, meta.Message))
			continue
		}
		out := outcomes[i]
		if !out.attempted {
			meta.Message = "查询预算已耗尽或请求已取消，未执行；请缩小窗口或指定该 operation 重试"
			addNotice(fmt.Sprintf("操作 %q 未查询：%s", op, meta.Message))
			continue
		}
		if out.err != nil {
			meta.Status, meta.Message = OperationFailed, out.err.Error()
			addNotice(fmt.Sprintf("操作 %q 查询失败：%s；未计入统计，不代表零调用", op, meta.Message))
			continue
		}
		successes++
		meta.Status, meta.Message = OperationSuccess, ""
		rawCount := out.batch.RawCount
		meta.RawTraceCount = &rawCount
		meta.LimitReached = rawCount >= limit
		if meta.LimitReached {
			if q.Operation == "" {
				meta.Message = fmt.Sprintf("候选达到上限 %d，可能截断；请指定该 operation 重查（上限 %d），仍触顶时缩小窗口", limit, options.FocusedLimit)
			} else {
				meta.Message = fmt.Sprintf("候选达到上限 %d，可能截断；请缩小时间窗口继续查询", limit)
			}
			addNotice(fmt.Sprintf("操作 %q：%s；统计与百分位仅针对已获取样本", op, meta.Message))
		}
		for _, notice := range out.batch.Notices {
			addNotice(fmt.Sprintf("操作 %q：%s", op, notice))
		}
		for _, trace := range out.batch.Traces {
			if trace != nil {
				traceIDs[trace.TraceID] = true
			}
		}
		selected, warnings := SelectServiceEntries(out.batch.Traces, SearchOptions{Service: q.Service, Operation: op, StartMs: q.Start, EndMs: q.End})
		for _, warning := range warnings {
			addNotice(warning)
		}
		for _, entry := range selected {
			key := [2]string{entry.Trace.TraceID, entry.Span.SpanID}
			if !seenEntry[key] {
				seenEntry[key] = true
				entries = append(entries, entry)
			}
		}
	}
	result.FetchedTraces = len(traceIDs)
	var aggregateNotices []string
	result.Stats, aggregateNotices = Aggregate(entries)
	for _, notice := range aggregateNotices {
		addNotice(notice)
	}
	if len(operations) == 0 {
		addNotice("Jaeger 未返回该服务的 server operation；目录不按诊断窗口过滤，空目录不代表系统没有异常")
	} else if successes == 0 {
		addNotice("所有 operation 均查询失败或未执行，无法形成有效统计")
		return result, fmt.Errorf("所有 operation 均查询失败或未执行")
	}
	if successes < len(operations) {
		addNotice("部分 operation 未成功查询，整体统计不完整；请检查 operation_queries 并重试")
	}
	return result, nil
}
