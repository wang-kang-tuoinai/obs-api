package logstore

import (
	"context"
	"fmt"
	"strings"
)

const VisualBucketMs int64 = 10000
const VisualWindowMs int64 = 900000

type LogWindow struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
}
type HistogramQuery struct {
	Service, Method, Route string
	StartMs, EndMs         int64
}
type LogCounts struct {
	Total   int64            `json:"total"`
	ByLevel map[string]int64 `json:"by_level"`
}
type LogBucket struct {
	LogWindow
	LogCounts
}
type Histogram struct {
	Window  LogWindow
	Buckets []LogBucket
	Summary LogCounts
}

func zeroCounts() LogCounts {
	return LogCounts{ByLevel: map[string]int64{"DEBUG": 0, "INFO": 0, "WARN": 0, "ERROR": 0}}
}

// QueryHistogram only transfers grouped counts. Boundary buckets are clipped to [start,end).
func (s *MysqlStore) QueryHistogram(ctx context.Context, q HistogramQuery) (*Histogram, error) {
	if q.Service == "" || q.StartMs <= 0 || q.EndMs <= q.StartMs || q.EndMs-q.StartMs > VisualWindowMs || (q.Method == "") != (q.Route == "") {
		return nil, fmt.Errorf("invalid histogram query")
	}
	clauses := []string{"service = ?", "ts >= ?", "ts < ?"}
	args := []any{VisualBucketMs, VisualBucketMs, q.Service, q.StartMs, q.EndMs}
	if q.Method != "" {
		clauses = append(clauses, "method = ?", "route = ?")
		args = append(args, q.Method, q.Route)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT (ts DIV ?) * ? AS bucket_start_ms, level, COUNT(*) FROM logs WHERE "+strings.Join(clauses, " AND ")+" GROUP BY bucket_start_ms, level ORDER BY bucket_start_ms, level", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := &Histogram{Window: LogWindow{q.StartMs, q.EndMs}, Summary: zeroCounts(), Buckets: []LogBucket{}}
	first := q.StartMs / VisualBucketMs * VisualBucketMs
	for start := first; start < q.EndMs; start += VisualBucketMs {
		// 把第一个bucket的Windows截取到start，把最后一个bucket的Windows截取到end，其他bucket的Windows都是整齐的10秒
		result.Buckets = append(result.Buckets, LogBucket{LogWindow: LogWindow{max(start, q.StartMs), min(start+VisualBucketMs, q.EndMs)}, LogCounts: zeroCounts()})
	}
	for rows.Next() {
		var start, count int64
		var level string
		if err := rows.Scan(&start, &level, &count); err != nil {
			return nil, err
		}
		index := (start - first) / VisualBucketMs
		if start < first || start%VisualBucketMs != 0 || index >= int64(len(result.Buckets)) || count < 0 {
			return nil, fmt.Errorf("invalid histogram row")
		}
		bucket := &result.Buckets[index]
		bucket.Total += count
		bucket.ByLevel[level] += count
		result.Summary.Total += count
		result.Summary.ByLevel[level] += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
