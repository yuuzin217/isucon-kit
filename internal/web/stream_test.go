package web

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

func TestLocalCycleShowsLogWhileRunning(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Fatal("make が必要です")
	}
	dir := t.TempDir()
	makefile := "cycle:\n" +
		"\techo hello-from-make\n" +
		"\techo \"-----BEGIN OPENSSH PRIVATE KEY----- secret-material\"\n" +
		"\tsleep 2\n" +
		"\texit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := baseConfig(t.TempDir())
	cfg.Local = &config.LocalPractice{WorkDir: dir, Target: "cycle", TimeoutSeconds: 30}
	st := newStore(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	h := NewMuxWith(st, nil, Options{Config: cfg, ExecutionEnabled: true})
	if err := st.SetSetting("mode", model.ModeLocal); err != nil {
		t.Fatal(err)
	}
	token := csrfToken(t, h)
	rec := post(h, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("開始 %d %s", rec.Code, rec.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	var saw string
	for time.Now().Before(deadline) {
		ex, err := st.LoadExecution()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(ex.Log, "hello-from-make") && ex.Status == model.ExecRunning {
			saw = ex.Log
			if ex.Step == "完了" {
				t.Fatalf("実行中なのに完了です: %+v", ex)
			}
			break
		}
		if ex.Status == model.ExecDone || ex.Status == model.ExecFailed {
			t.Fatalf("ログが見える前に終わりました: %+v", ex)
		}
		time.Sleep(30 * time.Millisecond)
	}
	if saw == "" {
		t.Fatal("実行中のログが出ません")
	}
	if strings.Contains(saw, "PRIVATE KEY") || strings.Contains(saw, "secret-material") {
		t.Fatalf("実行中のログに秘密があります: %s", saw)
	}
	if len(saw) > 64*1024+32 {
		t.Fatalf("実行中ログが長すぎます: %d", len(saw))
	}
	ex := waitExec(t, st, true)
	if ex.Status != model.ExecFailed {
		t.Fatalf("失敗を成功にしています: %+v", ex)
	}
	if !strings.Contains(ex.Log, "hello-from-make") || strings.Contains(ex.Log, "secret-material") || strings.Contains(ex.Log, "PRIVATE KEY") {
		t.Fatalf("終了後のログ: %s", ex.Log)
	}
}
