package collector

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/analyzer"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

type pgRunner struct {
	calls []string
	body  string
	err   error
}

func (f *pgRunner) LookPath(name string) (string, error) {
	return "", errors.New("external analyzer must not run")
}
func (f *pgRunner) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return []byte(f.body), nil, f.err
}

const pgFixture = `{"version":1,"database":"app","captured_at":"2026-10-31T14:00:00Z","stats_reset":"2026-10-31T13:00:00Z","hidden_queries":0,"queries":[{"query":"SELECT 'private-value', $1","calls":2,"total_exec_time":1200,"max_exec_time":800}]}`

func TestCollectPostgresThroughAnalysis(t *testing.T) {
	cfg, st := loadCfg(t, nil)
	cfg.Hosts = []config.Host{{Name: "db1", Address: "203.0.113.1", Postgres: &config.Postgres{Database: "app", User: "postgres", Sudo: true}}}
	fake := &pgRunner{body: pgFixture}
	res, err := Collect(context.Background(), st, cfg, "pg", fake)
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || !strings.Contains(fake.calls[0], "default_transaction_read_only=on") || !strings.Contains(fake.calls[0], "StrictHostKeyChecking=yes") || !strings.Contains(fake.calls[0], "'sudo' '-n' '-u' 'postgres'") {
		t.Fatalf("unsafe command: %v", fake.calls)
	}
	sources, err := st.Sources(res.RunID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources: %+v %v", sources, err)
	}
	p := filepath.Join(cfg.DataDir, "snapshots", res.RunID, sources[0].SnapshotRel)
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-value") {
		t.Fatal("saved literal")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("snapshot permissions")
	}
	if _, err := analyzer.Analyze(context.Background(), st, cfg, res.RunID, fake); err != nil {
		t.Fatal(err)
	}
	qs, err := st.Queries(res.RunID, 20)
	if err != nil || len(qs) != 1 || qs[0].TotalSeconds != 1.2 || qs[0].AvgSeconds != 0.6 {
		t.Fatalf("queries: %+v %v", qs, err)
	}
	run, _ := st.GetRun(res.RunID)
	if run.SlowAnalysis != model.AnalysisOK || !strings.Contains(run.CollectionScope, "累積") {
		t.Fatalf("run: %+v", run)
	}
}

func TestPostgresFailureIsNotAbsent(t *testing.T) {
	for _, bad := range []struct {
		name, body string
		limit      int64
		err        error
	}{
		{"permission", "", 10000, errors.New("exit 1")}, {"invalid", "broken", 10000, nil}, {"oversize", pgFixture, 10, nil},
	} {
		t.Run(bad.name, func(t *testing.T) {
			cfg, st := loadCfg(t, nil)
			cfg.Hosts = []config.Host{{Name: "db", Address: "203.0.113.1", Postgres: &config.Postgres{Database: "app", User: "reader"}}}
			cfg.Collection.MaxBytesPerFile = bad.limit
			res, err := Collect(context.Background(), st, cfg, "pg", &pgRunner{body: bad.body, err: bad.err})
			if err == nil {
				t.Fatal("failure accepted")
			}
			run, _ := st.GetRun(res.RunID)
			if run.SlowAnalysis != model.AnalysisFailed {
				t.Fatalf("not failed: %+v", run)
			}
		})
	}
}
