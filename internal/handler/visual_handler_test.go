package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"obs-api/internal/tracestore"
	"strconv"
	"testing"
	"time"
)

type emptyVisualProvider struct{}

func (emptyVisualProvider) GetTrace(context.Context, string) (*tracestore.Trace, error) {
	return nil, nil
}
func (emptyVisualProvider) GetOperations(context.Context, string) ([]string, error) {
	return []string{}, nil
}
func (emptyVisualProvider) GetServices(context.Context) ([]string, error) {
	return []string{"user"}, nil
}
func (emptyVisualProvider) FindTraces(context.Context, tracestore.TraceQuery) (tracestore.TraceBatch, error) {
	return tracestore.TraceBatch{}, nil
}
func TestVisualHandlerWindowsAndLoading(t *testing.T) {
	p := emptyVisualProvider{}
	cache := tracestore.NewVisualCache(p, tracestore.DefaultVisualOptions())
	defer cache.Close()
	h := NewVisualHandler(cache, p, "user", nil)
	r := gin.New()
	r.GET("/visual", h.Traces)
	r.GET("/services", h.Services)
	now := time.Now().UnixMilli()
	end := strconv.FormatInt(now, 10)
	start := strconv.FormatInt(now-900000, 10)
	for _, query := range []string{"", "?service=user&start_ms=1", "?service=user&start_ms=0&end_ms=1", "?service=user&start_ms=1000&end_ms=901001", "?service=user&start_ms=" + end + "&end_ms=" + start} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/visual"+query, nil))
		if w.Code != 400 {
			t.Fatalf("accepted %s: %d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/visual?service=user&start_ms="+start+"&end_ms="+end, nil))
	var body tracestore.VisualResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || body.Points == nil || !body.Loading || body.Window.StartMs != now-900000 || body.Window.EndMs != now || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("response=%s", w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/services", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}
