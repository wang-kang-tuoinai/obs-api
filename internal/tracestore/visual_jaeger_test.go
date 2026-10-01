package tracestore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestVisualJaegerServiceDirectoryAndEntryScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/services" {
			fmt.Fprint(w, `{"data":["user","gateway","user",""]}`)
			return
		}
		if r.URL.Path != "/api/traces" || r.URL.Query().Get("service") != "user" || r.URL.Query().Get("start") != "1000000" {
			t.Error(r.URL.String())
		}
		fmt.Fprint(w, `{"data":[{"traceID":"t","processes":{"u":{"serviceName":"user"},"g":{"serviceName":"gateway"},"d":{"serviceName":"db"}},"spans":[
		{"spanID":"g","operationName":"GET /gateway","processID":"g","startTime":1000000,"duration":100000,"tags":[{"key":"span.kind","value":"server"}]},
		{"spanID":"u","operationName":"GET /user","processID":"u","startTime":1010000,"duration":30000,"references":[{"refType":"CHILD_OF","spanID":"g"}],"tags":[{"key":"span.kind","value":"server"}]},
		{"spanID":"d","operationName":"query","processID":"d","startTime":1011000,"duration":20000,"references":[{"refType":"CHILD_OF","spanID":"u"}],"tags":[{"key":"otel.status_code","value":"ERROR"}]}
		]}]}`)
	}))
	defer server.Close()
	p := NewJaegerProvider(server.URL)
	services, err := p.GetServices(context.Background())
	if err != nil || !reflect.DeepEqual(services, []string{"gateway", "user"}) {
		t.Fatal(services, err)
	}
	m, _ := newManualVisual(p)
	points, _, raw, err := m.queryPoints(context.Background(), TraceQuery{Service: "user", Operation: "GET /user", Start: 1000, End: 2000, Limit: 1500})
	if err != nil || raw != 1 || len(points) != 1 || points[0].EntrySpanID != "u" || points[0].Status != "degraded" || points[0].DurationMs != 30 {
		t.Fatalf("points=%+v raw=%d err=%v", points, raw, err)
	}
}
