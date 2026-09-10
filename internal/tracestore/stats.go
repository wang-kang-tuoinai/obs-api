package tracestore

import (
	"fmt"
	"math"
	"sort"
)

// ---- 聚合统计 ----

// StatsResult 是 Aggregate 的输出，描述一批 Trace 的整体健康状况。
type StatsResult struct {
	TotalTraces int              `json:"total_traces"`
	ByStatus    map[string]int   `json:"by_status"`   // ok / degraded / failed
	Entrypoints []EntrypointStat `json:"entrypoints"` // 按调用量降序
}

// EntrypointStat 描述某个入口操作（root operation）的统计数据。
type EntrypointStat struct {
	Operation string  `json:"operation"`
	Count     int     `json:"count"`
	P50Ms     float64 `json:"p50_ms"`
	P95Ms     float64 `json:"p95_ms"`
	P99Ms     float64 `json:"p99_ms"`
	Failed    int     `json:"failed"`
	Degraded  int     `json:"degraded"`
}

const smallSampleThreshold = 20

// Aggregate 将一批 Trace 按 RootOperation 分组并计算耗时百分位与状态分布。
// 返回聚合结果和可附加到响应 notices 的提示列表。
func Aggregate(traces []*Trace) (*StatsResult, []string) {
	byStatus := map[string]int{"ok": 0, "degraded": 0, "failed": 0}
	grouped := map[string][]*Trace{}

	for _, t := range traces {
		byStatus[t.Status]++
		grouped[t.RootOperation] = append(grouped[t.RootOperation], t)
	}

	var notices []string
	eps := make([]EntrypointStat, 0, len(grouped))
	for op, ts := range grouped {
		durations := make([]float64, 0, len(ts))
		var failed, degraded int
		for _, t := range ts {
			durations = append(durations, t.DurationMs)
			switch t.Status {
			case "failed":
				failed++
			case "degraded":
				degraded++
			}
		}
		sort.Float64s(durations)
		if len(durations) < smallSampleThreshold {
			notices = append(notices,
				fmt.Sprintf("操作 %q 样本量较小（%d 条），百分位仅供参考", op, len(durations)))
		}
		eps = append(eps, EntrypointStat{
			Operation: op,
			Count:     len(ts),
			P50Ms:     percentile(durations, 0.50),
			P95Ms:     percentile(durations, 0.95),
			P99Ms:     percentile(durations, 0.99),
			Failed:    failed,
			Degraded:  degraded,
		})
	}
	sort.Slice(eps, func(i, j int) bool { return eps[i].Count > eps[j].Count })

	return &StatsResult{
		TotalTraces: len(traces),
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
