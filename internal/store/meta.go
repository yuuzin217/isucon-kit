package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

// Execution は画面を更新しても残る実行状態である。
type Execution struct {
	Mode       string
	Status     string
	Step       string
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      string
	Log        string
	RunID      string
}

// GetSetting は保存済みの設定値を返す。無いときは def。
func (s *Store) GetSetting(key, def string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return def, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// SetSetting は設定値を置き換える。
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// SetRunMode は既存 run のモードを付ける。履歴行は消さない。
func (s *Store) SetRunMode(runID, mode string) error {
	res, err := s.db.Exec(`UPDATE runs SET mode = ? WHERE id = ?`, mode, runID)
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
	return nil
}

// ListRunsFiltered は mode が空なら全件、指定時はそのモードだけ返す。
func (s *Store) ListRunsFiltered(limit int, mode string) ([]model.Run, error) {
	q := `SELECT ` + runColumns + ` FROM runs`
	args := []any{}
	if mode != "" {
		q += ` WHERE mode = ?`
		args = append(args, mode)
	}
	q += ` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
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

// SaveExecution は実行状態を1行で保持する。ログは長すぎる場合に末尾だけ残す。
func (s *Store) SaveExecution(ex Execution) error {
	ex.Log = tailText(ex.Log, 64*1024)
	_, err := s.db.Exec(`INSERT INTO executions(id, mode, status, step, started_at, finished_at, error, log, run_id)
		VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			mode = excluded.mode,
			status = excluded.status,
			step = excluded.step,
			started_at = excluded.started_at,
			finished_at = excluded.finished_at,
			error = excluded.error,
			log = excluded.log,
			run_id = excluded.run_id`,
		ex.Mode, ex.Status, ex.Step, formatTime(ex.StartedAt), formatTime(ex.FinishedAt), ex.Error, ex.Log, ex.RunID)
	return err
}

// LoadExecution は最後の実行状態を返す。未実行なら待機中。
func (s *Store) LoadExecution() (Execution, error) {
	var ex Execution
	var started, finished sql.NullString
	err := s.db.QueryRow(`SELECT mode, status, step, started_at, finished_at, error, log, run_id FROM executions WHERE id = 1`).Scan(
		&ex.Mode, &ex.Status, &ex.Step, &started, &finished, &ex.Error, &ex.Log, &ex.RunID)
	if errors.Is(err, sql.ErrNoRows) {
		return Execution{Status: model.ExecIdle, Step: ""}, nil
	}
	if err != nil {
		return Execution{}, err
	}
	ex.StartedAt = parseTime(started)
	ex.FinishedAt = parseTime(finished)
	return ex, nil
}

func formatTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(timeLayout)
}

func parseTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t, err := time.Parse(timeLayout, v.String)
	if err != nil {
		return nil
	}
	return &t
}

func tailText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return "…省略…\n" + s[len(s)-max:]
}

// RedactLog は秘密鍵らしき行を実行ログから除く。
func RedactLog(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "-----BEGIN ") || strings.Contains(trim, "PRIVATE KEY") {
			lines[i] = "[secret redacted]"
		}
	}
	return strings.Join(lines, "\n")
}
