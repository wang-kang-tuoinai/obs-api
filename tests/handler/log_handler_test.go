package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"obs-api/internal/handler"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	lg "obs-api/internal/logstore"
)

type fakeLogStore struct {
	statsQuery     lg.StatsQuery
	templatesQuery lg.TemplatesQuery
	stats          lg.StatsResult
	templates      lg.TemplatesResult
	err            error
	calls          int
}

func (s *fakeLogStore) QueryStats(_ context.Context, q lg.StatsQuery) (*lg.StatsResult, error) {
	s.statsQuery = q
	s.calls++
	return &s.stats, s.err
}
func (s *fakeLogStore) QueryTemplates(_ context.Context, q lg.TemplatesQuery) (*lg.TemplatesResult, error) {
	s.templatesQuery = q
	s.calls++
	return &s.templates, s.err
}
func (s *fakeLogStore) QuerySearch(context.Context, lg.SearchQuery) (*lg.SearchResult, error) {
	panic("unexpected search")
}
func callLogHandler(t *testing.T, store *fakeLogStore, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	h := handler.NewLogHandler(store)
	r.GET("/logs/stats", h.Stats)
	r.GET("/logs/templates", h.Templates)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}

func TestLogStatsResponseAndFilters(t *testing.T) {
	store := &fakeLogStore{stats: lg.StatsResult{Summaries: []lg.StatsSummary{{Service: "user", Total: 3, ErrorCount: 1, ErrorRate: 0.3333, ByLevel: map[string]int64{"ERROR": 1, "INFO": 2}}}}}
	w := callLogHandler(t, store, "/logs/stats?start=10&end=20&service=%20user%20&route=/users&method=PUT")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var resp lg.LogStatsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Window.Start != 10 || resp.Window.End != 20 || len(resp.Summaries) != 1 || resp.Summaries[0].Service != "user" || resp.GeneratedAt == 0 {
		t.Fatalf("response=%+v", resp)
	}
	if store.statsQuery != (lg.StatsQuery{Start: 10, End: 20, Service: "user", Route: "/users", Method: "PUT"}) {
		t.Fatal(store.statsQuery)
	}
	if strings.Contains(w.Body.String(), `"summary":`) || strings.Contains(w.Body.String(), "top_templates") {
		t.Fatal(w.Body.String())
	}
}

func TestLogStatsEmptyAndDeprecatedParameters(t *testing.T) {
	store := &fakeLogStore{}
	w := callLogHandler(t, store, "/logs/stats?start=10&end=20")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"summaries":[]`) || store.statsQuery.Service != "" {
		t.Fatal(w.Body.String())
	}
	for _, query := range []string{"level=ERROR", "level=", "top_n=10", "top_n="} {
		store := &fakeLogStore{}
		w := callLogHandler(t, store, "/logs/stats?"+query)
		if w.Code != 400 || store.calls != 0 {
			t.Fatalf("%s: %d %s", query, w.Code, w.Body.String())
		}
	}
}

func TestLogTemplatesRequireService(t *testing.T) {
	for _, query := range []string{"", "?service=", "?service=%20%20"} {
		store := &fakeLogStore{}
		w := callLogHandler(t, store, "/logs/templates"+query)
		if w.Code != 400 || store.calls != 0 {
			t.Fatalf("%s: %d", query, w.Code)
		}
	}
}

func TestLogTemplatesResponseAndLimits(t *testing.T) {
	for _, tt := range []struct {
		query     string
		limit     int
		corrected bool
	}{
		{"", 200, false}, {"&limit=2", 2, false}, {"&limit=500", 500, false},
		{"&limit=501", 200, true}, {"&limit=0", 200, true}, {"&limit=oops", 200, true}, {"&limit=", 200, true},
	} {
		store := &fakeLogStore{}
		w := callLogHandler(t, store, "/logs/templates?service=%20user%20&start=10&end=20&level=WARN&route=/users&method=GET"+tt.query)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var resp lg.TemplatesResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		if resp.Service != "user" || resp.Items == nil || resp.HasMore || (len(resp.Notices) > 0) != tt.corrected {
			t.Fatalf("%s: %+v", tt.query, resp)
		}
		if store.templatesQuery != (lg.TemplatesQuery{Service: "user", Start: 10, End: 20, Level: "WARN", Route: "/users", Method: "GET", Limit: tt.limit}) {
			t.Fatal(store.templatesQuery)
		}
	}
	store := &fakeLogStore{templates: lg.TemplatesResult{Items: []lg.TemplateStat{{Template: "timeout", Count: 100}}, HasMore: true}}
	w := callLogHandler(t, store, "/logs/templates?service=user&start=10&end=20&limit=1")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"has_more":true`) || !strings.Contains(w.Body.String(), "匹配模板组数超过") {
		t.Fatal(w.Body.String())
	}
}

func TestLogDatabaseFailureIsNotEmptySuccess(t *testing.T) {
	for _, path := range []string{"/logs/stats", "/logs/templates?service=user"} {
		w := callLogHandler(t, &fakeLogStore{err: errors.New("database unavailable")}, path)
		if w.Code != 500 {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
}
