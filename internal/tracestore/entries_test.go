package tracestore

import "testing"

func TestServiceEntryStatsForestAndFilters(t *testing.T) {
	first := span("a", "", "user", "server", "GET /users", 100, 50)
	nested := span("b", "a", "user", "server", "GET /users", 120, 20)
	otherRoot := span("c", "", "user", "server", "POST /users", 200, 10)
	orphan := span("d", "missing", "user", "server", "GET /users", 300, 30)
	remote := span("e", "b", "profile", "server", "GET /profiles", 125, 10)
	remote.Status = "error"
	unknown := span("f", "d", "", "client", "unknown", 301, 5)
	unknown.Error = "timeout"
	// 不与入口连接的错误片段，以及同名 client/internal，不能冒充入口。
	detached := span("g", "lost", "billing", "server", "GET /bill", 150, 10)
	detached.Status = "error"
	client := span("h", "", "user", "client", "GET /users", 150, 10)
	internal := span("i", "", "user", "internal", "GET /users", 150, 10)
	tr := built(t, "forest", first, nested, otherRoot, orphan, remote, unknown, detached, client, internal)
	q := query("user")
	q.Limit = 1 // stats 基础筛选不能被 search 的返回上限截断。
	entries, warnings := SelectServiceEntries([]*Trace{nil, tr, tr}, q)
	stats, _ := Aggregate(entries)
	search := Search([]*Trace{tr}, q)
	if stats.TotalCalls != 4 || search.MatchedCount != 4 || len(search.Items) != 1 || stats.ByStatus["degraded"] != 3 || stats.ByStatus["ok"] != 1 {
		t.Fatalf("stats=%+v search=%+v", stats, search)
	}
	if len(warnings) != len(tr.Warnings) || tr.Root != nil {
		t.Fatalf("warnings repeated or global root changed: %v", warnings)
	}
	get := stats.Entrypoints[0]
	if get.Operation != "GET /users" || get.Count != 3 || get.P50Ms != 30 || len(get.DownstreamErrorServices) != 1 || get.DownstreamErrorServices[0].Service != "profile" || get.DownstreamErrorServices[0].RequestCount != 2 {
		t.Fatalf("nested calls/unknown service/disconnected error: %+v", get)
	}
	for _, tc := range []struct {
		op         string
		start, end int64
		want       int
	}{
		{"GET /users", 100, 300, 3},
		{"GET /users", 120, 120, 1}, // 两端包含，按入口开始时间。
		{"GET /users", 121, 299, 0},
		{"POST /users", 0, 1000, 1},
		{"missing", 0, 1000, 0},
	} {
		q.Operation, q.StartMs, q.EndMs = tc.op, tc.start, tc.end
		entries, _ := SelectServiceEntries([]*Trace{tr}, q)
		result, _ := Aggregate(entries)
		found := Search([]*Trace{tr}, q)
		if result.TotalCalls != tc.want || found.MatchedCount != tc.want {
			t.Fatalf("%+v: stats=%+v search=%+v", tc, result, found)
		}
		if tc.want == 0 && (result.Entrypoints == nil || len(result.Entrypoints) != 0 || result.ByStatus["ok"] != 0 || result.ByStatus["degraded"] != 0 || result.ByStatus["failed"] != 0) {
			t.Fatalf("invalid empty stats: %+v", result)
		}
	}
}
