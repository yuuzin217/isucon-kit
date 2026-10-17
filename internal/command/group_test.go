package command

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRunGroupKillsDescendants(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "child.pid")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	script := "sleep 60 & echo $! > " + shellSingle(pidfile) + "; wait"
	_, _, _, err := RunGroup(ctx, "sh", 4096, "-c", script)
	if err == nil || !strings.Contains(err.Error(), "タイムアウト") {
		t.Fatalf("タイムアウトになりません: %v", err)
	}
	raw, readErr := os.ReadFile(pidfile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if convErr != nil {
		t.Fatal(convErr)
	}
	if waitDead(pid) {
		return
	}
	t.Fatalf("子プロセス %d が残っています", pid)
}

func TestRunGroupCapsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, stderr, truncated, err := RunGroup(ctx, "sh", 4096, "-c", "dd if=/dev/zero bs=1024 count=200 2>/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(stdout) != 4096 || len(stderr) != 0 {
		t.Fatalf("上限: truncated=%v stdout=%d stderr=%d", truncated, len(stdout), len(stderr))
	}
}

func TestRunGroupKeepsTailMarker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	seen := make(chan struct{}, 1)
	finished := make(chan struct{})
	var stdout []byte
	var truncated bool
	var runErr error
	go func() {
		stdout, _, truncated, runErr = RunGroupProgress(ctx, "sh", 64*1024, func(text string) {
			if strings.Contains(text, "marker-at-end") {
				select {
				case seen <- struct{}{}:
				default:
				}
			}
		}, "-c", "echo HEAD-MARKER; dd if=/dev/zero bs=1024 count=300 2>/dev/null | tr '\\0' 'A'; echo marker-at-end; sleep 1")
		close(finished)
	}()
	select {
	case <-seen:
	case <-time.After(6 * time.Second):
		t.Fatal("実行中に marker-at-end が見えません")
	}
	select {
	case <-finished:
		t.Fatal("マーカーはプロセス終了後にしか出ていません")
	case <-time.After(200 * time.Millisecond):
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("コマンドが終わりません")
	}
	if runErr != nil {
		t.Fatal(runErr)
	}
	text := string(stdout)
	if !truncated || !strings.Contains(text, "marker-at-end") || !strings.HasSuffix(strings.TrimSpace(text), "marker-at-end") {
		t.Fatalf("終了後の末尾: truncated=%v tail=%q", truncated, tail(text, 80))
	}
	if strings.Contains(text, "HEAD-MARKER") {
		t.Fatal("先頭の大量出力を残しています")
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func TestRunGroupSharesCaptureBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, stderr, truncated, err := RunGroup(ctx, "sh", 4096, "-c", "dd if=/dev/zero bs=3000 count=1 2>/dev/null; dd if=/dev/zero bs=3000 count=1 >&2")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(stdout)+len(stderr) != 4096 {
		t.Fatalf("合計上限: truncated=%v stdout=%d stderr=%d", truncated, len(stdout), len(stderr))
	}
}

func TestProcessAliveCurrentAndExited(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Fatal("自分のプロセスを生存と判定できません")
	}
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if !waitDead(cmd.Process.Pid) {
		t.Fatal("終了したプロセスを生存とみなしています")
	}
}

func waitDead(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !ProcessAlive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func shellSingle(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}
