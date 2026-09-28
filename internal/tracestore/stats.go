package tracestore

import (
	"fmt"
	"math"
	"sort"
)

// ---- 聚合统计 ----

// StatsResult 描述匹配服务入口的调用统计，不是按 Trace 去重的请求统计。
type StatsResult struct {
	TotalCalls  int              `json:"total_calls"`
	ByStatus    map[string]int   `json:"by_status"`   // ok / degraded / failed
	Entrypoints []EntrypointStat `json:"entrypoints"` // 按调用量降序
}

// EntrypointStat 描述指定服务的某个 server 入口操作。
type EntrypointStat struct {
	Service                 string                   `json:"service"`
	Operation               string                   `json:"operation"`
	Count                   int                      `json:"count"`
	P50Ms                   float64                  `json:"p50_ms"`
	P95Ms                   float64                  `json:"p95_ms"`
	P99Ms                   float64                  `json:"p99_ms"`
	Failed                  int                      `json:"failed"`
	Degraded                int                      `json:"degraded"`
	DownstreamErrorServices []DownstreamErrorService `json:"downstream_error_services"`
}

type DownstreamErrorService struct {
	Service      string `json:"service"`
	RequestCount int    `json:"request_count"`
}

const smallSampleThreshold = 20

// Aggregate 将已筛选并去重的服务入口按 service/operation 分组，只分析入口及后代。
// 返回聚合结果和可附加到响应 notices 的提示列表。
func Aggregate(entries []ServiceEntry) (*StatsResult, []string) {
	byStatus := map[string]int{"ok": 0, "degraded": 0, "failed": 0}
	type entryKey struct{ service, operation string }
	grouped := map[entryKey][]*Span{}
	total := 0

	for _, entry := range entries {
		s := entry.Span
		if s == nil {
			continue
		}
		byStatus[classifyStatus(s)]++
		key := entryKey{s.Service, s.Operation}
		grouped[key] = append(grouped[key], s)
		total++
	}

	var notices []string
	eps := make([]EntrypointStat, 0, len(grouped))
	for op, ts := range grouped {
		durations := make([]float64, 0, len(ts))
		var failed, degraded int
		serviceCounts := map[string]int{}
		for _, entry := range ts {
			durations = append(durations, entry.DurationMs)
			// 按入口调用去重；同一次调用可观察到多个下游服务的错误。
			seen := map[string]bool{}
			var collect func(*Span)
			collect = func(s *Span) {
				if isErrorSpan(s) && s.Service != "" && s.Service != entry.Service {
					seen[s.Service] = true
				}
				for _, c := range s.Children {
					collect(c)
				}
			}
			for _, c := range entry.Children {
				collect(c)
			}
			for service := range seen {
				serviceCounts[service]++
			}
			switch classifyStatus(entry) {
			case "failed":
				failed++
			case "degraded":
				degraded++
			}
		}
		sort.Float64s(durations)
		if len(durations) < smallSampleThreshold {
			notices = append(notices,
				fmt.Sprintf("服务 %q 操作 %q 样本量较小（%d 条），百分位仅供参考", op.service, op.operation, len(durations)))
		}
		downstream := make([]DownstreamErrorService, 0, len(serviceCounts))
		for service, count := range serviceCounts {
			downstream = append(downstream, DownstreamErrorService{service, count})
		}
		sort.Slice(downstream, func(i, j int) bool {
			if downstream[i].RequestCount != downstream[j].RequestCount {
				return downstream[i].RequestCount > downstream[j].RequestCount
			}
			return downstream[i].Service < downstream[j].Service
		})
		eps = append(eps, EntrypointStat{
			Service:                 op.service,
			Operation:               op.operation,
			DownstreamErrorServices: downstream,
			Count:                   len(ts),
			P50Ms:                   percentile(durations, 0.50),
			P95Ms:                   percentile(durations, 0.95),
			P99Ms:                   percentile(durations, 0.99),
			Failed:                  failed,
			Degraded:                degraded,
		})
	}
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].Count != eps[j].Count {
			return eps[i].Count > eps[j].Count
		}
		if eps[i].Service != eps[j].Service {
			return eps[i].Service < eps[j].Service
		}
		return eps[i].Operation < eps[j].Operation
	})

	sort.Strings(notices)
	return &StatsResult{
		TotalCalls:  total,
		ByStatus:    byStatus,
		Entrypoints: eps,
	}, notices
}

// percentile 在已升序排列的切片上计算第 p 百分位数（p ∈ [0,1]），结果保留 3 位小数。
func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return math.Round(sorted[idx]*1000) / 1000
}
