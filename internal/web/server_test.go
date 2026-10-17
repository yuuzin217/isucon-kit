package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seed(t *testing.T, st *store.Store, id string, score *int, status string) {
	t.Helper()
	p95 := 0.01
	err := st.CreateRun(model.Run{
		ID: id, CreatedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC), Label: "demo",
		Score: score, BenchmarkStatus: status, CollectionStatus: model.CollectionOK,
		AnalysisStatus: model.AnalysisOK, CollectionScope: "ファイル全体（デモ）",
		AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK,
		TimingBasis: "request_time",
	}, []model.Source{{
		HostName: "local", Kind: model.KindAccess, Format: "json", RemotePath: "access.jsonl",
		SnapshotRel: "local-access.log", Status: model.SourceOK, AnalysisStatus: model.AnalysisOK,
		CollectedAt: time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC),
	}})
	if err != nil {
		t.Fatal(err)
	}
	err = st.ReplaceAnalysis(id, store.AnalysisUpdate{
		AnalysisStatus: model.AnalysisOK, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK,
		TimingBasis: "request_time",
		Endpoints:   []model.EndpointStat{{Method: "GET", Route: "/health", Count: 1, TotalSeconds: 0.01, AvgSeconds: 0.01, MaxSeconds: 0.01, P95Seconds: &p95}},
		Queries:     []model.QueryStat{{Fingerprint: "<script>alert(1)</script>", Count: 1, TotalSeconds: 0.2, AvgSeconds: 0.2, MaxSeconds: 0.2}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAPI(t *testing.T) {
	st := newStore(t)
	mux := NewMux(st, nil)
	empty := httptest.NewRecorder()
	mux.ServeHTTP(empty, httptest.NewRequest(http.MethodGet, "/api/runs/latest", nil))
	if empty.Code != http.StatusNotFound {
		t.Fatalf("latest empty %d", empty.Code)
	}
	score := 10
	seed(t, st, "run-demo-1", &score, model.BenchmarkPass)
	seed(t, st, "run-demo-2", nil, model.BenchmarkFail)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/healthz", 200},
		{http.MethodGet, "/api/runs?limit=30", 200},
		{http.MethodGet, "/api/runs?limit=0", 400},
		{http.MethodGet, "/api/runs?limit=101", 400},
		{http.MethodGet, "/api/runs/latest", 200},
		{http.MethodGet, "/api/runs/run-demo-1", 200},
		{http.MethodGet, "/api/runs/missing", 404},
		{http.MethodGet, "/api/compare?base=run-demo-1&head=run-demo-2", 200},
		{http.MethodGet, "/api/compare?base=run-demo-1", 400},
		{http.MethodGet, "/api/compare?base=missing&head=run-demo-1", 404},
		{http.MethodPost, "/api/runs", 405},
		{http.MethodGet, "/../../etc/passwd", 404},
		{http.MethodGet, "/", 200},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Fatalf("%s %s => %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/run-demo-1", nil))
	body := rec.Body.String()
	if strings.Contains(body, "identity_file") || strings.Contains(body, "BEGIN OPENSSH") || strings.Contains(body, "<script>") {
		t.Fatalf("秘密または生の HTML を返しています: %s", body)
	}
	if !strings.Contains(body, `\u003cscript`) {
		t.Fatalf("HTML エスケープされていません: %s", body)
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	health := httptest.NewRecorder()
	mux.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Header().Get("Access-Control-Allow-Origin") == "*" {
		t.Fatal("CORS ワイルドカード")
	}
	if !strings.Contains(health.Header().Get("Cache-Control"), "no-store") {
		t.Fatal("API がキャッシュされます")
	}
	page := httptest.NewRecorder()
	mux.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	html := page.Body.String()
	for _, want := range []string{"遅いAPI", "遅いSQL", "計測履歴", "ローカル練習", "ログ収集・解析"} {
		if !strings.Contains(html, want) {
			t.Fatalf("画面に %s がありません", want)
		}
	}
	if strings.Contains(html, "ランキング基準") || strings.Contains(html, "Slow Endpoints") {
		t.Fatalf("内部用語が画面に残っています: %s", html)
	}
}

func TestHealthUnavailable(t *testing.T) {
	st := newStore(t)
	mux := NewMux(st, nil)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestServeLocalhostAndShutdown(t *testing.T) {
	st := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	addr := "127.0.0.1:18765"
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, addr, st, nil) }()
	deadline := time.Now().Add(3 * time.Second)
	var resp *http.Response
	var err error
	for time.Now().Before(deadline) {
		resp, err = http.Get("http://" + addr + "/healthz")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"status":"ok"`) && !strings.Contains(string(body), `"status": "ok"`) {
		// Encoder はスペースを入れない。
		if resp.StatusCode != 200 || !strings.Contains(string(body), "ok") {
			cancel()
			t.Fatalf("%d %s", resp.StatusCode, body)
		}
	}
	errCh := make(chan error, 1)
	go func() { errCh <- Serve(context.Background(), addr, st, nil) }()
	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), "ポート") {
			cancel()
			t.Fatalf("ポート競合を報告しません: %v", err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("競合待ちが終わりません")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("Ctrl+C 相当の停止が 5 秒を超えました")
	}
}

func TestAssetsDoNotUseInnerHTML(t *testing.T) {
	js, err := os.ReadFile(filepath.Join("assets", "app.js"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(js), "innerHTML") || strings.Contains(string(js), "document.write") {
		t.Fatal("ログ由来文字列を HTML として解釈するコードがあります")
	}
	if !strings.Contains(string(js), "textContent") {
		t.Fatal("textContent がありません")
	}
}
