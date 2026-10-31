// Package pgstat reads PostgreSQL 14+ cumulative execution statistics.
package pgstat

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

// Query never executes captured statements, creates extensions, or resets counters.
// Only the connected database is collected, rather than every database on the server.
const Query = `SELECT json_build_object(
 'version', 1, 'database', current_database(), 'captured_at', clock_timestamp(),
 'stats_reset', (SELECT stats_reset FROM pg_stat_statements_info),
 'hidden_queries', (SELECT count(*) FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND calls > 0 AND (query IS NULL OR queryid IS NULL OR query = '<insufficient privilege>')),
 'queries', COALESCE((SELECT json_agg(row_to_json(s)) FROM (
 SELECT query, calls, total_exec_time, max_exec_time FROM pg_stat_statements
 WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
 AND calls > 0 AND query IS NOT NULL AND queryid IS NOT NULL
 AND query NOT ILIKE '%pg_stat_statements%'
 ) s), '[]'::json))`

type Row struct {
	Query   string  `json:"query"`
	Calls   int     `json:"calls"`
	TotalMS float64 `json:"total_exec_time"`
	MaxMS   float64 `json:"max_exec_time"`
}

type Snapshot struct {
	Version       int       `json:"version"`
	Database      string    `json:"database"`
	CapturedAt    time.Time `json:"captured_at"`
	StatsReset    time.Time `json:"stats_reset"`
	HiddenQueries int       `json:"hidden_queries"`
	Queries       []Row     `json:"queries"`
}

// Decode validates a complete snapshot and removes literals/comments before storage.
func Decode(body []byte) (*Snapshot, error) {
	var s Snapshot
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, fmt.Errorf("PostgreSQL統計JSONが不正です: %w", err)
	}
	if s.Version != 1 || s.Database == "" || s.CapturedAt.IsZero() || s.StatsReset.IsZero() || s.Queries == nil || s.StatsReset.After(s.CapturedAt) {
		return nil, fmt.Errorf("PostgreSQL統計の形式・集計範囲が不正です（PostgreSQL 14以降が必要です）")
	}
	if s.HiddenQueries != 0 {
		return nil, fmt.Errorf("SQL本文を参照できません。収集用DBユーザーのpg_read_all_stats権限を確認してください")
	}
	for i := range s.Queries {
		r := &s.Queries[i]
		if strings.TrimSpace(r.Query) == "" || r.Calls <= 0 || !finite(r.TotalMS) || !finite(r.MaxMS) || r.MaxMS > r.TotalMS+0.001 {
			return nil, fmt.Errorf("PostgreSQL統計の行%dが不正です", i+1)
		}
		r.Query = Fingerprint(r.Query)
		if r.Query == "" {
			return nil, fmt.Errorf("PostgreSQL統計の行%dにSQLがありません", i+1)
		}
	}
	return &s, nil
}

func finite(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

func (s *Snapshot) Stats() []model.QueryStat {
	var out []model.QueryStat
	for _, r := range s.Queries {
		out = append(out, model.QueryStat{Fingerprint: r.Query, Count: r.Calls, TotalSeconds: r.TotalMS / 1000, AvgSeconds: r.TotalMS / 1000 / float64(r.Calls), MaxSeconds: r.MaxMS / 1000})
	}
	return out
}

func (s *Snapshot) Scope(host string) string {
	return fmt.Sprintf("PostgreSQL %s/%s: %s〜%s（統計リセット以降の累積実行時間、ベンチ1回分ではありません）", host, s.Database, s.StatsReset.UTC().Format(time.RFC3339), s.CapturedAt.UTC().Format(time.RFC3339))
}

// Fingerprint defensively strips literals and comments: pg_stat_statements can
// retain constants in utility statements and after entry eviction. SQL is never executed.
func Fingerprint(q string) string {
	var b strings.Builder
	for i := 0; i < len(q); {
		switch {
		case strings.HasPrefix(q[i:], "--"):
			j := strings.IndexByte(q[i:], '\n')
			if j < 0 {
				i = len(q)
			} else {
				i += j + 1
			}
			b.WriteByte(' ')
		case strings.HasPrefix(q[i:], "/*"):
			i += 2
			depth := 1
			for i < len(q) && depth > 0 {
				if strings.HasPrefix(q[i:], "/*") {
					depth++
					i += 2
				} else if strings.HasPrefix(q[i:], "*/") {
					depth--
					i += 2
				} else {
					i++
				}
			}
			b.WriteByte(' ')
		case q[i] == '\'':
			escaped := i > 0 && (q[i-1] == 'e' || q[i-1] == 'E') && (i < 2 || !word(q[i-2]))
			i++
			for i < len(q) {
				if q[i] == '\\' {
					// With unknown standard_conforming_strings, discard the suffix
					// rather than risk treating literal text as an identifier.
					if !escaped {
						i = len(q)
						break
					}
					i += 2
					continue
				}
				if q[i] == '\'' {
					i++
					if i < len(q) && q[i] == '\'' {
						i++
						continue
					}
					break
				}
				i++
			}
			b.WriteByte('?')
		case q[i] == '"':
			start := i
			i++
			for i < len(q) {
				if q[i] == '"' {
					i++
					if i < len(q) && q[i] == '"' {
						i++
						continue
					}
					break
				}
				i++
			}
			b.WriteString(q[start:i])
		case q[i] == '$':
			j := i + 1
			if j < len(q) && q[j] >= '0' && q[j] <= '9' {
				for j < len(q) && q[j] >= '0' && q[j] <= '9' {
					j++
				}
				b.WriteString(q[i:j])
				i = j
				continue
			}
			for j < len(q) && word(q[j]) {
				j++
			}
			if j < len(q) && q[j] == '$' {
				tag := q[i : j+1]
				end := strings.Index(q[j+1:], tag)
				if end < 0 {
					i = len(q)
				} else {
					i = j + 1 + end + len(tag)
				}
				b.WriteByte('?')
			} else {
				b.WriteByte(q[i])
				i++
			}
		case word(q[i]) && !(q[i] >= '0' && q[i] <= '9'):
			j := i + 1
			for j < len(q) && (word(q[j]) || q[j] == '$') {
				j++
			}
			b.WriteString(q[i:j])
			i = j
		case q[i] >= '0' && q[i] <= '9' || q[i] == '.' && i+1 < len(q) && q[i+1] >= '0' && q[i+1] <= '9':
			i++
			for i < len(q) && (word(q[i]) || q[i] == '.' || (q[i] == '+' || q[i] == '-') && (q[i-1] == 'e' || q[i-1] == 'E')) {
				i++
			}
			b.WriteByte('?')
		default:
			b.WriteByte(q[i])
			i++
		}
	}
	return strings.TrimSpace(b.String())
}

func word(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c >= 128
}
