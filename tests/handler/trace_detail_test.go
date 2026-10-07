package handler_test

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"obs-api/internal/handler"
	"obs-api/internal/tracestore"
	"strings"
	"testing"
)

type detailProvider struct {
	searchProvider
	trace *tracestore.Trace
}

func (p *detailProvider) GetTrace(context.Context, string) (*tracestore.Trace, error) {
	return p.trace, p.err
}

func TestGetTraceDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := &detailProvider{trace: &tracestore.Trace{TraceID: "abc", SpanCount: 2, Root: &tracestore.Span{SpanID: "root", Children: []*tracestore.Span{{SpanID: "child"}}}}}
	r := gin.New()
	r.GET("/traces/:trace_id", handler.NewTraceHandler(p).GetTrace)
	run := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	w := run("/traces/abc?max_spans=1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"truncated":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, v := range []string{"0", "201", "bad"} {
		if w := run("/traces/abc?max_spans=" + v); w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	p.trace = nil
	if w := run("/traces/abc"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	p.err = errors.New("unavailable")
	if w := run("/traces/abc"); w.Code != 502 {
		t.Fatal(w.Code)
	}
}
