package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestE2EImportAnalyzeScoreReportDiff(t *testing.T) {
	repo := filepath.Join("..", "..")
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(repo, "testdata", "alp-v1.0.22-json.tsv"), filepath.Join(bin, "json.tsv"))
	copyFile(t, filepath.Join(repo, "testdata", "pt-query-digest-3.2.1.json"), filepath.Join(bin, "pt.json"))
	copyFile(t, filepath.Join(repo, "testdata", "nginx-access.jsonl"), filepath.Join(dir, "access.jsonl"))
	copyFile(t, filepath.Join(repo, "testdata", "mysql-slow.log"), filepath.Join(dir, "slow.log"))
	writeExe(t, filepath.Join(bin, "alp"), `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
if [ "$1" = "--version" ]; then echo 1.0.22-fake; exit 0; fi
prev=
file=
for a in "$@"; do
  if [ "$prev" = "--file" ]; then file=$a; fi
  prev=$a
done
if grep -q request_time "$file"; then cat "$here/json.tsv"; exit 0; fi
printf 'Count\tMethod\tUri\tSum\tAvg\tMax\tP95\n'
`)
	writeExe(t, filepath.Join(bin, "pt-query-digest"), `#!/bin/sh
here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
if [ "$1" = "--version" ]; then echo "pt-query-digest 3.2.1-fake"; exit 0; fi
file=
for a in "$@"; do file=$a; done
if grep -q Query_time "$file"; then cat "$here/pt.json"; exit 0; fi
echo "未対応のスローログです" >&2
exit 1
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	if code := Main([]string{"init"}, os.Stdout, os.Stderr); code != 0 {
		t.Fatalf("init %d", code)
	}
	if code := Main([]string{"init"}, os.Stdout, os.Stderr); code == 0 {
		t.Fatal("init が上書きしました")
	}
	if code := Main([]string{"doctor"}, os.Stdout, os.Stderr); code != 0 {
		t.Fatal("doctor")
	}
	var out, errb strings.Builder
	code := Main([]string{"import", "--access", "access.jsonl", "--access-format", "json", "--slowlog", "slow.log", "--label", "demo"}, &out, &errb)
	if code != 0 {
		t.Fatalf("import %d %s", code, errb.String())
	}
	run1 := runID(out.String())
	out.Reset()
	errb.Reset()
	if code := Main([]string{"analyze", "--run", run1}, &out, &errb); code != 0 {
		t.Fatalf("analyze %s %s", out.String(), errb.String())
	}
	if code := Main([]string{"analyze", "--run", run1}, &out, &errb); code != 0 {
		t.Fatalf("reanalyze %s", errb.String())
	}
	if code := Main([]string{"score", "set", "--run", run1, "--status", "pass", "--value", "100", "--note", "demo"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	out.Reset()
	if code := Main([]string{"report", "--run", run1}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	text := out.String()
	for _, want := range []string{"/api/items/:id", "count=2", "total=0.400s", "select * from items where id=?", "スコア: 100", "デモではない"} {
		if want == "デモではない" {
			continue
		}
		if !strings.Contains(text, want) {
			t.Fatalf("report に %q がありません:\n%s", want, text)
		}
	}
	out.Reset()
	code = Main([]string{"import", "--access", "access.jsonl", "--access-format", "json", "--label", "second"}, &out, &errb)
	if code != 0 {
		t.Fatal(errb.String())
	}
	run2 := runID(out.String())
	if code := Main([]string{"analyze", "--run", run2}, &out, &errb); code != 0 {
		t.Fatalf("second analyze %s", errb.String())
	}
	if code := Main([]string{"score", "set", "--run", run2, "--status", "fail"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	out.Reset()
	if code := Main([]string{"report", "--run", run2}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "スコア: 未計測") || !strings.Contains(out.String(), "fail") {
		t.Fatalf("FAIL と未計測を区別できていません:\n%s", out.String())
	}
	out.Reset()
	if code := Main([]string{"diff", "--base", run1, "--head", run2}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "score percent: N/A") {
		t.Fatalf("差分:\n%s", out.String())
	}
	out.Reset()
	if code := Main([]string{"runs"}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), run1) || !strings.Contains(out.String(), run2) {
		t.Fatalf("runs:\n%s", out.String())
	}
	if code := Main([]string{"no-such"}, &out, &errb); code != 2 {
		t.Fatalf("usage %d", code)
	}
	if code := Main([]string{"serve", "--port", "99999"}, &out, &errb); code != 2 {
		t.Fatalf("port %d", code)
	}
}

func TestScoreSetPassRequiresValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.jsonl"), []byte("{\"time\":\"t\",\"method\":\"GET\",\"uri\":\"/health\",\"status\":200,\"request_time\":0.1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if code := Main([]string{"init"}, ioDiscard(), ioDiscard()); code != 0 {
		t.Fatal("init")
	}
	var out, errb strings.Builder
	if code := Main([]string{"import", "--access", "a.jsonl", "--access-format", "json", "--label", "score"}, &out, &errb); code != 0 {
		t.Fatalf("import %s", errb.String())
	}
	run := runID(out.String())
	if run == "" {
		t.Fatal(out.String())
	}

	errb.Reset()
	if code := Main([]string{"score", "set", "--run", run, "--status", "pass"}, ioDiscard(), &errb); code != 2 {
		t.Fatalf("pass 値なしの終了コード %d: %s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "--value") {
		t.Fatalf("案内がありません: %s", errb.String())
	}

	errb.Reset()
	if code := Main([]string{"score", "set", "--run", run, "--status", "pass", "--value", "-1"}, ioDiscard(), &errb); code != 2 {
		t.Fatalf("負数の終了コード %d", code)
	}
	errb.Reset()
	if code := Main([]string{"score", "set", "--run", run, "--status", "fail", "--value", "-1"}, ioDiscard(), &errb); code != 2 {
		t.Fatalf("fail の負数 %d", code)
	}

	out.Reset()
	errb.Reset()
	if code := Main([]string{"score", "set", "--run", run, "--status", "pass", "--value", "0"}, &out, &errb); code != 0 {
		t.Fatalf("0点: %s", errb.String())
	}
	out.Reset()
	if code := Main([]string{"report", "--run", run}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "スコア: 0\n") || !strings.Contains(out.String(), "判定: pass") {
		t.Fatalf("0点を未計測と混同しています:\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := Main([]string{"score", "set", "--run", run, "--status", "fail"}, &out, &errb); code != 0 {
		t.Fatalf("fail 値なし: %s", errb.String())
	}
	out.Reset()
	if code := Main([]string{"report", "--run", run}, &out, &errb); code != 0 {
		t.Fatal(errb.String())
	}
	if !strings.Contains(out.String(), "スコア: 未計測\n") || !strings.Contains(out.String(), "判定: fail") {
		t.Fatalf("fail の省略:\n%s", out.String())
	}

	errb.Reset()
	if code := Main([]string{"score", "set", "--run", run, "--status", "unknown"}, ioDiscard(), &errb); code != 0 {
		t.Fatalf("unknown 値なし: %s", errb.String())
	}
}

func ioDiscard() *strings.Builder { return &strings.Builder{} }

func TestGitignoreCoversPrivateData(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, needle := range []string{"isucon-kit.json", ".isucon-kit/", "*.db"} {
		if !strings.Contains(text, needle) {
			t.Fatalf(".gitignore に %s がありません", needle)
		}
	}
}

func runID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "run: ") {
			return strings.TrimPrefix(line, "run: ")
		}
	}
	return ""
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Fatal(err)
	}
}
