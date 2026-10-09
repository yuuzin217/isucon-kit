// Package cli は isucon-kit のサブコマンドを実装する。
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/analyzer"
	"github.com/yuuzin217/isucon-kit/internal/analyzer/alp"
	"github.com/yuuzin217/isucon-kit/internal/analyzer/ptdigest"
	"github.com/yuuzin217/isucon-kit/internal/collector"
	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/report"
	"github.com/yuuzin217/isucon-kit/internal/store"
	"github.com/yuuzin217/isucon-kit/internal/web"
)

// Main は引数を処理し、プロセスの終了コードを返す。
func Main(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, helpText)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	switch args[0] {
	case "init":
		return cmdInit(args[1:], stdout, stderr)
	case "doctor":
		return cmdDoctor(ctx, args[1:], stdout, stderr)
	case "collect":
		return cmdCollect(ctx, args[1:], stdout, stderr)
	case "import":
		return cmdImport(ctx, args[1:], stdout, stderr)
	case "analyze":
		return cmdAnalyze(ctx, args[1:], stdout, stderr)
	case "score":
		return cmdScore(args[1:], stdout, stderr)
	case "runs":
		return cmdRuns(args[1:], stdout, stderr)
	case "report":
		return cmdReport(args[1:], stdout, stderr)
	case "diff":
		return cmdDiff(args[1:], stdout, stderr)
	case "verify":
		return cmdVerify(ctx, args[1:], stdout, stderr)
	case "serve":
		return cmdServe(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "不明なサブコマンドです: %s\n%s", args[0], helpText)
		return 2
	}
}

const helpText = `isucon-kit は ISUCON 用の読み取り専用ログ分析ツールです。

使い方:
  isucon-kit init [--config ./isucon-kit.json]
  isucon-kit doctor [--config ./isucon-kit.json] [--remote]
  isucon-kit import --access access.log --access-format json [--slowlog slow.log] [--label demo]
  isucon-kit analyze --run RUN_ID
  isucon-kit score set --run RUN_ID --status pass --value 12850 [--note text] [--git-sha SHA]
  isucon-kit report --run RUN_ID [--format text|json]
  isucon-kit diff --base RUN_ID --head RUN_ID [--format text|json]
  isucon-kit runs
  isucon-kit serve [--port 8080]
  isucon-kit collect --label baseline
  isucon-kit verify

--config を省略するとカレントディレクトリの isucon-kit.json を読みます。
serve は 127.0.0.1 だけを待ち受けます。ログの削除、再起動、SQL 実行はしません。
`

func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "余分な引数があります。\n例: isucon-kit init --config ./isucon-kit.json\n")
		return 2
	}
	if err := config.WriteTemplate(*configPath); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "設定の雛形を書きました: %s\n接続先は example.invalid です。実ホストへは自動接続しません。\n", *configPath)
	return 0
}

func cmdDoctor(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	remote := fs.Bool("remote", false, "SSH と読み取り権限を確認する")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	ok := true
	if err != nil {
		fmt.Fprintf(stdout, "[NG] 設定: %s\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "[OK] 設定: %s\n", *configPath)
	if err := checkDataDir(cfg.DataDir); err != nil {
		fmt.Fprintf(stdout, "[NG] データディレクトリ: %s\n", err)
		ok = false
	} else {
		fmt.Fprintf(stdout, "[OK] データディレクトリ: %s\n", cfg.DataDir)
	}
	runner := command.OSRunner{}
	if cfg.NeedsAlp() {
		if _, err := runner.LookPath("alp"); err != nil {
			fmt.Fprintf(stdout, "[NG] alp: 見つかりません。%s\n", alp.InstallURL)
			ok = false
		} else {
			fmt.Fprintln(stdout, "[OK] alp")
		}
	} else {
		fmt.Fprintln(stdout, "[OK] alp: アクセスログ未設定のため不要")
	}
	if cfg.NeedsPT() {
		if _, err := runner.LookPath("pt-query-digest"); err != nil {
			fmt.Fprintf(stdout, "[NG] pt-query-digest: 見つかりません。%s\n", ptdigest.InstallURL)
			ok = false
		} else {
			fmt.Fprintln(stdout, "[OK] pt-query-digest")
		}
	} else {
		fmt.Fprintln(stdout, "[OK] pt-query-digest: スローログ未設定のため不要")
	}
	if cfg.NeedsSSH() {
		if _, err := exec.LookPath("ssh"); err != nil {
			fmt.Fprintln(stdout, "[NG] ssh: 見つかりません")
			ok = false
		} else {
			fmt.Fprintln(stdout, "[OK] ssh")
		}
	} else {
		fmt.Fprintln(stdout, "[OK] ssh: 実接続先がないため未確認")
	}
	if *remote {
		for _, host := range cfg.Hosts {
			if config.IsPlaceholder(host.Address) {
				fmt.Fprintf(stdout, "[NG] remote %s: 接続先 %s は雛形のため SSH しません\n", host.Name, host.Address)
				ok = false
				continue
			}
			for _, logf := range host.AccessLogs {
				if err := collector.CheckReadable(ctx, cfg, host.Address, logf.Path, runner); err != nil {
					fmt.Fprintf(stdout, "[NG] remote %s %s: %s\n", host.Name, logf.Path, err)
					ok = false
				} else {
					fmt.Fprintf(stdout, "[OK] remote %s %s\n", host.Name, logf.Path)
				}
			}
			for _, logf := range host.SlowLogs {
				if err := collector.CheckReadable(ctx, cfg, host.Address, logf.Path, runner); err != nil {
					fmt.Fprintf(stdout, "[NG] remote %s %s: %s\n", host.Name, logf.Path, err)
					ok = false
				} else {
					fmt.Fprintf(stdout, "[OK] remote %s %s\n", host.Name, logf.Path)
				}
			}
		}
	}
	if !ok {
		fmt.Fprintln(stderr, "doctor: 失敗した項目があります")
		return 1
	}
	return 0
}

func checkDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("権限を 0700 にできません: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("権限が広すぎます: %o", info.Mode().Perm())
	}
	return nil
}

func cmdCollect(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("collect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	label := fs.String("label", "", "ラベル")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	res, err := collector.Collect(ctx, st, cfg, *label, command.OSRunner{})
	if res.RunID != "" {
		fmt.Fprintf(stdout, "run: %s\nstatus: %s\n", res.RunID, res.Status)
		for _, line := range res.Lines {
			fmt.Fprintln(stdout, line)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func cmdImport(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	access := fs.String("access", "", "アクセスログ")
	format := fs.String("access-format", "", "json または ltsv")
	slow := fs.String("slowlog", "", "スローログ")
	label := fs.String("label", "", "ラベル")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *access == "" && *slow == "" {
		fmt.Fprintln(stderr, "例: isucon-kit import --access testdata/nginx-access.jsonl --access-format json --slowlog testdata/mysql-slow.log")
		return 2
	}
	if *access != "" && *format != "json" && *format != "ltsv" {
		fmt.Fprintln(stderr, "--access-format は json または ltsv です")
		return 2
	}
	cfg, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	res, err := collector.Import(ctx, st, cfg, *access, *format, *slow, *label)
	if res.RunID != "" {
		fmt.Fprintf(stdout, "run: %s\nstatus: %s\n", res.RunID, res.Status)
		for _, line := range res.Lines {
			fmt.Fprintln(stdout, line)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func cmdAnalyze(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	runID := fs.String("run", "", "run ID")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *runID == "" {
		fmt.Fprintln(stderr, "例: isucon-kit analyze --run RUN_ID")
		return 2
	}
	cfg, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	res, err := analyzer.Analyze(ctx, st, cfg, *runID, command.OSRunner{})
	fmt.Fprintf(stdout, "run: %s\nanalysis: %s\n", *runID, res.Status)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func cmdScore(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "set" {
		fmt.Fprintln(stderr, "例: isucon-kit score set --run RUN_ID --status pass --value 12850")
		return 2
	}
	fs := flag.NewFlagSet("score set", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	runID := fs.String("run", "", "run ID")
	status := fs.String("status", "", "pass, fail, unknown")
	value := fs.String("value", "", "非負整数。省略すると未計測")
	note := fs.String("note", "", "メモ")
	gitSHA := fs.String("git-sha", "", "記録するコミット SHA")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *runID == "" || (*status != model.BenchmarkPass && *status != model.BenchmarkFail && *status != model.BenchmarkUnknown) {
		fmt.Fprintln(stderr, "--run と --status pass|fail|unknown が必要です")
		return 2
	}
	var score *int
	if *value != "" {
		n, err := strconv.Atoi(*value)
		if err != nil || n < 0 {
			fmt.Fprintln(stderr, "--value は 0 以上の整数です。未計測は --value を省略してください")
			return 2
		}
		score = &n
	}
	var sha *string
	if *gitSHA != "" {
		sha = gitSHA
	}
	_, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	if err := st.SetScore(*runID, score, *status, *note, sha); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "run: %s\nscore: %s\nstatus: %s\n", *runID, formatOpt(score), *status)
	return 0
}

func cmdRuns(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	_, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	runs, err := st.ListRuns(100)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "run はありません")
		return 0
	}
	for _, run := range runs {
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", run.CreatedAt.UTC().Format(time.RFC3339), run.ID, run.Label, formatOpt(run.Score), run.BenchmarkStatus)
	}
	return 0
}

func cmdReport(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	runID := fs.String("run", "", "run ID")
	format := fs.String("format", "text", "text または json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *runID == "" || (*format != "text" && *format != "json") {
		fmt.Fprintln(stderr, "例: isucon-kit report --run RUN_ID --format text")
		return 2
	}
	_, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	detail, err := web.LoadDetail(st, *runID, 20)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *format == "json" {
		if err := report.WriteJSON(stdout, detailView(detail)); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	report.WriteText(stdout, detail)
	return 0
}

func cmdDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	base := fs.String("base", "", "比較元")
	head := fs.String("head", "", "比較先")
	format := fs.String("format", "text", "text または json")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *base == "" || *head == "" || (*format != "text" && *format != "json") {
		fmt.Fprintln(stderr, "例: isucon-kit diff --base OLD --head NEW")
		return 2
	}
	_, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	b, err := web.LoadDetail(st, *base, 5000)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	h, err := web.LoadDetail(st, *head, 5000)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cmp := report.Compare(b, h)
	if *format == "json" {
		if err := report.WriteJSON(stdout, cmp); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	report.WriteDiffText(stdout, cmp)
	return 0
}

func cmdVerify(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	text, err := collector.Verify(ctx, cfg, command.OSRunner{})
	fmt.Fprint(stdout, text)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func cmdServe(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "isucon-kit.json", "設定ファイル")
	port := fs.Int("port", 8080, "待受ポート")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *port < 1 || *port > 65535 {
		fmt.Fprintln(stderr, "--port は 1 から 65535 の整数です")
		return 2
	}
	_, st, code := openStore(*configPath, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	fmt.Fprintf(stdout, "http://%s/ を待受中です。Ctrl+C で停止します。\n", addr)
	logger := log.New(stderr, "", 0)
	if err := web.Serve(ctx, addr, st, logger); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func openStore(path string, stderr io.Writer) (*config.File, *store.Store, int) {
	cfg, err := config.Load(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, nil, 1
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, nil, 1
	}
	return cfg, st, 0
}

func formatOpt(score *int) string {
	if score == nil {
		return "未計測"
	}
	return fmt.Sprintf("%d", *score)
}

func detailView(d report.Detail) map[string]any {
	return map[string]any{
		"run":               d.Run.ID,
		"created_at":        d.Run.CreatedAt.UTC().Format(time.RFC3339),
		"label":             d.Run.Label,
		"score":             d.Run.Score,
		"benchmark_status":  d.Run.BenchmarkStatus,
		"collection_status": d.Run.CollectionStatus,
		"collection_scope":  d.Run.CollectionScope,
		"analysis_status":   d.Run.AnalysisStatus,
		"access_analysis":   d.Run.AccessAnalysis,
		"slow_analysis":     d.Run.SlowAnalysis,
		"analysis_error":    d.Run.AnalysisError,
		"timing_basis":      d.Run.TimingBasis,
		"ranking":           "total_seconds desc",
		"endpoints":         d.Endpoints,
		"queries":           d.Queries,
	}
}
