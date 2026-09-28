package tracestore

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestJaegerOperationsAndLargeLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/operations" {
			if r.URL.Query().Get("service") != "user service" || r.URL.Query().Get("spanKind") != "server" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, `{"data":[{"name":"PUT /b","spanKind":"server"},{"name":"GET /a","spanKind":"server"},{"name":"GET /a","spanKind":"server"},{"name":"SELECT","spanKind":"client"}],"errors":null}`)
			return
		}
		if r.URL.Query().Get("limit") != "1500" && r.URL.Query().Get("limit") != "5000" {
			t.Error(r.URL)
		}
		// 两条原始记录都损坏，仍应返回原始数量，而不是把数量当作零。
		fmt.Fprint(w, `{"data":[{"traceID":"a","spans":[]},{"traceID":"b","spans":[]}]}`)
	}))
	defer server.Close()
	p := NewJaegerProvider(server.URL)
	ops, err := p.GetOperations(context.Background(), "user service")
	if err != nil || !reflect.DeepEqual(ops, []string{"GET /a", "PUT /b"}) {
		t.Fatalf("%v %v", ops, err)
	}
	for _, limit := range []int{1500, 5000} {
		batch, err := p.FindTraces(context.Background(), TraceQuery{Service: "user", Limit: limit})
		if err != nil || batch.RawCount != 2 || len(batch.Traces) != 0 || len(batch.Notices) == 0 {
			t.Fatalf("%+v %v", batch, err)
		}
	}
	u, _ := url.Parse(p.buildQueryURL(TraceQuery{Limit: 5001}))
	if u.Query().Get("limit") != "200" {
		t.Fatal(u)
	}
}

func TestJaegerQueryFailuresAreNotEmptySuccess(t *testing.T) {
	for _, tc := range []struct {
		code int
		body string
	}{
		{404, `{}`}, {503, `{}`}, {200, `not json`}, {200, `{"data":[],"errors":[{"msg":"failed"}]}`},
	} {
		t.Run(fmt.Sprint(tc.code, tc.body), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.code); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			p := NewJaegerProvider(server.URL)
			if _, err := p.GetOperations(context.Background(), "user"); err == nil {
				t.Fatal("directory error swallowed")
			}
			if _, err := p.FindTraces(context.Background(), TraceQuery{Service: "user"}); err == nil {
				t.Fatal("query error swallowed")
			}
			if tc.code == 404 {
				if tr, err := p.GetTrace(context.Background(), "missing"); tr != nil || err != nil {
					t.Fatal(tr, err)
				}
			}
		})
	}
}
