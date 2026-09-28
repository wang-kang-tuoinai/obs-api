package tracestore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func span(id, parent, service, kind, op string, start int64, duration float64) *Span {
	return &Span{SpanID: id, ParentSpanID: parent, Service: service, Kind: kind, Operation: op,
		StartMs: start, DurationMs: duration, Status: "ok", Attrs: map[string]any{}}
}

func built(t *testing.T, id string, spans ...*Span) *Trace {
	t.Helper()
	tr, err := BuildTrace(id, spans)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func query(service string) SearchOptions {
	return SearchOptions{Service: service, StartMs: 0, EndMs: 10000, Limit: 10, Sort: "duration_desc"}
}

func TestMultiServiceStatsAndSearch(t *testing.T) {
	r := span("root", "", "gateway", "server", "GET /same", 1000, 1000)
	u1 := span("u1", "root", "user", "server", "GET /same", 1100, 200)
	u2 := span("u2", "root", "user", "server", "GET /same", 1400, 300)
	p := span("p", "client", "profile", "server", "GET /profile", 1110, 50)
	db := span("db", "p", "profile", "client", "SELECT users", 1120, 10)
	client := span("client", "u1", "user", "client", "HTTP profile", 1105, 60)
	other := span("other", "root", "order", "server", "GET /order", 1800, 30)
	for _, s := range []*Span{p, db, client, other} {
		s.Status, s.Error = "error", "timeout"
	}
	tr := built(t, "t1", other, db, u2, client, p, r, u1)
	selected, _ := SelectServiceEntries([]*Trace{tr}, query("gateway"))
	stats, _ := Aggregate(selected)
	if stats.TotalCalls != 1 || stats.ByStatus["degraded"] != 1 {
		t.Fatalf("%+v", stats)
	}
	services := stats.Entrypoints[0].DownstreamErrorServices
	if len(services) != 3 {
		t.Fatalf("%+v", services)
	}
	for _, s := range services {
		if s.RequestCount != 1 {
			t.Fatalf("multiple errors counted twice: %+v", s)
		}
	}
	entries, _ := SelectServiceEntries([]*Trace{tr, tr}, query("user"))
	local, _ := Aggregate(entries)
	if local.TotalCalls != 2 || local.ByStatus["ok"] != 1 || local.ByStatus["degraded"] != 1 || local.Entrypoints[0].P50Ms != 200 || local.Entrypoints[0].P95Ms != 300 {
		t.Fatalf("wrong local stats: %+v", local)
	}
	downstream := local.Entrypoints[0].DownstreamErrorServices
	if len(downstream) != 1 || downstream[0].Service != "profile" || downstream[0].RequestCount != 1 {
		t.Fatalf("sibling errors leaked or duplicate errors counted: %+v", downstream)
	}
	q := query("user")
	q.Operation = "GET /same"
	found := Search([]*Trace{tr, tr}, q) // 候选重复也不重复返回入口调用。
	if found.MatchedCount != 2 || found.Items[0].EntrySpanID != "u2" || found.Items[0].Status != "ok" {
		t.Fatalf("%+v", found)
	}
	item := found.Items[1]
	if item.DurationMs != 200 || item.Status != "degraded" || item.ErrorSummary.SpanID != "db" || item.ErrorSummary.Service != "profile" {
		t.Fatalf("%+v", item)
	}
	q.Status, q.MinDurationMs = "degraded", 250
	if len(Search([]*Trace{tr}, q).Items) != 0 {
		t.Fatal("used global duration instead of local duration")
	}
	q.Status, q.MinDurationMs, q.StartMs = "", 0, 1300
	if got := Search([]*Trace{tr}, q); len(got.Items) != 1 || got.Items[0].EntrySpanID != "u2" {
		t.Fatal("wrong entry time filter")
	}
	if tr.Root.SpanID != "root" || tr.Root.DurationMs != 1000 {
		t.Fatal("search mutated global root")
	}
	// 上游请求失败也不改变正常下游入口的分类。
	tr.Root.Status = "error"
	entries, _ = SelectServiceEntries([]*Trace{tr}, query("user"))
	local, _ = Aggregate(entries)
	if local.ByStatus["failed"] != 0 || local.ByStatus["ok"] != 1 {
		t.Fatal("upstream error leaked into stats")
	}
	q = query("user")
	q.Status = "ok"
	if got := Search([]*Trace{tr}, q); len(got.Items) != 1 || got.Items[0].EntrySpanID != "u2" {
		t.Fatalf("upstream/sibling error leaked: %+v", got)
	}
}

func TestExpectedDuplicateOnlyExcludesMatchingDatabaseError(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		marked, redisError               bool
		code                             int
		dbError, system, dbService, want string
	}{
		{"handled", true, false, 409, "*errors.errorString: duplicated key not allowed", "mysql", "user", "ok"},
		{"native code", true, false, 409, "*mysql.MySQLError: Error 1062 (23000): Duplicate entry 'a' for key 'users.username'", "mysql", "user", "ok"},
		{"redis also failed", true, true, 409, "duplicated key not allowed", "mysql", "user", "degraded"},
		{"unhandled duplicate", false, false, 409, "duplicated key not allowed", "mysql", "user", "degraded"},
		{"other database error", true, false, 404, "i/o timeout", "mysql", "user", "degraded"},
		{"not mysql", true, false, 409, "duplicated key not allowed", "redis", "user", "degraded"},
		{"other service", true, false, 409, "duplicated key not allowed", "mysql", "other", "degraded"},
		{"500 cannot be ignored", true, false, 500, "duplicated key not allowed", "mysql", "user", "failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := span("r", "", "gateway", "server", "POST /user", 0, 100)
			entry := span("u", "r", "user", "server", "POST /user", 1, 90)
			root.Attrs["http.response.status_code"] = tc.code
			entry.Attrs["http.response.status_code"] = tc.code
			op := span("op", "u", "user", "internal", "mysql.Create", 2, 50)
			op.Attrs["user.duplicate"] = tc.marked
			db := span("db", "op", tc.dbService, "client", "INSERT users", 3, 20)
			db.Attrs["db.system.name"] = tc.system
			db.Status, db.Error, db.StatusDesc = "error", tc.dbError, tc.dbError
			redis := span("redis", "u", "user", "client", "GET cache", 60, 20)
			if tc.redisError {
				redis.Status, redis.Error = "error", "i/o timeout"
			}
			tr := built(t, "duplicate", root, entry, op, db, redis)
			if tr.Status != tc.want {
				t.Fatalf("got %s want %s", tr.Status, tc.want)
			}
			entries, _ := SelectServiceEntries([]*Trace{tr}, query("gateway"))
			stats, _ := Aggregate(entries)
			if tc.want == "ok" && len(stats.Entrypoints[0].DownstreamErrorServices) != 0 {
				t.Fatal("expected error leaked into stats")
			}
			search := Search([]*Trace{tr}, query("user"))
			entries, _ = SelectServiceEntries([]*Trace{tr}, query("user"))
			local, _ := Aggregate(entries)
			if local.TotalCalls != 1 || local.ByStatus[tc.want] != 1 {
				t.Fatalf("local duplicate classification: %+v", local)
			}
			if len(search.Items) != 1 || search.Items[0].Status != tc.want {
				t.Fatalf("%+v", search)
			}
			if tc.redisError && search.Items[0].ErrorSummary.SpanID != "redis" {
				t.Fatal("summary did not skip duplicate and continue DFS")
			}
			if tc.want == "ok" && search.Items[0].ErrorSummary != nil {
				t.Fatal("expected error leaked into summary")
			}
			detail := BuildDetail(tr, 50)
			actualDB := detail.Root.Children[0].Children[0].Children[0]
			if actualDB.Status != "error" || actualDB.Error != tc.dbError {
				t.Fatal("raw error was erased")
			}
			if tc.want == "ok" && actualDB.ExpectedError != "handled_mysql_duplicate_key" {
				t.Fatal("missing exclusion explanation")
			}
		})
	}
}

func TestForestRetainsDownstreamAndDetailFragments(t *testing.T) {
	entry := span("u", "missing-upstream", "user", "server", "GET /users", 100, 100)
	child := span("db", "u", "user", "client", "SELECT", 120, 30)
	tr := built(t, "partial", child, entry)
	if tr.Root != nil || !tr.Incomplete || tr.Status != "unknown" {
		t.Fatalf("%+v", tr)
	}
	if got := Search([]*Trace{tr}, query("user")); len(got.Items) != 1 || len(got.Items[0].Warnings) == 0 {
		t.Fatalf("%+v", got)
	}
	if got, warnings := SelectServiceEntries([]*Trace{tr}, query("user")); len(got) != 1 || len(warnings) == 0 || tr.Root != nil {
		t.Fatal("orphan entry lost or global root mutated")
	}
	detail := BuildDetail(tr, 50)
	if detail.Root != nil || len(detail.Fragments) != 1 || detail.ReturnedSpanCount != 2 || !detail.Incomplete || detail.StartMs != 100 {
		t.Fatalf("%+v", detail)
	}
	small := BuildDetail(tr, 1)
	if !small.Truncated || small.ReturnedSpanCount != 1 {
		t.Fatal("fragment limit ignored")
	}
	// 重建不污染原始节点；非 server 根不妨碍下游 server 入口搜索。
	root := span("task", "", "job", "internal", "job.run", 0, 300)
	entry.ParentSpanID = "task"
	tr = built(t, "job", root, entry, child)
	if len(Search([]*Trace{tr}, query("user")).Items) != 1 {
		t.Fatal("non-server global root was rejected")
	}
	if len(root.Children) != 0 || len(entry.Children) != 0 {
		t.Fatal("source spans mutated")
	}
	// 两个明确根不随机挑选其中一个。
	entry.ParentSpanID = ""
	tr = built(t, "multiple", root, entry, child)
	if tr.Root != nil || len(BuildDetail(tr, 50).Fragments) != 2 {
		t.Fatal("ambiguous roots not preserved")
	}
	if _, err := BuildTrace("cycle", []*Span{span("a", "b", "s", "server", "a", 0, 1), span("b", "a", "s", "internal", "b", 0, 1)}); err == nil {
		t.Fatal("cycle accepted")
	}
	if _, err := BuildTrace("duplicate-id", []*Span{root, root}); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
}

func TestJaegerMultiServiceNormalization(t *testing.T) {
	// 通过实际 HTTP provider 验证 Process -> Service、上游缺失、业务标记与 GORM 错误转换。
	input := `{"data":[{"traceID":"t","processes":{"p":{"serviceName":"user"}},"spans":[
	{"traceID":"t","spanID":"u","operationName":"POST /users","processID":"p","startTime":1000000,"duration":100000,"references":[{"refType":"CHILD_OF","traceID":"t","spanID":"missing"}],"tags":[{"key":"span.kind","type":"string","value":"server"},{"key":"http.response.status_code","type":"int64","value":409}]},
	{"traceID":"t","spanID":"op","operationName":"mysql.Create","processID":"p","startTime":1001000,"duration":20000,"references":[{"refType":"CHILD_OF","traceID":"t","spanID":"u"}],"tags":[{"key":"user.duplicate","type":"bool","value":true}]},
	{"traceID":"t","spanID":"db","operationName":"INSERT users","processID":"p","startTime":1002000,"duration":10000,"references":[{"refType":"CHILD_OF","traceID":"t","spanID":"op"}],"tags":[{"key":"db.system.name","type":"string","value":"mysql"},{"key":"otel.status_code","type":"string","value":"ERROR"},{"key":"otel.status_description","type":"string","value":"duplicated key not allowed"}],"logs":[{"fields":[{"key":"exception.message","type":"string","value":"duplicated key not allowed"}]}]}
	]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(input))
	}))
	defer server.Close()
	provider := NewJaegerProvider(server.URL)
	batch, err := provider.FindTraces(context.Background(), TraceQuery{Service: "user", Limit: 1})
	if err != nil || batch.RawCount != 1 {
		t.Fatalf("candidate count lost: %+v %v", batch, err)
	}
	batch, err = provider.FindTraces(context.Background(), TraceQuery{Service: "user"})
	if err != nil || len(batch.Notices) != 0 {
		t.Fatalf("unexpected notices: %v %v", batch.Notices, err)
	}
	tr, err := provider.GetTrace(context.Background(), "t")
	if err != nil || tr == nil {
		t.Fatalf("%+v %v", tr, err)
	}
	result := Search([]*Trace{tr}, query("user"))
	if len(result.Items) != 1 || result.Items[0].Status != "ok" || result.Items[0].ErrorSummary != nil {
		t.Fatalf("%+v", result)
	}
	b, _ := json.Marshal(BuildDetail(tr, 50))
	if !strings.Contains(string(b), "handled_mysql_duplicate_key") || !strings.Contains(string(b), "duplicated key not allowed") {
		t.Fatal(string(b))
	}
}
