// Package collector はログを読み取り専用でスナップショットへ保存する。
package collector

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/id"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/shellquote"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

const (
	// ScopeTail はリモートログの末尾だけを取ったことを示す。
	ScopeTail = "ログ末尾"
	// ScopeFull はローカルファイル全体を取り込んだことを示す。ベンチマーク1回分とは限らない。
	ScopeFull = "ファイル全体（ベンチマーク1回分とは限りません）"
)

// Result は作成した run の概要である。
type Result struct {
	RunID  string
	Status string
	Lines  []string
}

// Collect は設定されたホストからログの末尾を取得する。example.invalid へは接続しない。
func Collect(ctx context.Context, st *store.Store, cfg *config.File, label string, runner command.Runner) (Result, error) {
	if runner == nil {
		runner = command.OSRunner{}
	}
	if len(cfg.Hosts) == 0 {
		return Result{}, fmt.Errorf("hosts が空です。設定に収集先を書くか、import を使ってください")
	}
	runID, err := id.New()
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	dir := filepath.Join(cfg.DataDir, "snapshots", runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, fmt.Errorf("スナップショットディレクトリを作成できません: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return Result{}, err
	}
	var sources []model.Source
	var files []manifestFile
	okN, failN := 0, 0
	idx := 0
	for _, host := range cfg.Hosts {
		for _, logf := range host.AccessLogs {
			src, mf := fetchOne(ctx, runner, cfg, host, model.KindAccess, logf.Format, logf.Path, dir, runID, idx, now)
			idx++
			sources = append(sources, src)
			files = append(files, mf)
			if src.Status == model.SourceOK {
				okN++
			} else {
				failN++
			}
		}
		for _, logf := range host.SlowLogs {
			src, mf := fetchOne(ctx, runner, cfg, host, model.KindSlow, logf.Format, logf.Path, dir, runID, idx, now)
			idx++
			sources = append(sources, src)
			files = append(files, mf)
			if src.Status == model.SourceOK {
				okN++
			} else {
				failN++
			}
		}
	}
	if len(sources) == 0 {
		return Result{}, fmt.Errorf("収集対象のログが設定されていません")
	}
	return finish(st, runID, label, ScopeTail, now, sources, files, okN, failN, dir)
}

// Import はローカルログをスナップショットへコピーする。SSH は使わない。
func Import(ctx context.Context, st *store.Store, cfg *config.File, accessPath, format, slowPath, label string) (Result, error) {
	if accessPath == "" && slowPath == "" {
		return Result{}, fmt.Errorf("import には --access または --slowlog が必要です")
	}
	if accessPath != "" && format != model.FormatJSON && format != model.FormatLTSV {
		return Result{}, fmt.Errorf("--access-format は json または ltsv です")
	}
	runID, err := id.New()
	if err != nil {
		return Result{}, err
	}
	now := time.Now().UTC()
	dir := filepath.Join(cfg.DataDir, "snapshots", runID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Result{}, err
	}
	_ = os.Chmod(dir, 0o700)
	var sources []model.Source
	var files []manifestFile
	okN, failN := 0, 0
	add := func(kind, format, path, name string) {
		src, mf := copyLocal(path, kind, format, name, dir, now)
		sources = append(sources, src)
		files = append(files, mf)
		if src.Status == model.SourceOK {
			okN++
		} else {
			failN++
		}
	}
	if accessPath != "" {
		add(model.KindAccess, format, accessPath, "local-access.log")
	}
	if slowPath != "" {
		add(model.KindSlow, model.FormatMySQL, slowPath, "local-slow.log")
	}
	_ = ctx
	return finish(st, runID, label, ScopeFull, now, sources, files, okN, failN, dir)
}

func finish(st *store.Store, runID, label, scope string, now time.Time, sources []model.Source, files []manifestFile, okN, failN int, dir string) (Result, error) {
	for i := range sources {
		sources[i].RunID = runID
	}
	status := model.CollectionOK
	switch {
	case okN == 0:
		status = model.CollectionFailed
	case failN > 0:
		status = model.CollectionPartial
	}
	if anyTruncated(sources) && scope == ScopeTail {
		scope = ScopeTail + "（最大バイト数で切り詰めたファイルがあります。ベンチマーク1回分とは限りません）"
	}
	run := model.Run{
		ID:               runID,
		CreatedAt:        now,
		Label:            label,
		BenchmarkStatus:  model.BenchmarkUnknown,
		CollectionStatus: status,
		AnalysisStatus:   model.AnalysisPending,
		CollectionScope:  scope,
		AccessAnalysis:   initialSide(sources, model.KindAccess),
		SlowAnalysis:     initialSide(sources, model.KindSlow),
	}
	if err := writeManifest(dir, manifest{RunID: runID, CollectedAt: now.Format(time.RFC3339), Scope: scope, Files: files}); err != nil {
		return Result{}, err
	}
	if err := st.CreateRun(run, sources); err != nil {
		return Result{}, err
	}
	res := Result{RunID: runID, Status: status, Lines: describe(sources)}
	if status != model.CollectionOK {
		return res, fmt.Errorf("収集結果は %s です。失敗した対象は run に残しています", status)
	}
	return res, nil
}

func fetchOne(ctx context.Context, runner command.Runner, cfg *config.File, host config.Host, kind, format, remote, dir, runID string, idx int, now time.Time) (model.Source, manifestFile) {
	name := fmt.Sprintf("%s-%s-%d.log", sanitize(host.Name), kind, idx)
	src := model.Source{
		RunID:          runID,
		HostName:       host.Name,
		Kind:           kind,
		Format:         format,
		RemotePath:     remote,
		SnapshotRel:    name,
		CollectedAt:    now,
		AnalysisStatus: "skipped",
	}
	mf := manifestFile{Host: host.Name, Kind: kind, Format: format, RemotePath: remote, SnapshotFile: name}
	if config.IsPlaceholder(host.Address) {
		src.Status = model.SourceFailed
		src.Error = fmt.Sprintf("接続先 %s は雛形のため SSH しません。address を実ホストに変更してください", host.Address)
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	timeout := time.Duration(cfg.SSH.ConnectTimeoutSeconds+60) * time.Second
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	statOut, statErr, err := runner.Run(callCtx, "ssh", sshArgs(cfg.SSH, host.Address, "stat -c %s -- "+shellquote.Single(remote))...)
	if err != nil {
		src.Status = model.SourceFailed
		src.Error = sshErr("サイズ取得", statErr, err)
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	size, err := strconv.ParseInt(strings.TrimSpace(string(statOut)), 10, 64)
	if err != nil {
		src.Status = model.SourceFailed
		src.Error = fmt.Sprintf("リモートのサイズ出力を解釈できません: %q", strings.TrimSpace(string(statOut)))
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	src.OriginalSize = size
	mf.OriginalSize = size
	maxBytes := cfg.Collection.MaxBytesPerFile
	tailCtx, tailCancel := context.WithTimeout(ctx, timeout)
	defer tailCancel()
	body, tailErr, err := runner.Run(tailCtx, "ssh", sshArgs(cfg.SSH, host.Address, fmt.Sprintf("tail -c %d -- %s", maxBytes, shellquote.Single(remote)))...)
	if err != nil {
		src.Status = model.SourceFailed
		src.Error = sshErr("ログ取得", tailErr, err)
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	truncated := size > maxBytes
	if truncated {
		body = trimPartial(body, kind)
	}
	if err := writeSecret(filepath.Join(dir, name), body); err != nil {
		src.Status = model.SourceFailed
		src.Error = err.Error()
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	src.Status = model.SourceOK
	src.SizeBytes = int64(len(body))
	src.Truncated = truncated
	src.AnalysisStatus = model.AnalysisPending
	mf.OK = true
	mf.SizeBytes = src.SizeBytes
	mf.Truncated = truncated
	return src, mf
}

func copyLocal(path, kind, format, name, dir string, now time.Time) (model.Source, manifestFile) {
	src := model.Source{
		HostName:       "local",
		Kind:           kind,
		Format:         format,
		RemotePath:     path,
		SnapshotRel:    name,
		CollectedAt:    now,
		AnalysisStatus: "skipped",
	}
	mf := manifestFile{Host: "local", Kind: kind, Format: format, RemotePath: path, SnapshotFile: name}
	in, err := os.Open(path)
	if err != nil {
		src.Status = model.SourceFailed
		src.Error = fmt.Errorf("ローカルログを開けません: %w", err).Error()
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		src.Status = model.SourceFailed
		src.Error = err.Error()
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	outPath := filepath.Join(dir, name)
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		src.Status = model.SourceFailed
		src.Error = err.Error()
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	n, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		src.Status = model.SourceFailed
		src.Error = fmt.Sprintf("コピーに失敗しました: %v %v", copyErr, closeErr)
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf
	}
	_ = os.Chmod(outPath, 0o600)
	src.Status = model.SourceOK
	src.SizeBytes = n
	src.OriginalSize = info.Size()
	src.AnalysisStatus = model.AnalysisPending
	mf.OK = true
	mf.SizeBytes = n
	mf.OriginalSize = info.Size()
	return src, mf
}

func sshArgs(ssh config.SSH, address, remote string) []string {
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes",
		"-o", fmt.Sprintf("ConnectTimeout=%d", ssh.ConnectTimeoutSeconds),
		"-p", strconv.Itoa(ssh.Port),
	}
	if ssh.IdentityFile != "" {
		args = append(args, "-i", ssh.IdentityFile, "-o", "IdentitiesOnly=yes")
	}
	args = append(args, ssh.User+"@"+address, remote)
	return args
}

func sshErr(action string, stderr []byte, err error) string {
	msg := strings.TrimSpace(string(stderr))
	if msg == "" {
		msg = err.Error()
	}
	return fmt.Sprintf("%sに失敗しました（sudo は実行しません）: %s", action, msg)
}

// trimPartial は末尾取得で切れた先頭の不完全な行、スローログでは不完全なイベントを捨てる。
func trimPartial(data []byte, kind string) []byte {
	i := bytes.IndexByte(data, '\n')
	if i < 0 {
		return nil
	}
	data = data[i+1:]
	if kind != model.KindSlow {
		return data
	}
	for len(data) > 0 {
		line, rest, _ := bytes.Cut(data, []byte("\n"))
		if bytes.HasPrefix(line, []byte("# Time:")) || bytes.HasPrefix(line, []byte("# User@Host:")) {
			return data
		}
		if rest == nil {
			return nil
		}
		data = rest
	}
	return data
}

func writeSecret(path string, body []byte) error {
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return fmt.Errorf("スナップショットを書けません: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func initialSide(sources []model.Source, kind string) string {
	seen, ok := false, false
	for _, s := range sources {
		if s.Kind != kind {
			continue
		}
		seen = true
		if s.Status == model.SourceOK {
			ok = true
		}
	}
	if !seen {
		return model.AnalysisAbsent
	}
	if ok {
		return model.AnalysisPending
	}
	return model.AnalysisFailed
}

func anyTruncated(sources []model.Source) bool {
	for _, s := range sources {
		if s.Truncated {
			return true
		}
	}
	return false
}

func describe(sources []model.Source) []string {
	var lines []string
	for _, s := range sources {
		if s.Status == model.SourceOK {
			scope := "全体"
			if s.Truncated {
				scope = "末尾"
			}
			lines = append(lines, fmt.Sprintf("[ok] %s %s %s (%d bytes, %s)", s.HostName, s.Kind, s.RemotePath, s.SizeBytes, scope))
			continue
		}
		lines = append(lines, fmt.Sprintf("[ng] %s %s %s: %s", s.HostName, s.Kind, s.RemotePath, s.Error))
	}
	return lines
}

// Verify は systemctl is-active だけを実行する。再起動や sudo はしない。
func Verify(ctx context.Context, cfg *config.File, runner command.Runner) (string, error) {
	if runner == nil {
		runner = command.OSRunner{}
	}
	var b strings.Builder
	anyService := false
	failed := false
	for _, host := range cfg.Hosts {
		if len(host.Services) == 0 {
			fmt.Fprintf(&b, "%s: サービス未設定\n", host.Name)
			continue
		}
		anyService = true
		if config.IsPlaceholder(host.Address) {
			fmt.Fprintf(&b, "%s: 接続先が未設定のため確認しません\n", host.Name)
			failed = true
			continue
		}
		for _, svc := range host.Services {
			timeout := time.Duration(cfg.SSH.ConnectTimeoutSeconds+30) * time.Second
			cctx, cancel := context.WithTimeout(ctx, timeout)
			remote := "systemctl is-active -- " + shellquote.Single(svc)
			stdout, stderr, err := runner.Run(cctx, "ssh", sshArgs(cfg.SSH, host.Address, remote)...)
			cancel()
			state := strings.TrimSpace(string(stdout))
			switch state {
			case "active", "inactive", "failed", "activating", "deactivating", "reloading":
				fmt.Fprintf(&b, "%s %s: %s\n", host.Name, svc, state)
				if state != "active" {
					failed = true
				}
			default:
				msg := strings.TrimSpace(string(stderr))
				if msg == "" && err != nil {
					msg = err.Error()
				}
				if msg == "" {
					msg = "出力がありません"
				}
				fmt.Fprintf(&b, "%s %s: 確認失敗: %s\n", host.Name, svc, msg)
				failed = true
			}
		}
	}
	if !anyService {
		return "サービス未設定\n", nil
	}
	text := b.String()
	if failed {
		return text, fmt.Errorf("一部のサービスを active と確認できませんでした")
	}
	return text, nil
}

// CheckReadable は test -r だけを実行する。
func CheckReadable(ctx context.Context, cfg *config.File, address, path string, runner command.Runner) error {
	if runner == nil {
		runner = command.OSRunner{}
	}
	if config.IsPlaceholder(address) {
		return fmt.Errorf("接続先 %s は雛形のため SSH しません", address)
	}
	timeout := time.Duration(cfg.SSH.ConnectTimeoutSeconds+15) * time.Second
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, stderr, err := runner.Run(cctx, "ssh", sshArgs(cfg.SSH, address, "test -r "+shellquote.Single(path))...)
	if err != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("読み取り確認に失敗しました: %s", msg)
	}
	return nil
}

func sanitize(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "host"
	}
	return b.String()
}

type manifest struct {
	RunID       string         `json:"run_id"`
	CollectedAt string         `json:"collected_at"`
	Scope       string         `json:"scope"`
	Files       []manifestFile `json:"files"`
}

type manifestFile struct {
	Host         string `json:"host"`
	Kind         string `json:"kind"`
	Format       string `json:"format"`
	RemotePath   string `json:"remote_path"`
	SnapshotFile string `json:"snapshot_file"`
	OK           bool   `json:"ok"`
	Error        string `json:"error,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
	OriginalSize int64  `json:"original_size_bytes"`
	Truncated    bool   `json:"truncated"`
}

func writeManifest(dir string, m manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
