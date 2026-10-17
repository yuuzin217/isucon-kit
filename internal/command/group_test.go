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
