package handler

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"obs-api/internal/tracestore"
	"strings"
	"testing"
)

type searchProvider struct {
	traces []*tracestore.Trace
	query  tracestore.TraceQuery
	err    error
}

func (p *searchProvider) GetTrace(context.Context, string) (*tracestore.Trace, error) {
	return nil, nil
}
func (p *searchProvider) FindTraces(_ context.Context, q tracestore.TraceQuery) ([]*tracestore.Trace, []string, error) {
	p.query = q
	return p.traces, []string{"candidate warning"}, p.err
}

func TestSearchContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	makeTrace := func(id, op string, d float64) *tracestore.Trace {
		root := &tracestore.Span{SpanID: id, Service: "app", Operation: op, Kind: "server", StartMs: 1500000, DurationMs: d, Children: []*tracestore.Span{{SpanID: "child", Operation: "redis", Status: "error", Error: "timeout"}}}
		return &tracestore.Trace{TraceID: id, Root: root, RootOperation: op, DurationMs: d, Status: "degraded", ErrorOrigin: "redis", ErrorDesc: "timeout"}
	}
	p := &searchProvider{traces: []*tracestore.Trace{makeTrace("a", "GET /users", 100), makeTrace("b", "GET /users", 300), makeTrace("c", "redis", 500)}}
	r := gin.New()
	r.GET("/traces/search", NewTraceHandler(p).Search)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traces/search?service=app&operation=GET%20%2Fusers&start=1000&end=2000&status=degraded&min_duration_ms=100&limit=1", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Items []struct {
			TraceID string `json:"trace_id"`
			Status  string `json:"status"`
		}
		Meta struct {
			Matched int  `json:"matched_count"`
			More    bool `json:"has_more_matches"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].TraceID != "b" || result.Items[0].Status != "degraded" || result.Meta.Matched != 2 || !result.Meta.More {
		t.Fatal(w.Body.String())
	}
	if p.query.Limit != 200 || p.query.Start != 1000000 {
		t.Fatal(p.query)
	}
	if strings.Contains(w.Body.String(), "children") || !strings.Contains(w.Body.String(), "candidate warning") {
		t.Fatal(w.Body.String())
	}
	for _, query := range []string{"", "service=app&status=error", "service=app&sort=oops", "service=app&min_duration_ms=NaN", "service=app&min_duration_ms=-1"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/traces/search?"+query, nil))
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	p.traces = nil
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traces/search?service=app", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatal(w.Body.String())
	}
	p.err = errors.New("unavailable")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/traces/search?service=app", nil))
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
}
