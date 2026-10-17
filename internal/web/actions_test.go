package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

type gateRunner struct {
	mu     sync.Mutex
	calls  [][]string
	gate   chan struct{}
	stdout string
	err    error
}

func (g *gateRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	g.mu.Lock()
	g.calls = append(g.calls, append([]string{name}, args...))
	gate, stdout, err := g.gate, g.stdout, g.err
	g.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return []byte(stdout), nil, ctx.Err()
		}
	}
	if err != nil {
		return []byte(stdout), []byte("failed"), err
	}
	return []byte(stdout), nil, nil
}

func (g *gateRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func (g *gateRunner) snapshot() [][]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([][]string, len(g.calls))
	copy(out, g.calls)
	return out
}

func baseConfig(dataDir string) *config.File {
	return &config.File{
		Version:    config.Version,
		DataDir:    dataDir,
		SSH:        config.SSH{User: "isucon", Port: 22, ConnectTimeoutSeconds: 1, IdentityFile: "/tmp/id_ed25519"},
		Collection: config.Collection{MaxBytesPerFile: 1024},
		Hosts: []config.Host{{
			Name: "web1", Address: config.PlaceholderAddress,
			AccessLogs: []config.AccessLog{{Path: "/var/log/nginx/access.log", Format: "json"}},
		}},
	}
}

func practiceDir(t *testing.T, score string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("cycle:\n\ttrue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if score != "" {
		if err := os.WriteFile(filepath.Join(dir, "score.json"), []byte(score), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func muxFor(t *testing.T, cfg *config.File, runner *gateRunner, enabled bool) (*store.Store, http.Handler) {
	t.Helper()
	st := newStore(t)
	if cfg == nil {
		cfg = baseConfig(t.TempDir())
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	h := NewMuxWith(st, nil, Options{Config: cfg, ExecutionEnabled: enabled, Runner: runner})
	return st, h
}

func csrfToken(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "127.0.0.1:8080"
	h.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie {
			return c.Value
		}
	}
	t.Fatal("CSRF クッキーがありません")
	return ""
}

func post(h http.Handler, path, body, token, host, origin string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = host
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: csrfCookie, Value: token})
		req.Header.Set("X-CSRF-Token", token)
	}
	h.ServeHTTP(rec, req)
	return rec
}

func waitExec(t *testing.T, st *store.Store, done bool) store.Execution {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var ex store.Execution
	var err error
	for time.Now().Before(deadline) {
		ex, err = st.LoadExecution()
		if err != nil {
			t.Fatal(err)
		}
		if done && ex.Status != model.ExecRunning && ex.Status != model.ExecIdle && ex.Status != "" {
			return ex
		}
		if !done && ex.Status == model.ExecRunning {
			return ex
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("実行状態が変わりません: %+v", ex)
	return ex
}

func TestExecutionDisabledAndCSRF(t *testing.T) {
	runner := &gateRunner{}
	_, h := muxFor(t, nil, runner, false)
	token := csrfToken(t, h)
	rec := post(h, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "http://127.0.0.1:8080")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("無効時 %d %s", rec.Code, rec.Body.String())
	}
	rec = post(h, "/api/actions/remote-collect", `{}`, token, "127.0.0.1:8080", "http://127.0.0.1:8080")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("リモート無効時 %d %s", rec.Code, rec.Body.String())
	}
	if len(runner.snapshot()) != 0 {
		t.Fatal("無効なのにコマンドを実行しました")
	}
	rec = post(h, "/api/mode", `{"mode":"local"}`, "", "127.0.0.1:8080", "http://127.0.0.1:8080")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF 無し %d", rec.Code)
	}
	rec = post(h, "/api/mode", `{"mode":"local"}`, token, "evil.example", "http://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("不正ホスト %d", rec.Code)
	}
	rec = post(h, "/api/mode", `{"mode":"local"}`, token, "127.0.0.1:8080", "http://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("不正 Origin %d", rec.Code)
	}
	rec = post(h, "/api/mode", `{"mode":"local"}`, token, "127.0.0.1:8080", "http://127.0.0.1:8080")
	if rec.Code != http.StatusOK {
		t.Fatalf("モード変更 %d %s", rec.Code, rec.Body.String())
	}
	got := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	h.ServeHTTP(got, req)
	if !strings.Contains(got.Body.String(), `"mode":"local"`) {
		t.Fatalf("モードが保存されていません: %s", got.Body.String())
	}
	if strings.Contains(got.Body.String(), "id_ed25519") || strings.Contains(got.Body.String(), "PRIVATE KEY") {
		t.Fatalf("秘密が画面向けレスポンスに含まれています: %s", got.Body.String())
	}
}

func TestLocalCycleSuccessFailureAndSingleFlight(t *testing.T) {
	dir := practiceDir(t, `{"score":42,"status":"pass"}`)
	cfg := baseConfig(t.TempDir())
	cfg.Local = &config.LocalPractice{WorkDir: dir, Target: "cycle", ScoreFile: filepath.Join(dir, "score.json"), TimeoutSeconds: 5}
	gate := make(chan struct{})
	runner := &gateRunner{gate: gate, stdout: "ok\n-----BEGIN OPENSSH PRIVATE KEY----- secret-material\n"}
	st, h := muxFor(t, cfg, runner, true)
	token := csrfToken(t, h)

	rec := post(h, "/api/actions/local-cycle", `{"target":"down","command":"rm -rf /"}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("開始 %d %s", rec.Code, rec.Body.String())
	}
	running := waitExec(t, st, false)
	if running.Status != model.ExecRunning {
		t.Fatalf("実行中になりません: %+v", running)
	}
	again := post(h, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "")
	if again.Code != http.StatusConflict {
		t.Fatalf("二重実行 %d %s", again.Code, again.Body.String())
	}
	mode := post(h, "/api/mode", `{"mode":"remote"}`, token, "127.0.0.1:8080", "")
	if mode.Code != http.StatusConflict {
		t.Fatalf("実行中のモード変更 %d", mode.Code)
	}
	restored := httptest.NewRecorder()
	h.ServeHTTP(restored, httptest.NewRequest(http.MethodGet, "/api/execution", nil))
	if !strings.Contains(restored.Body.String(), `"status":"running"`) {
		t.Fatalf("更新後の状態が戻りません: %s", restored.Body.String())
	}
	close(gate)
	ex := waitExec(t, st, true)
	if ex.Status != model.ExecDone {
		t.Fatalf("完了しません: %+v", ex)
	}
	if strings.Contains(ex.Log, "PRIVATE KEY") || strings.Contains(ex.Log, "secret-material") {
		t.Fatalf("秘密がログに残っています: %s", ex.Log)
	}
	calls := runner.snapshot()
	if len(calls) != 1 || calls[0][0] != "make" || strings.Join(calls[0][1:], " ") != "-C "+dir+" cycle" {
		t.Fatalf("許可外のコマンドです: %v", calls)
	}
	run, err := st.GetRun(ex.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Mode != model.ModeLocal || run.BenchmarkStatus != model.BenchmarkPass || run.Score == nil || *run.Score != 42 {
		t.Fatalf("結果の取り込み: %+v", run)
	}

	failDir := practiceDir(t, `{"status":"fail"}`)
	cfg.Local.WorkDir = failDir
	cfg.Local.ScoreFile = filepath.Join(failDir, "score.json")
	failRunner := &gateRunner{stdout: "bench failed recorded\n"}
	st2, h2 := muxFor(t, cfg, failRunner, true)
	token = csrfToken(t, h2)
	rec = post(h2, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body.String())
	}
	failed := waitExec(t, st2, true)
	if failed.Status != model.ExecFailed {
		t.Fatalf("失敗を成功と混同しています: %+v", failed)
	}
	failRun, err := st2.GetRun(failed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if failRun.BenchmarkStatus != model.BenchmarkFail || failRun.Score != nil {
		t.Fatalf("失敗 run: %+v", failRun)
	}
}

func TestLocalTimeoutAndBadWorkdir(t *testing.T) {
	dir := practiceDir(t, "")
	cfg := baseConfig(t.TempDir())
	cfg.Local = &config.LocalPractice{WorkDir: dir, Target: "cycle", TimeoutSeconds: 1}
	runner := &gateRunner{gate: make(chan struct{})}
	st, h := muxFor(t, cfg, runner, true)
	token := csrfToken(t, h)
	rec := post(h, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body.String())
	}
	ex := waitExec(t, st, true)
	if ex.Status != model.ExecFailed || !strings.Contains(ex.Error, "タイムアウト") {
		t.Fatalf("タイムアウト: %+v", ex)
	}

	cfg.Local.WorkDir = "/"
	if err := cfg.Validate(); err == nil {
		t.Fatal("ルートを設定として受け付けました")
	}
	cfg.Local.WorkDir = filepath.Join(dir, "missing")
	cfg.Local.TimeoutSeconds = 5
	st3, h3 := muxFor(t, cfg, &gateRunner{}, true)
	token = csrfToken(t, h3)
	rec = post(h3, "/api/actions/local-cycle", `{}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusAccepted {
		t.Fatal(rec.Body.String())
	}
	ex = waitExec(t, st3, true)
	if ex.Status != model.ExecFailed || ex.RunID != "" {
		t.Fatalf("不正ディレクトリなのに成功しています: %+v", ex)
	}
}

func TestRemotePartialAndNoMutation(t *testing.T) {
	body := `{"time":"2026-10-09T00:00:00+00:00","method":"GET","uri":"/health","status":200,"body_bytes":2,"request_time":0.01}` + "\n"
	cfg := baseConfig(t.TempDir())
	cfg.Hosts = []config.Host{
		{Name: "bad", Address: "bad.example", AccessLogs: []config.AccessLog{{Path: "/var/log/nginx/access.log", Format: "json"}}},
		{Name: "good", Address: "good.example", AccessLogs: []config.AccessLog{{Path: "/var/log/nginx/access.log", Format: "json"}}},
	}
	runner := &remoteRunner{body: body}
	st := newStore(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	h := NewMuxWith(st, nil, Options{Config: cfg, ExecutionEnabled: true, Runner: runner})
	token := csrfToken(t, h)
	rec := post(h, "/api/actions/remote-collect", `{}`, token, "127.0.0.1:8080", "http://127.0.0.1:8080")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("開始 %d %s", rec.Code, rec.Body.String())
	}
	ex := waitExec(t, st, true)
	if ex.Status != model.ExecDone || !strings.Contains(ex.Step, "一部") {
		t.Fatalf("部分成功: %+v", ex)
	}
	if strings.Contains(ex.Log, "PRIVATE KEY") || strings.Contains(ex.Log, "secret-material") || strings.Contains(ex.Log, "id_ed25519") {
		t.Fatalf("秘密がログに出ています: %s", ex.Log)
	}
	run, err := st.GetRun(ex.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Mode != model.ModeRemote || run.CollectionStatus != model.CollectionPartial {
		t.Fatalf("収集状態: %+v", run)
	}
	for _, call := range runner.snapshot() {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "sudo") || strings.Contains(joined, " rm ") || strings.Contains(joined, "reboot") {
			t.Fatalf("リモートを変更するコマンドです: %s", joined)
		}
		if call[0] == "ssh" {
			if !strings.Contains(joined, "BatchMode=yes") || !strings.Contains(joined, "StrictHostKeyChecking=yes") {
				t.Fatalf("SSH の安全オプションがありません: %s", joined)
			}
			remote := call[len(call)-1]
			if !strings.HasPrefix(remote, "stat ") && !strings.HasPrefix(remote, "tail -c ") {
				t.Fatalf("読み取り以外のリモートコマンドです: %s", remote)
			}
		}
	}
	page := httptest.NewRecorder()
	h.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	var session map[string]any
	if err := json.Unmarshal(page.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	raw := page.Body.String()
	if strings.Contains(raw, "id_ed25519") || strings.Contains(raw, "PRIVATE KEY") {
		t.Fatalf("セッションに秘密があります: %s", raw)
	}
	hosts, _ := session["hosts"].([]any)
	if len(hosts) != 2 {
		t.Fatalf("ホスト表示: %s", raw)
	}
}

type remoteRunner struct {
	mu    sync.Mutex
	calls [][]string
	body  string
}

func (r *remoteRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, append([]string{name}, args...))
	r.mu.Unlock()
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "bad.example"):
		return nil, []byte("ssh: connect failed\n-----BEGIN OPENSSH PRIVATE KEY----- secret-material"), errors.New("exit status 255")
	case strings.Contains(joined, "--version"):
		return []byte("alp version 1.0.22\n"), nil, nil
	case name == "ssh" && strings.Contains(joined, "stat "):
		return []byte("120\n"), nil, nil
	case name == "ssh" && strings.Contains(joined, "tail "):
		return []byte(r.body), nil, nil
	case strings.Contains(joined, "--format"):
		return []byte("Count\tMethod\tUri\tSum\tAvg\tMax\tP95\n1\tGET\t/health\t0.010\t0.010\t0.010\t0.010\n"), nil, nil
	default:
		return nil, []byte("unexpected"), errors.New("unexpected command")
	}
}

func (r *remoteRunner) LookPath(name string) (string, error) { return "/usr/bin/" + name, nil }

func (r *remoteRunner) snapshot() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]string, len(r.calls))
	copy(out, r.calls)
	return out
}

func TestManualScoreAndModeFilter(t *testing.T) {
	st, h := muxFor(t, nil, &gateRunner{}, false)
	token := csrfToken(t, h)
	score := 7
	seed(t, st, "legacy-run", &score, model.BenchmarkPass)
	rec := post(h, "/api/runs/legacy-run/score", `{"status":"pass"}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("pass に value が必要です: %d %s", rec.Code, rec.Body.String())
	}
	rec = post(h, "/api/runs/legacy-run/score", `{"status":"pass","value":0}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("0点 %d %s", rec.Code, rec.Body.String())
	}
	rec = post(h, "/api/runs/missing/score", `{"status":"fail"}`, token, "127.0.0.1:8080", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("不明 run %d", rec.Code)
	}
	zero := 0
	if err := st.SetScore("legacy-run", &zero, model.BenchmarkPass, "", nil); err != nil {
		t.Fatal(err)
	}
	head := 10
	seed(t, st, "remote-run", &head, model.BenchmarkPass)
	if err := st.SetRunMode("remote-run", model.ModeRemote); err != nil {
		t.Fatal(err)
	}
	cmp := httptest.NewRecorder()
	h.ServeHTTP(cmp, httptest.NewRequest(http.MethodGet, "/api/compare?base=legacy-run&head=remote-run", nil))
	if !strings.Contains(cmp.Body.String(), "実行モード") || !strings.Contains(cmp.Body.String(), `"score_percent":"N/A"`) {
		t.Fatalf("比較: %s", cmp.Body.String())
	}
	list := httptest.NewRecorder()
	h.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/runs?mode=remote", nil))
	if strings.Contains(list.Body.String(), "legacy-run") || !strings.Contains(list.Body.String(), "remote-run") {
		t.Fatalf("モードで履歴が混ざっています: %s", list.Body.String())
	}
}

func TestInterruptedExecution(t *testing.T) {
	st := newStore(t)
	now := time.Now().UTC()
	if err := st.SaveExecution(store.Execution{Mode: model.ModeLocal, Status: model.ExecRunning, Step: "実行中", StartedAt: &now}); err != nil {
		t.Fatal(err)
	}
	_ = NewMuxWith(st, nil, Options{})
	ex, err := st.LoadExecution()
	if err != nil {
		t.Fatal(err)
	}
	if ex.Status != model.ExecFailed || !strings.Contains(ex.Error, "停止") {
		t.Fatalf("中断を実行中のままにしています: %+v", ex)
	}
}
