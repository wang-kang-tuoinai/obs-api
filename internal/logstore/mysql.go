package logstore

import (
	"context"
	"database/sql"
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
	rows2, err := s.db.QueryContext(ctx, tplSQL, append(args, q.TopN)...)
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
