package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"obs-api/internal/handler"
	"strings"
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
	h := handler.NewTraceHandler(p)
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
	var stats handler.TraceStatsResponse
	get("/stats?service=gateway&operation=GET%20%2Fsame", &stats)
	if stats.Service != "gateway" || stats.Stats.TotalCalls != 1 || stats.Stats.Entrypoints[0].DownstreamErrorServices[0].Service != "other" {
		t.Fatalf("%+v", stats)
	}
	get("/stats?service=user", &stats)
	if stats.Stats.TotalCalls != 1 || stats.Stats.ByStatus["ok"] != 1 || stats.Stats.Entrypoints[0].P50Ms != 100 || len(stats.Stats.Entrypoints[0].DownstreamErrorServices) != 0 {
		t.Fatal("stats did not isolate a non-root service")
	}
	var found handler.TraceSearchResponse
	get("/search?service=user&operation=GET%20%2Fsame&status=ok", &found)
	if len(found.Items) != 1 || found.Items[0].EntrySpanID != "user" || found.Items[0].DurationMs != 100 || found.Items[0].ErrorSummary != nil {
		t.Fatalf("%+v", found)
	}
	get("/search?service=user&min_duration_ms=200", &found)
	if len(found.Items) != 0 || p.query.MinDurationMs != 200 {
		t.Fatal("local duration not enforced or candidate filter lost")
	}
}

func TestStatsHTTPPartialAndTotalFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, allFailed := range []bool{false, true} {
		t.Run(fmt.Sprint("allFailed=", allFailed), func(t *testing.T) {
			jaeger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == "/api/operations" {
					fmt.Fprint(w, `{"data":[{"name":"a","spanKind":"server"},{"name":"b","spanKind":"server"}]}`)
					return
				}
				if allFailed || req.URL.Query().Get("operation") == "b" {
					w.WriteHeader(503)
					return
				}
				if req.URL.Query().Get("limit") != "1500" {
					t.Error(req.URL)
				}
				fmt.Fprint(w, `{"data":[]}`)
			}))
			defer jaeger.Close()
			r := gin.New()
			r.GET("/stats", handler.NewTraceHandler(tracestore.NewJaegerProvider(jaeger.URL)).Stats)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/stats?service=user&start=1000&end=2000", nil))
			wantCode := 200
			if allFailed {
				wantCode = 502
			}
			if w.Code != wantCode {
				t.Fatal(w.Code, w.Body.String())
			}
			var response handler.TraceStatsResponse
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Meta.OperationQueries) != 2 || response.Meta.OperationQueries[1].Status != tracestore.OperationFailed || response.Meta.OperationQueries[1].RawTraceCount != nil {
				t.Fatal(w.Body.String())
			}
			if !allFailed && (*response.Meta.OperationQueries[0].RawTraceCount != 0 || response.Meta.OperationQueries[0].Status != tracestore.OperationSuccess) {
				t.Fatal(w.Body.String())
			}
		})
	}
}

func TestStatsCountsCallsAndReportsCandidateScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &tracestore.Span{SpanID: "a", ParentSpanID: "missing", Service: "user", Kind: "server", Operation: "GET /users", StartMs: 1000000, DurationMs: 20}
	b := &tracestore.Span{SpanID: "b", Service: "user", Kind: "server", Operation: "GET /users", StartMs: 2000000, DurationMs: 40}
	tr, err := tracestore.BuildTrace("partial", []*tracestore.Span{a, b})
	if err != nil {
		t.Fatal(err)
	}
	p := &searchProvider{traces: []*tracestore.Trace{tr, tr, nil}}
	r := gin.New()
	r.GET("/stats", handler.NewTraceHandler(p).Stats)
	get := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/stats?"+query, nil))
		return w
	}
	w := get("service=user&operation=GET%20%2Fusers&start=1000&end=2000")
	var result handler.TraceStatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || result.Stats.TotalCalls != 2 || result.Meta.FetchedTraces != 1 || result.Meta.PerOperationLimit != 5000 || result.Meta.Window.Start != 1000 || result.Meta.Window.End != 2000 {
		t.Fatal(w.Code, w.Body.String())
	}
	if p.query.Service != "user" || p.query.Operation != "GET /users" || p.query.Start != 1000000 || p.query.End != 2000000 || p.query.Limit != 5000 {
		t.Fatalf("wrong provider query: %+v", p.query)
	}
	if strings.Contains(w.Body.String(), "total_traces") || !strings.Contains(w.Body.String(), "candidate warning") || strings.Count(strings.Join(result.Notices, " "), "缺少父节点") != 1 {
		t.Fatal(w.Body.String())
	}
	if w := get("service=user&operation=missing&start=1000&end=2000"); w.Code != 200 || !strings.Contains(w.Body.String(), `"total_calls":0`) || !strings.Contains(w.Body.String(), `"entrypoints":[]`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := get("service=%20%20"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := get("service=user&limit=1500"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if len(result.Meta.OperationQueries) != 1 || result.Meta.OperationQueries[0].Status != tracestore.OperationSuccess || *result.Meta.OperationQueries[0].RawTraceCount != 3 {
		t.Fatalf("wrong query metadata: %+v", result.Meta)
	}
	p.err = errors.New("jaeger unavailable")
	if w := get("service=user"); w.Code != 502 {
		t.Fatal(w.Code)
	}
}
