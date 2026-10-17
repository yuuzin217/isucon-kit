// Package practice はローカル練習ディレクトリの許可済み make ターゲットだけを実行する。
package practice

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

// AllowedTargets は画面から起動できる make ターゲットである。
var AllowedTargets = map[string]struct{}{
	"cycle": {},
}

// Spec は実行前に検証するパラメータである。
type Spec struct {
	WorkDir string
	Make    string
	Target  string
	Timeout time.Duration
	// OnLog は実行中の出力を受け取る。nil なら通知しない。成功判定には使わない。
	OnLog func(string)
}

// ScoreRecord は練習側が書き出す明示的なスコアファイルである。
type ScoreRecord struct {
	Score  *int   `json:"score"`
	Status string `json:"status"`
}

// Command はシェルを経由しない引数を返す。
func Command(spec Spec) (string, []string, error) {
	if _, ok := AllowedTargets[spec.Target]; !ok || spec.Target == "" {
		return "", nil, fmt.Errorf("許可されていない make ターゲットです: %q", spec.Target)
	}
	makeBin := spec.Make
	if makeBin == "" {
		makeBin = "make"
	}
	base := filepath.Base(makeBin)
	if base != "make" && base != "gmake" {
		return "", nil, fmt.Errorf("実行ファイルは make か gmake だけです: %s", makeBin)
	}
	if strings.ContainsAny(makeBin, "\n\r") || strings.ContainsAny(spec.WorkDir, "\n\r") || strings.ContainsAny(spec.Target, "\n\r") {
		return "", nil, errors.New("パスに改行は使えません")
	}
	if !filepath.IsAbs(spec.WorkDir) || spec.WorkDir == "/" || strings.Contains(spec.WorkDir, "..") {
		return "", nil, fmt.Errorf("作業ディレクトリは絶対パスで、ルートや親参照は使えません: %s", spec.WorkDir)
	}
	wd := filepath.Clean(spec.WorkDir)
	info, err := os.Stat(wd)
	if err != nil || !info.IsDir() {
		return "", nil, fmt.Errorf("作業ディレクトリがありません: %s", wd)
	}
	if _, err := os.Stat(filepath.Join(wd, "Makefile")); err != nil {
		if _, err2 := os.Stat(filepath.Join(wd, "makefile")); err2 != nil {
			return "", nil, fmt.Errorf("作業ディレクトリに Makefile がありません: %s", wd)
		}
	}
	return makeBin, []string{"-C", wd, spec.Target}, nil
}

// Run は許可済みターゲットを実行し、標準出力と標準エラーを結合して返す。
func Run(ctx context.Context, runner command.Runner, spec Spec) (string, error) {
	if runner == nil {
		runner = command.OSRunner{}
	}
	name, args, err := Command(spec)
	if err != nil {
		return "", err
	}
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	var stdout, stderr []byte
	var truncated bool
	var runErr error
	if _, ok := runner.(command.OSRunner); ok {
		stdout, stderr, truncated, runErr = command.RunGroupProgress(ctx, name, command.MaxCaptureBytes, spec.OnLog, args...)
	} else {
		stdout, stderr, runErr = runner.Run(ctx, name, args...)
	}
	log := command.FormatOutput(stdout, stderr, truncated)
	if runErr != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return log, fmt.Errorf("計測がタイムアウトしました: %w", ctx.Err())
		}
		return log, fmt.Errorf("計測コマンドが失敗しました: %w", runErr)
	}
	return log, nil
}

// Artifact は実行前後で成果物が更新されたかを比べるための観測値である。
type Artifact struct {
	Path    string
	Exists  bool
	ModTime time.Time
	Size    int64
	Sum     [32]byte
}

// Observe はファイルの更新時刻、サイズ、内容ハッシュを読む。無いファイルは Exists=false。
func Observe(path string) (Artifact, error) {
	art := Artifact{Path: path}
	if path == "" {
		return art, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return art, nil
		}
		return art, fmt.Errorf("成果物を確認できません: %w", err)
	}
	if info.IsDir() {
		return art, fmt.Errorf("成果物がディレクトリです: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return art, fmt.Errorf("成果物を開けません: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return art, fmt.Errorf("成果物を読めません: %w", err)
	}
	art.Exists = true
	art.ModTime = info.ModTime()
	art.Size = n
	copy(art.Sum[:], h.Sum(nil))
	return art, nil
}

// Changed は実行後の成果物が、実行前と違う内容か新しい更新時刻を持つかを返す。
// 内容も更新時刻も同じファイルは、今回の計測結果としては受け取らない。
func Changed(before, after Artifact) bool {
	if !after.Exists {
		return false
	}
	if !before.Exists {
		return true
	}
	if before.Sum != after.Sum || before.Size != after.Size {
		return true
	}
	return after.ModTime.After(before.ModTime)
}

// ReadScore は明示された JSON スコアファイルを読む。ファイルが無ければ ok=false。
func ReadScore(path string) (ScoreRecord, bool, error) {
	if path == "" {
		return ScoreRecord{}, false, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ScoreRecord{}, false, nil
		}
		return ScoreRecord{}, false, err
	}
	var rec ScoreRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return ScoreRecord{}, false, fmt.Errorf("スコアファイルの JSON が不正です: %w", err)
	}
	switch rec.Status {
	case model.BenchmarkPass:
		if rec.Score == nil || *rec.Score < 0 {
			return ScoreRecord{}, false, errors.New("pass のスコアファイルには 0 以上の score が必要です")
		}
	case model.BenchmarkFail, model.BenchmarkUnknown:
		if rec.Score != nil && *rec.Score < 0 {
			return ScoreRecord{}, false, errors.New("score は 0 以上です")
		}
	default:
		return ScoreRecord{}, false, fmt.Errorf("スコアファイルの status が不正です: %s", rec.Status)
	}
	return rec, true, nil
}
