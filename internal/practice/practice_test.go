package practice

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

type fakeRunner struct {
	name string
	args []string
	err  error
	wait bool
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.name = name
	f.args = append([]string(nil), args...)
	if f.wait {
		<-ctx.Done()
		return []byte("-----BEGIN OPENSSH PRIVATE KEY----- secret-material\n"), nil, ctx.Err()
	}
	if f.err != nil {
		return nil, []byte("failed"), f.err
	}
	return []byte("ok\n-----BEGIN OPENSSH PRIVATE KEY----- secret-material\n"), nil, nil
}

func (f *fakeRunner) LookPath(name string) (string, error) { return name, nil }

func workdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("cycle:\n\ttrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCommandAllowlist(t *testing.T) {
	dir := workdir(t)
	name, args, err := Command(Spec{WorkDir: dir, Target: "cycle"})
	if err != nil {
		t.Fatal(err)
	}
	if name != "make" || len(args) != 3 || args[0] != "-C" || args[1] != dir || args[2] != "cycle" {
		t.Fatalf("引数が許可範囲を超えています: %s %v", name, args)
	}
	for _, target := range []string{"", "down", "cycle;rm", "up"} {
		if _, _, err := Command(Spec{WorkDir: dir, Target: target}); err == nil {
			t.Fatalf("ターゲット %q を許可しました", target)
		}
	}
	if _, _, err := Command(Spec{WorkDir: dir, Make: "/tmp/bash", Target: "cycle"}); err == nil {
		t.Fatal("make 以外の実行ファイルを許可しました")
	}
	if _, _, err := Command(Spec{WorkDir: "relative", Target: "cycle"}); err == nil {
		t.Fatal("相対パスを許可しました")
	}
	if _, _, err := Command(Spec{WorkDir: "/", Target: "cycle"}); err == nil {
		t.Fatal("ルートを許可しました")
	}
	if _, _, err := Command(Spec{WorkDir: dir + "/../" + filepath.Base(dir), Target: "cycle"}); err == nil {
		t.Fatal("親参照を許可しました")
	}
	missing := filepath.Join(t.TempDir(), "no-makefile")
	if err := os.Mkdir(missing, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Command(Spec{WorkDir: missing, Target: "cycle"}); err == nil {
		t.Fatal("Makefile が無いディレクトリを許可しました")
	}
}

func TestRunTimeoutAndRedactableOutput(t *testing.T) {
	dir := workdir(t)
	runner := &fakeRunner{wait: true}
	_, err := Run(context.Background(), runner, Spec{WorkDir: dir, Target: "cycle", Timeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("タイムアウトになりません: %v", err)
	}
	if runner.name != "make" || strings.Join(runner.args, " ") != "-C "+dir+" cycle" {
		t.Fatalf("実行引数が違います: %s %v", runner.name, runner.args)
	}
}

func TestUnchangedArtifactIsStale(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.json")
	if err := os.WriteFile(path, []byte(`{"score":42,"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	if Changed(before, after) {
		t.Fatal("更新していないスコアを今回の結果とみなしました")
	}
	if err := os.WriteFile(path, []byte(`{"score":43,"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rewritten, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	if !Changed(before, rewritten) {
		t.Fatal("書き直したスコアを受け取っていません")
	}
}

func TestMakeTimeoutKillsRecipeChildren(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Fatal("make が必要です")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("cycle:\n\tsleep 60 & echo $$! > child.pid; wait\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), command.OSRunner{}, Spec{WorkDir: dir, Target: "cycle", Timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("タイムアウトになりません: %v", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(dir, "child.pid"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil {
		t.Fatal(convErr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !command.ProcessAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("make の子プロセス %d が残っています", pid)
}

func TestAppendSpanTakesOnlyGrowth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	if err := os.WriteFile(path, []byte("old-line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("new-line\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	offset, n, kind, ok, err := AppendSpan(before, after)
	if err != nil || !ok || kind != "append" || offset != int64(len("old-line\n")) || n != int64(len("new-line\n")) {
		t.Fatalf("増分: offset=%d n=%d kind=%s ok=%v err=%v", offset, n, kind, ok, err)
	}
	if err := os.WriteFile(path, []byte("rotated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rotated, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	_, n, kind, ok, err = AppendSpan(after, rotated)
	if err != nil || !ok || kind == "append" || n != int64(len("rotated\n")) {
		t.Fatalf("切り詰め: n=%d kind=%s ok=%v err=%v", n, kind, ok, err)
	}
}

func TestAppendSpanDetectsTruncateRewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.log")
	beforeBody := bytes.Repeat([]byte("A"), 5*1024)
	if err := os.WriteFile(path, beforeBody, 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("B"), 5*1024)); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	appended, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	offset, n, kind, ok, err := AppendSpan(before, appended)
	if err != nil || !ok || kind != "append" || offset != 5*1024 || n != 5*1024 {
		t.Fatalf("通常の追記: offset=%d n=%d kind=%s ok=%v err=%v", offset, n, kind, ok, err)
	}
	rewritten := bytes.Repeat([]byte("C"), 10*1024)
	if err := os.WriteFile(path, rewritten, 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Observe(path)
	if err != nil {
		t.Fatal(err)
	}
	if !sameID(before, after) {
		t.Fatal("テストでは同じ inode のまま切り詰めています")
	}
	offset, n, kind, ok, err = AppendSpan(before, after)
	if err != nil || !ok || kind != "rewrite" || offset != 0 || n != 10*1024 {
		t.Fatalf("再作成: offset=%d n=%d kind=%s ok=%v err=%v", offset, n, kind, ok, err)
	}
}

func TestReadScore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "score.json")
	if _, ok, err := ReadScore(""); err != nil || ok {
		t.Fatalf("未設定: ok=%v err=%v", ok, err)
	}
	if _, ok, err := ReadScore(filepath.Join(dir, "missing.json")); err != nil || ok {
		t.Fatalf("欠落: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(path, []byte(`{"status":"pass"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadScore(path); err == nil {
		t.Fatal("pass なのに score 無しを受け付けました")
	}
	score := 0
	raw := `{"score":0,"status":"pass"}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, ok, err := ReadScore(path)
	if err != nil || !ok || rec.Score == nil || *rec.Score != score || rec.Status != model.BenchmarkPass {
		t.Fatalf("0点: %+v ok=%v err=%v", rec, ok, err)
	}
	if err := os.WriteFile(path, []byte(`{"status":"fail"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, ok, err = ReadScore(path)
	if err != nil || !ok || rec.Score != nil || rec.Status != model.BenchmarkFail {
		t.Fatalf("失敗: %+v ok=%v err=%v", rec, ok, err)
	}
}
