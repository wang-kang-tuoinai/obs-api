package logstore

import (
	"context"
	"database/sql"
	"encoding/json"
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
	if q.Level != "" {
		conds = append(conds, "level = ?")
		args = append(args, q.Level)
	}
	where := strings.Join(conds, " AND ")

	// 窗口函数：每个 template 取最新一条（rn=1），带聚合计数与首末时间
	sqlStr := `SELECT template, cnt, first_seen, last_seen, sample_ts, sample_trace_id, sample_attrs
FROM (
	SELECT template,
		COUNT(*) OVER (PARTITION BY template) AS cnt,
		MIN(ts) OVER (PARTITION BY template) AS first_seen,
		MAX(ts) OVER (PARTITION BY template) AS last_seen,
		ts AS sample_ts,
		trace_id AS sample_trace_id,
		attrs AS sample_attrs,
		ROW_NUMBER() OVER (PARTITION BY template ORDER BY ts DESC, id DESC) AS rn
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
			template  string
			count     int64
			firstSeen int64
			lastSeen  int64
			sampleTs  int64
			sampleTID string
			attrs     []byte
		)
		if err := rows.Scan(&template, &count, &firstSeen, &lastSeen, &sampleTs, &sampleTID, &attrs); err != nil {
			return nil, err
		}
		// attrs 可能为 NULL，兜底成空对象
		attrsJSON := json.RawMessage(attrs)
		if len(attrs) == 0 {
			attrsJSON = json.RawMessage("{}")
		}
		items = append(items, TemplateStat{
			Template:  template,
			Count:     count,
			FirstSeen: firstSeen / 1000, // 毫秒 → 秒
			LastSeen:  lastSeen / 1000,
			Sample: TemplateSample{
				Ts:      sampleTs / 1000,
				TraceID: sampleTID,
				Attrs:   attrsJSON,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
