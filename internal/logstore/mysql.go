package logstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

type MysqlStore struct {
	db *sql.DB
}

func NewMysqlStore(db *sql.DB) *MysqlStore {
	return &MysqlStore{
		db: db,
	}
}

func (s *MysqlStore) QueryStats(ctx context.Context, q StatsQuery) (*StatsResult, error) {
	conds := []string{"ts BETWEEN ? AND ?"}
	// 数据库里是毫秒而参数是秒，所以需要转换
	args := []any{q.Start * 1000, q.End * 1000}
	// 可选参数判断是否为空
	if q.Service != "" {
		conds = append(conds, "service = ?")
		args = append(args, q.Service)
	}
	if q.Route != "" {
		conds = append(conds, "route = ?")
		args = append(args, q.Route)
	}
	if q.Method != "" {
		conds = append(conds, "method = ?")
		args = append(args, q.Method)
	}
	where := strings.Join(conds, " AND ")
	// 一次分组查询生成各服务统计，不再查询模板。
	levelSQL := "SELECT COALESCE(service, ''), level, COUNT(*) FROM logs WHERE " + where + " GROUP BY service, level"
	byService := make(map[string]*StatsSummary)
	rows, err := s.db.QueryContext(ctx, levelSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var service, level string
		var count int64
		// 注意：Scan 的参数顺序必须和 SELECT 列的顺序一致
		if err := rows.Scan(&service, &level, &count); err != nil {
			return nil, err
		}
		if strings.TrimSpace(service) == "" {
			service = ""
		}
		summary := byService[service]
		if summary == nil {
			summary = &StatsSummary{Service: service, ByLevel: map[string]int64{"DEBUG": 0, "INFO": 0, "WARN": 0, "ERROR": 0}}
			byService[service] = summary
		}
		summary.ByLevel[level] += count
		summary.Total += count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result := &StatsResult{Summaries: make([]StatsSummary, 0, len(byService))}
	for _, summary := range byService {
		summary.ErrorCount = summary.ByLevel[LevelError]
		if summary.Total > 0 {
			summary.ErrorRate = math.Round(float64(summary.ErrorCount)/float64(summary.Total)*10000) / 10000
		}
		result.Summaries = append(result.Summaries, *summary)
		if summary.Service == "" {
			result.Notices = append(result.Notices, "存在缺少 service 的日志，已归入 service 为空的分组；无法确定服务归属")
		}
	}
	sort.Slice(result.Summaries, func(i, j int) bool {
		a, b := result.Summaries[i], result.Summaries[j]
		if a.ErrorCount != b.ErrorCount {
			return a.ErrorCount > b.ErrorCount
		}
		if a.ByLevel["WARN"] != b.ByLevel["WARN"] {
			return a.ByLevel["WARN"] > b.ByLevel["WARN"]
		}
		return a.Service < b.Service
	})
	return result, nil
}

func (s *MysqlStore) QueryTemplates(ctx context.Context, q TemplatesQuery) (*TemplatesResult, error) {
	q.Service = strings.TrimSpace(q.Service)
	if q.Service == "" {
		return nil, fmt.Errorf("service 不能为空")
	}
	if q.Limit < 1 || q.Limit > 500 {
		return nil, fmt.Errorf("limit 必须在 1–500 之间")
	}
	conds := []string{"ts BETWEEN ? AND ?"}
	// 数据库里是毫秒而参数是秒，所以需要转换
	args := []any{q.Start * 1000, q.End * 1000}
	if q.Service != "" {
		conds = append(conds, "service = ?")
		args = append(args, q.Service)
	}
	if q.Route != "" {
		conds = append(conds, "route = ?")
		args = append(args, q.Route)
	}
	if q.Method != "" {
		conds = append(conds, "method = ?")
		args = append(args, q.Method)
	}
	if q.Level != "" {
		conds = append(conds, "level = ?")
		args = append(args, q.Level)
	}
	where := strings.Join(conds, " AND ")

	// 窗口函数：按 (template, level) 分组，每组取最新一条（rn=1），带聚合计数与首末时间
	sqlStr := `SELECT template, level, cnt, first_seen, last_seen,
       sample_ts, sample_trace_id, sample_route, sample_method, sample_attrs
FROM (
	SELECT template, level,
		COUNT(*)      OVER (PARTITION BY template, level) AS cnt,
		MIN(ts)       OVER (PARTITION BY template, level) AS first_seen,
		MAX(ts)       OVER (PARTITION BY template, level) AS last_seen,
		ts        AS sample_ts,
		trace_id  AS sample_trace_id,
		route     AS sample_route,
		method    AS sample_method,
		attrs     AS sample_attrs,
		ROW_NUMBER() OVER (PARTITION BY template, level ORDER BY ts DESC, id DESC) AS rn
	FROM logs
	WHERE ` + where + `
) t
WHERE rn = 1
ORDER BY cnt DESC, template ASC, level ASC
LIMIT ?`

	args = append(args, q.Limit+1)

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]TemplateStat, 0)
	for rows.Next() {
		var (
			template    string
			level       string
			count       int64
			firstSeen   int64
			lastSeen    int64
			sampleTs    int64
			sampleTID   string
			sampleRoute string
			sampleMeth  string
			attrs       []byte
		)
		if err := rows.Scan(&template, &level, &count, &firstSeen, &lastSeen, &sampleTs, &sampleTID, &sampleRoute, &sampleMeth, &attrs); err != nil {
			return nil, err
		}
		// attrs 可能为 NULL，兜底成空对象
		attrsJSON := json.RawMessage(attrs)
		if len(attrs) == 0 {
			attrsJSON = json.RawMessage("{}")
		}
		items = append(items, TemplateStat{
			Template:  template,
			Level:     level,
			Count:     count,
			FirstSeen: firstSeen / 1000, // 毫秒 → 秒
			LastSeen:  lastSeen / 1000,
			Sample: TemplateSample{
				Ts:      sampleTs / 1000,
				TraceID: sampleTID,
				Route:   sampleRoute,
				Method:  sampleMeth,
				Attrs:   attrsJSON,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	hasMore := len(items) > q.Limit
	if hasMore {
		items = items[:q.Limit]
	}
	return &TemplatesResult{Items: items, HasMore: hasMore}, nil
}

func (s *MysqlStore) QuerySearch(ctx context.Context, q SearchQuery) (*SearchResult, error) {
	conds := []string{"ts BETWEEN ? AND ?"}
	// 数据库里是毫秒而参数是秒，所以需要转换
	args := []any{q.Start * 1000, q.End * 1000}
	if q.Service != "" {
		conds = append(conds, "service = ?")
		args = append(args, q.Service)
	}
	if q.Route != "" {
		conds = append(conds, "route = ?")
		args = append(args, q.Route)
	}
	if q.Method != "" {
		conds = append(conds, "method = ?")
		args = append(args, q.Method)
	}
	if q.Level != "" {
		conds = append(conds, "level = ?")
		args = append(args, q.Level)
	}
	if q.TraceID != "" {
		conds = append(conds, "trace_id = ?")
		args = append(args, q.TraceID)
	}
	if q.Template != "" {
		conds = append(conds, "template = ?")
		args = append(args, q.Template)
	}
	if q.Keyword != "" {
		// 模糊子串：搜模板字符串 + attrs 文本，须配合时间窗 + limit
		conds = append(conds, "(template LIKE ? OR CAST(attrs AS CHAR) LIKE ?)")
		kw := "%" + q.Keyword + "%"
		args = append(args, kw, kw)
	}
	// 游标分页（keyset）：取游标之后更旧的日志
	if q.CursorTs > 0 {
		conds = append(conds, "(ts < ? OR (ts = ? AND id < ?))")
		args = append(args, q.CursorTs, q.CursorTs, q.CursorID)
	}
	where := strings.Join(conds, " AND ")

	sqlStr := "SELECT ts, level, service, route, method, template, attrs, trace_id, id FROM logs WHERE " +
		where + " ORDER BY ts DESC, id DESC LIMIT ?"
	args = append(args, q.Limit+1) // 多取一条判断是否还有下一页

	rows, err := s.db.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := make([]LogItem, 0, q.Limit)
	var (
		lastTs int64
		lastID uint64
		count  int
	)
	for rows.Next() {
		var (
			ts       int64
			level    string
			service  string
			route    string
			method   string
			template string
			attrs    []byte
			traceID  string
			id       uint64
		)
		if err := rows.Scan(&ts, &level, &service, &route, &method, &template, &attrs, &traceID, &id); err != nil {
			return nil, err
		}
		count++
		if count > q.Limit {
			// 多取的那条只用于判断 has_more，不返回
			continue
		}
		attrsJSON := json.RawMessage(attrs)
		if len(attrs) == 0 {
			attrsJSON = json.RawMessage("{}")
		}
		items = append(items, LogItem{
			Ts:       ts / 1000, // 毫秒 → 秒
			Level:    level,
			Service:  service,
			Route:    route,
			Method:   method,
			Template: template,
			Attrs:    attrsJSON,
			TraceID:  traceID,
		})
		lastTs = ts
		lastID = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	hasMore := count > q.Limit
	var nextCursor *string
	if hasMore {
		c := fmt.Sprintf("%d:%d", lastTs, lastID)
		nextCursor = &c
	}
	return &SearchResult{Items: items, NextCursor: nextCursor, HasMore: hasMore}, nil
}
