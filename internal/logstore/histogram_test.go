package logstore

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestHistogramClippedBucketsAndFilters(t *testing.T) {
	f := &queryFixture{columns: []string{"bucket_start_ms", "level", "count"}, rows: [][]driver.Value{{int64(10000), "INFO", int64(3)}, {int64(30000), "ERROR", int64(2)}, {int64(30000), "FATAL", int64(1)}}}
	h, err := fixtureStore(t, f).QueryHistogram(context.Background(), HistogramQuery{"svc", "PUT", "/users/:id", 15500, 35500})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Buckets) != 3 || h.Buckets[0].StartMs != 15500 || h.Buckets[2].EndMs != 35500 || h.Buckets[1].Total != 0 || h.Summary.Total != 6 || h.Summary.ByLevel["FATAL"] != 1 {
		t.Fatalf("%+v", h)
	}
	if !reflect.DeepEqual(boundValues(f), []any{int64(10000), int64(10000), "svc", int64(15500), int64(35500), "PUT", "/users/:id"}) {
		t.Fatal(boundValues(f))
	}
	if !strings.Contains(f.query, "ts >= ? AND ts < ? AND method = ? AND route = ?") || f.calls != 1 {
		t.Fatal(f.query)
	}
}
func TestHistogramEmptySuccessAndFailureDiffer(t *testing.T) {
	f := &queryFixture{columns: []string{"bucket_start_ms", "level", "count"}}
	store := fixtureStore(t, f)
	h, err := store.QueryHistogram(context.Background(), HistogramQuery{Service: "svc", StartMs: 10000, EndMs: 30000})
	if err != nil || len(h.Buckets) != 2 || h.Summary.Total != 0 {
		t.Fatalf("%+v %v", h, err)
	}
	if strings.Contains(f.query, "route =") || strings.Contains(f.query, "BETWEEN") {
		t.Fatal(f.query)
	}
	f.err = errors.New("offline")
	h, err = store.QueryHistogram(context.Background(), HistogramQuery{Service: "svc", StartMs: 10000, EndMs: 30000})
	if err == nil || h != nil {
		t.Fatal("query failure manufactured empty result")
	}
}
func TestHistogramWindowValidation(t *testing.T) {
	f := &queryFixture{}
	for _, q := range []HistogramQuery{{Service: "svc", StartMs: 100, EndMs: 100}, {Service: "svc", StartMs: 1, EndMs: 900002}, {StartMs: 1, EndMs: 10}, {Service: "svc", Method: "GET", StartMs: 1, EndMs: 10}} {
		if _, err := fixtureStore(t, f).QueryHistogram(context.Background(), q); err == nil {
			t.Fatal(q)
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid query reached DB")
	}
}
