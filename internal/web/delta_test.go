package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

func TestLocalCycleImportsOnlyAppendedLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("cycle:\n\ttrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "access.log")
	oldLine := "{\"method\":\"GET\",\"uri\":\"/old\",\"status\":200,\"request_time\":0.1}\n"
	if err := os.WriteFile(logPath, []byte(oldLine), 0o644); err != nil {
		t.Fatal(err)
	}
	var n int
	runner := &gateRunner{onRun: func(name string) {
		if name != "make" {
			return
		}
		n++
		line := fmt.Sprintf("{\"method\":\"GET\",\"uri\":\"/cycle-%d\",\"status\":200,\"request_time\":0.2}\n", n)
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		if _, err := f.WriteString(line); err != nil {
			panic(err)
		}
	}}
	cfg := baseConfig(t.TempDir())
	cfg.Local = &config.LocalPractice{
		WorkDir: dir, Target: "cycle", AccessLog: logPath, AccessFormat: "json", TimeoutSeconds: 5,
	}
	st, h := muxFor(t, cfg, runner, true)
	if err := st.SetSetting("mode", model.ModeLocal); err != nil {
		t.Fatal(err)
	}
	token := csrfToken(t, h)
	prev := ""
	for cycle := 1; cycle <= 2; cycle++ {
		rec := post(h, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("cycle %d: %d %s", cycle, rec.Code, rec.Body.String())
		}
		deadline := time.Now().Add(3 * time.Second)
		var started bool
		for time.Now().Before(deadline) {
			ex, err := st.LoadExecution()
			if err != nil {
				t.Fatal(err)
			}
			if ex.Status == model.ExecRunning || (ex.RunID != "" && ex.RunID != prev && ex.Status != model.ExecIdle) {
				started = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !started {
			t.Fatalf("cycle %d が始まりません", cycle)
		}
		ex := waitExec(t, st, true)
		if ex.RunID == "" {
			t.Fatalf("cycle %d: run がありません: %+v", cycle, ex)
		}
		snap := filepath.Join(cfg.DataDir, "snapshots", ex.RunID, "local-access.log")
		body, err := os.ReadFile(snap)
		if err != nil {
			t.Fatal(err)
		}
		text := string(body)
		want := fmt.Sprintf("/cycle-%d", cycle)
		if !strings.Contains(text, want) || strings.Contains(text, "/old") {
			t.Fatalf("cycle %d のスナップショット: %s", cycle, text)
		}
		if cycle == 2 && strings.Contains(text, "/cycle-1") {
			t.Fatalf("2回目に前回の増分が含まれています: %s", text)
		}
		run, err := st.GetRun(ex.RunID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(run.CollectionScope, "今回の増分") {
			t.Fatalf("ログ範囲: %s", run.CollectionScope)
		}
		prev = ex.RunID
		view := httptest.NewRecorder()
		h.ServeHTTP(view, httptest.NewRequest(http.MethodGet, "/api/runs/"+ex.RunID, nil))
		if view.Code != http.StatusOK || !strings.Contains(view.Body.String(), "今回の増分") {
			t.Fatalf("API にログ範囲がありません: %d %s", view.Code, view.Body.String())
		}
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(page.Body.String(), "id=\"log-range\"") {
		t.Fatal("画面にログ範囲の表示がありません")
	}
}
