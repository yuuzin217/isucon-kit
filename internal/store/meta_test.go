package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

func TestUpgradeV2KeepsOldRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "isucon-kit.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version(version) VALUES (1);
		CREATE TABLE runs (
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
		);
		INSERT INTO runs(id, created_at, label, benchmark_status, collection_status, analysis_status, access_analysis, slow_analysis, score)
		VALUES ('old-run', '2026-10-01T00:00:00Z', 'before-mode', 'pass', 'ok', 'ok', 'ok', 'ok', 10);
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	run, err := st.GetRun("old-run")
	if err != nil {
		t.Fatal(err)
	}
	if run.Mode != model.ModeLegacy || run.Score == nil || *run.Score != 10 {
		t.Fatalf("過去の履歴が壊れています: %+v", run)
	}
	filtered, err := st.ListRunsFiltered(10, model.ModeRemote)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 0 {
		t.Fatalf("legacy が remote に混ざっています: %+v", filtered)
	}
	all, err := st.ListRunsFiltered(10, "")
	if err != nil || len(all) != 1 {
		t.Fatalf("全履歴: %+v err=%v", all, err)
	}
	ex, err := st.LoadExecution()
	if err != nil || ex.Status != model.ExecIdle {
		t.Fatalf("初期状態: %+v err=%v", ex, err)
	}
	now := time.Now().UTC()
	if err := st.SaveExecution(Execution{Mode: model.ModeLocal, Status: model.ExecRunning, Step: "実行中", StartedAt: &now, Log: "-----BEGIN OPENSSH PRIVATE KEY----- secret-material"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.LoadExecution()
	if err != nil || loaded.Status != model.ExecRunning || !strings.Contains(loaded.Log, "PRIVATE KEY") {
		t.Fatalf("保存前のログはそのまま残る: %+v err=%v", loaded, err)
	}
	if redacted := RedactLog(loaded.Log); strings.Contains(redacted, "PRIVATE KEY") || strings.Contains(redacted, "secret-material") {
		t.Fatalf("秘密が残っています: %s", redacted)
	}
}
