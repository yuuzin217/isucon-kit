package analyzer

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/collector"
	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

func TestRealToolsMatchFixture(t *testing.T) {
	runner := command.OSRunner{}
	if _, err := runner.LookPath("alp"); err != nil {
		t.Skip("alp が無いため実ツール検証をスキップします")
	}
	if _, err := runner.LookPath("pt-query-digest"); err != nil {
		t.Skip("pt-query-digest が無いため実ツール検証をスキップします")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "isucon-kit.json")
	if err := config.WriteTemplate(cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root := filepath.Join("..", "..", "testdata")
	res, err := collector.Import(context.Background(), st, cfg, filepath.Join(root, "nginx-access.jsonl"), "json", filepath.Join(root, "mysql-slow.log"), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), st, cfg, res.RunID, command.OSRunner{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), st, cfg, res.RunID, command.OSRunner{}); err != nil {
		t.Fatal(err)
	}
	eps, err := st.Endpoints(res.RunID, 20)
	if err != nil {
		t.Fatal(err)
	}
	assertEndpoint(t, eps, "GET", "/api/items/:id", 2, 0.4, 0.2, 0.3, false)
	assertEndpoint(t, eps, "POST", "/api/items/:id", 1, 0.2, 0.2, 0.2, true)
	assertEndpoint(t, eps, "GET", "/health", 1, 0.01, 0.01, 0.01, true)
	qs, err := st.Queries(res.RunID, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, q := range qs {
		if q.Fingerprint == "select * from items where id=?" {
			found = true
			if q.Count != 2 || math.Abs(q.TotalSeconds-1.7) > 1e-6 || math.Abs(q.AvgSeconds-0.85) > 1e-6 {
				t.Fatalf("%+v", q)
			}
		}
	}
	if !found {
		t.Fatalf("%+v", qs)
	}
	run, err := st.GetRun(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.AnalysisStatus != model.AnalysisOK {
		t.Fatalf("再解析後の状態 %s %s", run.AnalysisStatus, run.AnalysisError)
	}
}

func TestRealLTSV(t *testing.T) {
	runner := command.OSRunner{}
	if _, err := runner.LookPath("alp"); err != nil {
		t.Skip("alp が無いため実ツール検証をスキップします")
	}
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "isucon-kit.json")
	if err := config.WriteTemplate(cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	res, err := collector.Import(context.Background(), st, cfg, filepath.Join("..", "..", "testdata", "nginx-access.ltsv"), "ltsv", "", "ltsv-demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Analyze(context.Background(), st, cfg, res.RunID, runner); err != nil {
		t.Fatal(err)
	}
	eps, err := st.Endpoints(res.RunID, 20)
	if err != nil {
		t.Fatal(err)
	}
	assertEndpoint(t, eps, "GET", "/api/items/:id", 2, 0.5, 0.25, 0.4, false)
	assertEndpoint(t, eps, "POST", "/api/orders", 1, 0.05, 0.05, 0.05, true)
	run, err := st.GetRun(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.SlowAnalysis != model.AnalysisAbsent {
		t.Fatalf("スローログ未設定を未収集にしませんでした: %s", run.SlowAnalysis)
	}
}

func TestMissingRequestTimeIsNotUpstream(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.jsonl")
	body := `{"time":"2026-10-09T00:00:00Z","method":"GET","uri":"/health","status":200,"response_time":0.5}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	err := ValidateAccess(path, "json")
	if err == nil || !strings.Contains(err.Error(), "request_time") {
		t.Fatalf("%v", err)
	}
}

func TestLTSVRequiresReqtime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.ltsv")
	if err := os.WriteFile(path, []byte("time:t\tmethod:GET\turi:/a\tapptime:0.2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateAccess(path, "ltsv"); err == nil {
		t.Fatal("apptime を reqtime の代わりに受理しました")
	}
}

func assertEndpoint(t *testing.T, eps []model.EndpointStat, method, route string, count int, total, avg, max float64, wantP95 bool) {
	t.Helper()
	for _, ep := range eps {
		if ep.Method == method && ep.Route == route {
			if ep.Count != count || math.Abs(ep.TotalSeconds-total) > 1e-6 || math.Abs(ep.AvgSeconds-avg) > 1e-6 || math.Abs(ep.MaxSeconds-max) > 1e-6 {
				t.Fatalf("%s %s %+v", method, route, ep)
			}
			if wantP95 && ep.P95Seconds == nil {
				t.Fatalf("p95 がありません: %+v", ep)
			}
			if !wantP95 && ep.P95Seconds != nil {
				t.Fatalf("p95 を合算しています: %+v", ep)
			}
			return
		}
	}
	t.Fatalf("%s %s がありません: %+v", method, route, eps)
}
