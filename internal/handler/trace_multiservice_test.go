package handler

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"obs-api/internal/tracestore"
)

func TestMultiServiceHTTPContracts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := &tracestore.Span{SpanID: "gateway", Service: "gateway", Kind: "server", Operation: "GET /same", StartMs: 1500000, DurationMs: 500}
	entry := &tracestore.Span{SpanID: "user", ParentSpanID: "gateway", Service: "user", Kind: "server", Operation: "GET /same", StartMs: 1500100, DurationMs: 100}
	bad := &tracestore.Span{SpanID: "bad", ParentSpanID: "gateway", Service: "other", Kind: "server", Operation: "GET /bad", StartMs: 1500200, DurationMs: 50, Status: "error", Error: "timeout"}
	tr, err := tracestore.BuildTrace("trace", []*tracestore.Span{root, entry, bad})
	if err != nil {
		t.Fatal(err)
	}
	p := &searchProvider{traces: []*tracestore.Trace{tr}}
	r := gin.New()
	h := NewTraceHandler(p)
	r.GET("/stats", h.Stats)
	r.GET("/search", h.Search)
	get := func(path string, out any) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path+"&start=1000&end=2000", nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatal(err)
		}
	}
	var stats TraceStatsResponse
	get("/stats?service=gateway&operation=GET%20%2Fsame", &stats)
	if stats.Service != "gateway" || stats.Stats.TotalTraces != 1 || stats.Stats.Entrypoints[0].DownstreamErrorServices[0].Service != "other" {
		t.Fatalf("%+v", stats)
	}
	get("/stats?service=user", &stats)
	if stats.Stats.TotalTraces != 0 {
		t.Fatal("stats accepted a non-root service")
	}
	var found TraceSearchResponse
	get("/search?service=user&operation=GET%20%2Fsame&status=ok", &found)
	if len(found.Items) != 1 || found.Items[0].EntrySpanID != "user" || found.Items[0].DurationMs != 100 || found.Items[0].ErrorSummary != nil {
		t.Fatalf("%+v", found)
	}
	get("/search?service=user&min_duration_ms=200", &found)
	if len(found.Items) != 0 || p.query.MinDurationMs != 200 {
		t.Fatal("local duration not enforced or candidate filter lost")
	}
}
