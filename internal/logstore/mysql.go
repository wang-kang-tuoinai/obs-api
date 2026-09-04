package logstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	if q.Level != "" {
		conds = append(conds, "level = ?")
		args = append(args, q.Level)
	}
	where := strings.Join(conds, " AND ")
	// 第一条：按 level 分组
	levelSQL := "SELECT level, COUNT(*) FROM logs WHERE " + where + " GROUP BY level"
	byLevel := make(map[string]int64)
	rows, err := s.db.QueryContext(ctx, levelSQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var level string
		var count int64
		// 注意：Scan 的参数顺序必须和 SELECT 列的顺序一致
		if err := rows.Scan(&level, &count); err != nil {
			return nil, err
		}
		byLevel[level] = count
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 复制并添加元素，避免修改原切片的底层数组
	tplArgs := make([]any, len(args), len(args)+1)
	copy(tplArgs, args)
	tplArgs = append(tplArgs, q.TopN)
	tplSQL := "SELECT template, level, COUNT(*) c FROM logs WHERE " + where +
		" GROUP BY template, level ORDER BY c DESC LIMIT ?"
	var template_count []TemplateItem
	rows2, err := s.db.QueryContext(ctx, tplSQL, tplArgs...)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var tc TemplateItem
		if err := rows2.Scan(&tc.Template, &tc.Level, &tc.Count); err != nil {
			return nil, err
		}
		template_count = append(template_count, tc)
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}
	return &StatsResult{ByLevel: byLevel, TopTemplates: template_count}, nil
}

func (s *MysqlStore) QueryTemplates(ctx context.Context, q TemplatesQuery) ([]TemplateStat, error) {
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
ORDER BY cnt DESC
LIMIT ?`

	args = append(args, q.Limit)

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
	return items, nil
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
