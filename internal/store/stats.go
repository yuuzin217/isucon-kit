package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

// StatQuery はランキングの並び、検索、ページである。
type StatQuery struct {
	Sort   string
	Desc   bool
	Search string
	Limit  int
	Offset int
}

// SearchEndpoints は run 全体から条件に合う API を返す。合計時間の上位だけに絞らない。
func (s *Store) SearchEndpoints(runID string, q StatQuery) ([]model.EndpointStat, int, error) {
	order, err := statOrder(q, "method ASC, route ASC")
	if err != nil {
		return nil, 0, err
	}
	where, args := statWhere(runID, "route", q.Search)
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM endpoint_stats WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT method, route, count, total_seconds, avg_seconds, max_seconds, p95_seconds
		FROM endpoint_stats WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append([]any{}, args...), q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []model.EndpointStat
	for rows.Next() {
		var ep model.EndpointStat
		var p95 sql.NullFloat64
		if err := rows.Scan(&ep.Method, &ep.Route, &ep.Count, &ep.TotalSeconds, &ep.AvgSeconds, &ep.MaxSeconds, &p95); err != nil {
			return nil, 0, err
		}
		if p95.Valid {
			v := p95.Float64
			ep.P95Seconds = &v
		}
		out = append(out, ep)
	}
	return out, total, rows.Err()
}

// SearchQueries は run 全体から条件に合う SQL を返す。
func (s *Store) SearchQueries(runID string, q StatQuery) ([]model.QueryStat, int, error) {
	order, err := statOrder(q, "fingerprint ASC")
	if err != nil {
		return nil, 0, err
	}
	where, args := statWhere(runID, "fingerprint", q.Search)
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM query_stats WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT fingerprint, count, total_seconds, avg_seconds, max_seconds
		FROM query_stats WHERE `+where+` ORDER BY `+order+` LIMIT ? OFFSET ?`, append(append([]any{}, args...), q.Limit, q.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []model.QueryStat
	for rows.Next() {
		var item model.QueryStat
		if err := rows.Scan(&item.Fingerprint, &item.Count, &item.TotalSeconds, &item.AvgSeconds, &item.MaxSeconds); err != nil {
			return nil, 0, err
		}
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func statOrder(q StatQuery, tie string) (string, error) {
	col := map[string]string{"total": "total_seconds", "avg": "avg_seconds", "count": "count"}[q.Sort]
	if col == "" {
		return "", fmt.Errorf("並び替えが不正です")
	}
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	return col + " " + dir + ", count DESC, " + tie, nil
}

func statWhere(runID, column, search string) (string, []any) {
	where := "run_id = ?"
	args := []any{runID}
	search = strings.TrimSpace(search)
	if search != "" {
		where += " AND " + column + " LIKE ? ESCAPE '\\'"
		args = append(args, likeContains(search))
	}
	return where, args
}

func likeContains(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return "%" + s + "%"
}
