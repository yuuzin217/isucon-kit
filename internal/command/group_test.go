package command

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("子プロセス %d が残っています", pid)
}

func TestRunGroupCapsOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, _, truncated, err := RunGroup(ctx, "sh", 4096, "-c", "dd if=/dev/zero bs=1024 count=200 status=none")
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(stdout) != 4096 {
		t.Fatalf("上限: truncated=%v len=%d", truncated, len(stdout))
	}
}

func processAlive(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	text := string(data)
	i := strings.LastIndex(text, ")")
	if i < 0 || i+2 >= len(text) {
		return true
	}
	return text[i+2] != 'Z'
}

func shellSingle(path string) string {
	return "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
}

func TestProcessAliveUsesSyscall(t *testing.T) {
	if processAlive(os.Getpid()) == false {
		t.Fatal("自分のプロセスを生存と判定できません")
	}
	_ = syscall.Signal(0)
}
