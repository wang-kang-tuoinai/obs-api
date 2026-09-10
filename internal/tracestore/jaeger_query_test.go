package tracestore

import (
	"net/url"
	"testing"
)

func TestQueryMinDuration(t *testing.T) {
	p := NewJaegerProvider("http://jaeger:16686")
	for _, tc := range []struct {
		ms   float64
		want string
	}{{0, ""}, {100, "100ms"}, {0.125, "0.125ms"}} {
		u, err := url.Parse(p.buildQueryURL(TraceQuery{Service: "app", Operation: "GET /users", Start: 1000, End: 2000, Limit: 200, MinDurationMs: tc.ms}))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if q.Get("minDuration") != tc.want || (tc.ms == 0 && q.Has("minDuration")) {
			t.Fatal(q)
		}
		if q.Get("operation") != "GET /users" || q.Get("start") != "1000000" || q.Get("limit") != "200" {
			t.Fatal(q)
		}
	}
}
