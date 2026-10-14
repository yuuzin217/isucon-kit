package collector

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

type fakeRun struct {
	calls [][]string
}

func (f *fakeRun) LookPath(name string) (string, error) { return name, nil }

func (f *fakeRun) Run(_ context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	remote := args[len(args)-1]
	switch {
	case strings.Contains(remote, "stat "):
		return []byte("100\n"), nil, nil
	case strings.Contains(remote, "tail "):
		return []byte("PARTIAL\n# Time: ok\nSELECT 1;\n"), nil, nil
	case strings.Contains(remote, "systemctl "):
		return []byte("active\n"), nil, nil
	case strings.Contains(remote, "test -r "):
		return nil, nil, nil
	default:
		return nil, []byte("unexpected"), os.ErrInvalid
	}
}

func loadCfg(t *testing.T, mutate func(*config.File, string)) (*config.File, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "isucon-kit.json")
	if err := config.WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, mutateBytes(raw), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return cfg, st
}

func mutateBytes(raw []byte) []byte { return raw }

func TestPlaceholderDoesNotSSH(t *testing.T) {
	cfg, st := loadCfg(t, nil)
	fake := &fakeRun{}
	res, err := Collect(context.Background(), st, cfg, "base", fake)
	if err == nil {
		t.Fatal("雛形への収集が成功扱いになっています")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("SSH しました: %+v", fake.calls)
	}
	if res.RunID == "" || res.Status != model.CollectionFailed {
		t.Fatalf("%+v", res)
	}
}

func TestCollectQuotesAndTrimsTail(t *testing.T) {
	cfg, st := loadCfg(t, nil)
	raw, err := os.ReadFile(filepath.Join(cfg.BaseDir(), "isucon-kit.json"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(raw), "example.invalid", "203.0.113.10", 1)
	body = strings.Replace(body, `"max_bytes_per_file": 33554432`, `"max_bytes_per_file": 10`, 1)
	body = strings.Replace(body, `"slow_logs": []`, `"slow_logs": [{"path":"/var/log/mysql/slow.log","format":"mysql"}]`, 1)
	path := filepath.Join(cfg.BaseDir(), "isucon-kit.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeRun{}
	res, err := Collect(context.Background(), st, cfg, "tail", fake)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.CollectionOK {
		t.Fatalf("%+v", res)
	}
	var sawQuote, sawStrict, sawSudo bool
	for _, call := range fake.calls {
		joined := strings.Join(call, " ")
		if strings.Contains(joined, "sudo") {
			sawSudo = true
		}
		if strings.Contains(joined, "StrictHostKeyChecking=yes") && strings.Contains(joined, "BatchMode=yes") {
			sawStrict = true
		}
		remote := call[len(call)-1]
		if strings.Contains(remote, "'/var/log/mysql/slow.log'") || strings.Contains(remote, "'/var/log/nginx/access.log'") {
			sawQuote = true
		}
		if strings.Contains(remote, "StrictHostKeyChecking=no") {
			t.Fatal("ホスト鍵検証を無効にしています")
		}
	}
	if !sawQuote || !sawStrict || sawSudo {
		t.Fatalf("quote=%v strict=%v sudo=%v calls=%v", sawQuote, sawStrict, sawSudo, fake.calls)
	}
	snap := filepath.Join(cfg.DataDir, "snapshots", res.RunID)
	entries, err := os.ReadDir(snap)
	if err != nil {
		t.Fatal(err)
	}
	var slow []byte
	for _, ent := range entries {
		if strings.Contains(ent.Name(), "slow") {
			slow, err = os.ReadFile(filepath.Join(snap, ent.Name()))
			if err != nil {
				t.Fatal(err)
			}
			info, err := ent.Info()
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Fatalf("スナップショット権限 %o", info.Mode().Perm())
			}
		}
	}
	if !bytes.HasPrefix(slow, []byte("# Time: ok\n")) {
		t.Fatalf("不完全なスローログ先頭を残しています: %q", slow)
	}
}

func TestVerifyPlaceholder(t *testing.T) {
	cfg, _ := loadCfg(t, nil)
	fake := &fakeRun{}
	text, err := Verify(context.Background(), cfg, fake)
	if err == nil {
		t.Fatal("未設定の接続先を成功にしています")
	}
	if len(fake.calls) != 0 {
		t.Fatal("接続しました")
	}
	if !strings.Contains(text, "未設定") {
		t.Fatalf("%s", text)
	}
}

func TestImportCopy(t *testing.T) {
	cfg, st := loadCfg(t, nil)
	access := filepath.Join(cfg.BaseDir(), "a.jsonl")
	if err := os.WriteFile(access, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Import(context.Background(), st, cfg, access, "json", "", "local")
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.GetRun(res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if run.CollectionScope == "" || run.Score != nil || run.BenchmarkStatus != model.BenchmarkUnknown {
		t.Fatalf("%+v", run)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "snapshots", res.RunID, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}
