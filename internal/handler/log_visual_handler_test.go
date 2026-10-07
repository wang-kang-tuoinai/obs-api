package handler

import (
	"context"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"obs-api/internal/logstore"
	"testing"
)

type blockingHistogram struct{}

func (blockingHistogram) QueryHistogram(ctx context.Context, q logstore.HistogramQuery) (*logstore.Histogram, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestLogVisualValidationAndInitialResponse(t *testing.T) {
	cache := logstore.NewLogVisualCache(blockingHistogram{}, logstore.DefaultLogVisualOptions())
	defer cache.Close()
	h := NewVisualHandler(nil, emptyVisualProvider{}, "svc", cache)
	r := gin.New()
	r.GET("/visual", h.Logs)
	for _, query := range []string{"", "?service=svc&method=GET", "?service=svc&route=/a", "?service=svc&method=get&route=/a", "?service=svc&start_ms=1", "?service=svc&start_ms=1&end_ms=900002", "?service=svc&operation=GET", "?service=svc&start_ms=-1&end_ms=1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/visual"+query, nil))
		if w.Code != 400 {
			t.Fatalf("%s: %d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/visual?service=svc&method=PUT&route=/users/:id&start_ms=10000&end_ms=20000", nil))
	var result logstore.LogVisualResponse
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !result.Loading || result.Initialized || result.Summary != nil || result.Method != "PUT" || result.Route != "/users/:id" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Body.String())
	}
}
