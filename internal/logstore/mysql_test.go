package logstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Feed database/sql grouped rows and capture bound parameters without a live database.
type queryFixture struct {
	columns []string
	rows    [][]driver.Value
	err     error
	query   string
	args    []driver.NamedValue
	calls   int
}

func (f *queryFixture) Connect(context.Context) (driver.Conn, error) { return f, nil }
func (f *queryFixture) Driver() driver.Driver                        { return f }
func (f *queryFixture) Open(string) (driver.Conn, error)             { return f, nil }
func (f *queryFixture) Close() error                                 { return nil }
func (f *queryFixture) Begin() (driver.Tx, error)                    { return nil, errors.New("unexpected transaction") }
func (f *queryFixture) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (f *queryFixture) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	f.query, f.args = query, args
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &fixtureRows{columns: f.columns, rows: f.rows}, nil
}

type fixtureRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

func (r *fixtureRows) Columns() []string { return r.columns }
func (r *fixtureRows) Close() error      { return nil }
func (r *fixtureRows) Next(dest []driver.Value) error {
	if r.index == len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}
func fixtureStore(t *testing.T, f *queryFixture) *MysqlStore {
	t.Helper()
	db := sql.OpenDB(f)
	t.Cleanup(func() { db.Close() })
	return NewMysqlStore(db)
}
func boundValues(f *queryFixture) []any {
	values := make([]any, len(f.args))
	for i, arg := range f.args {
		values[i] = arg.Value
	}
	return values
}

func TestStatsPerServiceCountsAndOrder(t *testing.T) {
	f := &queryFixture{columns: []string{"service", "level", "count"}, rows: [][]driver.Value{
		{"healthy", "INFO", int64(100)}, {"z", "ERROR", int64(2)}, {"z", "WARN", int64(1)},
		{"b", "ERROR", int64(2)}, {"b", "WARN", int64(3)}, {"a", "ERROR", int64(2)}, {"a", "WARN", int64(3)},
		{"a", "INFO", int64(1)}, {"a", "TRACE", int64(1)}, {"", "INFO", int64(1)}, {"  ", "WARN", int64(1)},
	}}
	result, err := fixtureStore(t, f).QueryStats(context.Background(), StatsQuery{Start: 10, End: 20})
	if err != nil {
		t.Fatal(err)
	}
	var services []string
	for _, summary := range result.Summaries {
		services = append(services, summary.Service)
	}
	if !reflect.DeepEqual(services, []string{"a", "b", "z", "", "healthy"}) {
		t.Fatalf("order = %v", services)
	}
	a := result.Summaries[0]
	if a.Total != 7 || a.ErrorCount != 2 || a.ErrorRate != 0.2857 || a.ByLevel["TRACE"] != 1 {
		t.Fatalf("summary = %+v", a)
	}
	if _, ok := a.ByLevel["DEBUG"]; !ok {
		t.Fatal("missing zero level")
	}
	if result.Summaries[3].Total != 2 || len(result.Notices) != 1 {
		t.Fatalf("missing service = %+v", result)
	}
	if f.calls != 1 || !strings.Contains(f.query, "GROUP BY service, level") || strings.Contains(f.query, "service = ?") {
		t.Fatalf("query = %s", f.query)
	}
	if !reflect.DeepEqual(boundValues(f), []any{int64(10000), int64(20000)}) {
		t.Fatal(boundValues(f))
	}
}

func TestStatsFiltersAndEmpty(t *testing.T) {
	f := &queryFixture{columns: []string{"service", "level", "count"}}
	result, err := fixtureStore(t, f).QueryStats(context.Background(), StatsQuery{Service: "user", Route: "/users", Method: "GET", Start: 10, End: 20})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summaries == nil || len(result.Summaries) != 0 {
		t.Fatalf("empty = %+v", result)
	}
	if !strings.Contains(f.query, "service = ? AND route = ? AND method = ?") {
		t.Fatal(f.query)
	}
	if !reflect.DeepEqual(boundValues(f), []any{int64(10000), int64(20000), "user", "/users", "GET"}) {
		t.Fatal(boundValues(f))
	}
}

func TestTemplatesTruncationBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 2, 3} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			f := &queryFixture{columns: []string{"template", "level", "cnt", "first", "last", "ts", "trace", "route", "method", "attrs"}}
			for i := 0; i < count; i++ {
				f.rows = append(f.rows, []driver.Value{"template", "ERROR", int64(100), int64(10000), int64(20000), int64(20000), "trace", "/users", "PUT", nil})
			}
			result, err := fixtureStore(t, f).QueryTemplates(context.Background(), TemplatesQuery{Service: " user ", Start: 10, End: 20, Limit: 2, Route: "/users", Method: "PUT", Level: "ERROR"})
			if err != nil {
				t.Fatal(err)
			}
			if result.Items == nil || len(result.Items) != min(count, 2) || result.HasMore != (count > 2) {
				t.Fatalf("count=%d result=%+v", count, result)
			}
			if count > 0 {
				item := result.Items[0]
				if item.Count != 100 || item.FirstSeen != 10 || item.LastSeen != 20 || item.Sample.Ts != 20 || string(item.Sample.Attrs) != "{}" {
					t.Fatalf("item=%+v", item)
				}
			}
			if !reflect.DeepEqual(boundValues(f), []any{int64(10000), int64(20000), "user", "/users", "PUT", "ERROR", int64(3)}) {
				t.Fatal(boundValues(f))
			}
			if !strings.Contains(f.query, "ORDER BY cnt DESC, template ASC, level ASC") || !strings.Contains(f.query, "service = ?") {
				t.Fatal(f.query)
			}
		})
	}
}

func TestTemplatesRejectInvalidScope(t *testing.T) {
	f := &queryFixture{}
	store := fixtureStore(t, f)
	for _, q := range []TemplatesQuery{{Service: " ", Limit: 2}, {Service: "user", Limit: 0}, {Service: "user", Limit: 501}} {
		if _, err := store.QueryTemplates(context.Background(), q); err == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid queries reached database")
	}
}

func TestLogQueriesDoNotHideDatabaseErrors(t *testing.T) {
	f := &queryFixture{err: errors.New("database unavailable")}
	store := fixtureStore(t, f)
	if _, err := store.QueryStats(context.Background(), StatsQuery{}); err == nil {
		t.Fatal("stats swallowed error")
	}
	if _, err := store.QueryTemplates(context.Background(), TemplatesQuery{Service: "user", Limit: 2}); err == nil {
		t.Fatal("templates swallowed error")
	}
}
