// Package store は SQLite へ履歴と集計だけを保存する。生ログは保存しない。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"

	_ "modernc.org/sqlite"
)

const timeLayout = time.RFC3339

const runColumns = `id, created_at, label, git_sha, note, score, benchmark_status, collection_status, analysis_status,
		timing_basis, collection_scope, tool_versions, access_analysis, slow_analysis, analysis_error, mode`

// ErrNotFound は run が存在しない。
var ErrNotFound = errors.New("run が見つかりません")

// Store は1つの SQLite ファイルを扱う。
type Store struct {
	db   *sql.DB
	path string
}

// Open は dataDir 以下に DB を作り、スキーマを用意する。
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("データディレクトリを作成できません: %w", err)
	}
	if err := os.Chmod(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("データディレクトリの権限を 0700 にできません: %w", err)
	}
	path := filepath.Join(dataDir, "isucon-kit.db")
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("SQLite を開けません: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s.tighten()
	return s, nil
}

// Close は接続を閉じる。
func (s *Store) Close() error { return s.db.Close() }

// Ping は DB が応答するかを確認する。
func (s *Store) Ping() error { return s.db.Ping() }

// Path は DB ファイルのパスである。API へは出さない。
func (s *Store) Path() string { return s.path }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			created_at TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT '',
			git_sha TEXT,
			note TEXT NOT NULL DEFAULT '',
			score INTEGER,
			benchmark_status TEXT NOT NULL,
			collection_status TEXT NOT NULL,
			analysis_status TEXT NOT NULL,
			timing_basis TEXT NOT NULL DEFAULT '',
			collection_scope TEXT NOT NULL DEFAULT '',
			tool_versions TEXT NOT NULL DEFAULT '',
			access_analysis TEXT NOT NULL,
			slow_analysis TEXT NOT NULL,
			analysis_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS sources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id),
			host_name TEXT NOT NULL,
			kind TEXT NOT NULL,
			format TEXT NOT NULL,
			remote_path TEXT NOT NULL,
			snapshot_rel TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			size_bytes INTEGER NOT NULL DEFAULT 0,
			original_size INTEGER NOT NULL DEFAULT 0,
			truncated INTEGER NOT NULL DEFAULT 0,
			collected_at TEXT NOT NULL,
			analysis_status TEXT NOT NULL,
			analysis_error TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS endpoint_stats (
			run_id TEXT NOT NULL REFERENCES runs(id),
			method TEXT NOT NULL,
			route TEXT NOT NULL,
			count INTEGER NOT NULL,
			total_seconds REAL NOT NULL,
			avg_seconds REAL NOT NULL,
			max_seconds REAL NOT NULL,
			p95_seconds REAL,
			PRIMARY KEY (run_id, method, route)
		)`,
		`CREATE TABLE IF NOT EXISTS query_stats (
			run_id TEXT NOT NULL REFERENCES runs(id),
			fingerprint TEXT NOT NULL,
			count INTEGER NOT NULL,
			total_seconds REAL NOT NULL,
			avg_seconds REAL NOT NULL,
			max_seconds REAL NOT NULL,
			PRIMARY KEY (run_id, fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_created ON runs(created_at, id)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_run ON sources(run_id)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("スキーマの作成に失敗しました: %w", err)
		}
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.db.Exec(`INSERT INTO schema_version(version) VALUES (1)`); err != nil {
			return err
		}
	}
	var v int
	if err := s.db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&v); err != nil {
		return err
	}
	if v > 2 {
		return fmt.Errorf("未対応の schema_version %d です", v)
	}
	if v < 2 {
		if err := s.upgradeV2(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) upgradeV2() error {
	if !s.hasColumn("runs", "mode") {
		if _, err := s.db.Exec(`ALTER TABLE runs ADD COLUMN mode TEXT NOT NULL DEFAULT 'legacy'`); err != nil {
			return fmt.Errorf("runs.mode の追加に失敗しました: %w", err)
		}
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS executions (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			mode TEXT NOT NULL,
			status TEXT NOT NULL,
			step TEXT NOT NULL DEFAULT '',
			started_at TEXT,
			finished_at TEXT,
			error TEXT NOT NULL DEFAULT '',
			log TEXT NOT NULL DEFAULT '',
			run_id TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`UPDATE schema_version SET version = 2`)
	return err
}

func (s *Store) hasColumn(table, column string) bool {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == column {
			return true
		}
	}
	return false
}

func (s *Store) tighten() {
	_ = os.Chmod(s.path, 0o600)
	_ = os.Chmod(s.path+"-wal", 0o600)
	_ = os.Chmod(s.path+"-shm", 0o600)
}

// CreateRun は run と収集元を1トランザクションで保存する。
func (s *Store) CreateRun(run model.Run, sources []model.Source) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	mode := run.Mode
	if mode == "" {
		mode = model.ModeLegacy
	}
	_, err = tx.Exec(`INSERT INTO runs(
		id, created_at, label, git_sha, note, score, benchmark_status, collection_status, analysis_status,
		timing_basis, collection_scope, tool_versions, access_analysis, slow_analysis, analysis_error, mode
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		run.ID, run.CreatedAt.UTC().Format(timeLayout), run.Label, nullString(run.GitSHA), run.Note, nullInt(run.Score),
		run.BenchmarkStatus, run.CollectionStatus, run.AnalysisStatus, run.TimingBasis, run.CollectionScope, run.ToolVersions,
		run.AccessAnalysis, run.SlowAnalysis, run.AnalysisError, mode,
	)
	if err != nil {
		return fmt.Errorf("run の保存に失敗しました: %w", err)
	}
	for _, src := range sources {
		trunc := 0
		if src.Truncated {
			trunc = 1
		}
		_, err = tx.Exec(`INSERT INTO sources(
			run_id, host_name, kind, format, remote_path, snapshot_rel, status, error, size_bytes, original_size,
			truncated, collected_at, analysis_status, analysis_error
		) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			run.ID, src.HostName, src.Kind, src.Format, src.RemotePath, src.SnapshotRel, src.Status, src.Error,
			src.SizeBytes, src.OriginalSize, trunc, src.CollectedAt.UTC().Format(timeLayout), src.AnalysisStatus, src.AnalysisError,
		)
		if err != nil {
			return fmt.Errorf("収集元の保存に失敗しました: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.tighten()
	return nil
}

// GetRun は ID で run を読む。
func (s *Store) GetRun(id string) (model.Run, error) {
	row := s.db.QueryRow(`SELECT `+runColumns+` FROM runs WHERE id = ?`, id)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Run{}, ErrNotFound
	}
	return run, err
}

// LatestRun は最も新しい run を返す。
func (s *Store) LatestRun() (model.Run, error) {
	row := s.db.QueryRow(`SELECT ` + runColumns + ` FROM runs ORDER BY created_at DESC, id DESC LIMIT 1`)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Run{}, ErrNotFound
	}
	return run, err
}

// ListRuns は新しい順に最大 limit 件返す。
func (s *Store) ListRuns(limit int) ([]model.Run, error) {
	rows, err := s.db.Query(`SELECT `+runColumns+` FROM runs ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// Sources は run の収集元を ID 順で返す。
func (s *Store) Sources(runID string) ([]model.Source, error) {
	rows, err := s.db.Query(`SELECT id, run_id, host_name, kind, format, remote_path, snapshot_rel, status, error,
		size_bytes, original_size, truncated, collected_at, analysis_status, analysis_error
		FROM sources WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Source
	for rows.Next() {
		var src model.Source
		var trunc int
		var collected string
		if err := rows.Scan(&src.ID, &src.RunID, &src.HostName, &src.Kind, &src.Format, &src.RemotePath, &src.SnapshotRel,
			&src.Status, &src.Error, &src.SizeBytes, &src.OriginalSize, &trunc, &collected, &src.AnalysisStatus, &src.AnalysisError); err != nil {
			return nil, err
		}
		src.Truncated = trunc != 0
		t, err := time.Parse(timeLayout, collected)
		if err != nil {
			return nil, err
		}
		src.CollectedAt = t
		out = append(out, src)
	}
	return out, rows.Err()
}

// SetScore はスコア・判定・メモ・git SHA を置き換える。未指定のスコアは NULL。
func (s *Store) SetScore(runID string, score *int, status, note string, gitSHA *string) error {
	res, err := s.db.Exec(`UPDATE runs SET score = ?, benchmark_status = ?, note = ?, git_sha = ? WHERE id = ?`,
		nullInt(score), status, note, nullString(gitSHA), runID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	s.tighten()
	return nil
}

// AnalysisUpdate は解析結果の置換内容である。
type AnalysisUpdate struct {
	AnalysisStatus string
	AccessAnalysis string
	SlowAnalysis   string
	AnalysisError  string
	TimingBasis    string
	ToolVersions   string
	Sources        []model.Source
	Endpoints      []model.EndpointStat
	Queries        []model.QueryStat
}

// ReplaceAnalysis は同一 run の集計をトランザクションで置き換え、再実行でも二重加算しない。
func (s *Store) ReplaceAnalysis(runID string, upd AnalysisUpdate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`UPDATE runs SET analysis_status = ?, access_analysis = ?, slow_analysis = ?, analysis_error = ?,
		timing_basis = ?, tool_versions = ? WHERE id = ?`,
		upd.AnalysisStatus, upd.AccessAnalysis, upd.SlowAnalysis, upd.AnalysisError, upd.TimingBasis, upd.ToolVersions, runID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM endpoint_stats WHERE run_id = ?`, runID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM query_stats WHERE run_id = ?`, runID); err != nil {
		return err
	}
	for _, src := range upd.Sources {
		if _, err := tx.Exec(`UPDATE sources SET analysis_status = ?, analysis_error = ? WHERE id = ? AND run_id = ?`,
			src.AnalysisStatus, src.AnalysisError, src.ID, runID); err != nil {
			return err
		}
	}
	for _, ep := range upd.Endpoints {
		if _, err := tx.Exec(`INSERT INTO endpoint_stats(run_id, method, route, count, total_seconds, avg_seconds, max_seconds, p95_seconds)
			VALUES (?,?,?,?,?,?,?,?)`,
			runID, ep.Method, ep.Route, ep.Count, ep.TotalSeconds, ep.AvgSeconds, ep.MaxSeconds, nullFloat(ep.P95Seconds)); err != nil {
			return err
		}
	}
	for _, q := range upd.Queries {
		if _, err := tx.Exec(`INSERT INTO query_stats(run_id, fingerprint, count, total_seconds, avg_seconds, max_seconds)
			VALUES (?,?,?,?,?,?)`,
			runID, q.Fingerprint, q.Count, q.TotalSeconds, q.AvgSeconds, q.MaxSeconds); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.tighten()
	return nil
}

// Endpoints は累積秒数の降順で最大 limit 件返す。
func (s *Store) Endpoints(runID string, limit int) ([]model.EndpointStat, error) {
	rows, err := s.db.Query(`SELECT method, route, count, total_seconds, avg_seconds, max_seconds, p95_seconds
		FROM endpoint_stats WHERE run_id = ?
		ORDER BY total_seconds DESC, count DESC, method ASC, route ASC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.EndpointStat
	for rows.Next() {
		var ep model.EndpointStat
		var p95 sql.NullFloat64
		if err := rows.Scan(&ep.Method, &ep.Route, &ep.Count, &ep.TotalSeconds, &ep.AvgSeconds, &ep.MaxSeconds, &p95); err != nil {
			return nil, err
		}
		if p95.Valid {
			v := p95.Float64
			ep.P95Seconds = &v
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}

// Queries は累積秒数の降順で最大 limit 件返す。
func (s *Store) Queries(runID string, limit int) ([]model.QueryStat, error) {
	rows, err := s.db.Query(`SELECT fingerprint, count, total_seconds, avg_seconds, max_seconds
		FROM query_stats WHERE run_id = ?
		ORDER BY total_seconds DESC, count DESC, fingerprint ASC LIMIT ?`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.QueryStat
	for rows.Next() {
		var q model.QueryStat
		if err := rows.Scan(&q.Fingerprint, &q.Count, &q.TotalSeconds, &q.AvgSeconds, &q.MaxSeconds); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (model.Run, error) {
	var run model.Run
	var created string
	var git sql.NullString
	var score sql.NullInt64
	err := row.Scan(&run.ID, &created, &run.Label, &git, &run.Note, &score, &run.BenchmarkStatus, &run.CollectionStatus,
		&run.AnalysisStatus, &run.TimingBasis, &run.CollectionScope, &run.ToolVersions, &run.AccessAnalysis, &run.SlowAnalysis, &run.AnalysisError, &run.Mode)
	if err != nil {
		return model.Run{}, err
	}
	t, err := time.Parse(timeLayout, created)
	if err != nil {
		return model.Run{}, err
	}
	run.CreatedAt = t
	if git.Valid {
		v := git.String
		run.GitSHA = &v
	}
	if score.Valid {
		v := int(score.Int64)
		run.Score = &v
	}
	return run, nil
}

func nullString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullFloat(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
