package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/pgstat"
	"github.com/yuuzin217/isucon-kit/internal/shellquote"
)

func postgresCommand(p *config.Postgres) string {
	args := []string{"env", "PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=10000", "PGCONNECT_TIMEOUT=10", "psql", "-X", "-w", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", p.User, "-d", p.Database, "-c", pgstat.Query}
	if p.Sudo {
		args = append([]string{"sudo", "-n", "-u", "postgres", "--"}, args...)
	}
	for i := range args {
		args[i] = shellquote.Single(args[i])
	}
	return strings.Join(args, " ")
}

func fetchPostgres(ctx context.Context, runner command.Runner, cfg *config.File, host config.Host, dir, runID string, idx int, now time.Time) (model.Source, manifestFile, string) {
	name := fmt.Sprintf("%s-pgstat-%d.json", sanitize(host.Name), idx)
	src := model.Source{RunID: runID, HostName: host.Name, Kind: model.KindSlow, Format: model.FormatPGStat, RemotePath: "pg_stat_statements/" + host.Postgres.Database, SnapshotRel: name, CollectedAt: now, Status: model.SourceFailed, AnalysisStatus: "skipped"}
	mf := manifestFile{Host: src.HostName, Kind: src.Kind, Format: src.Format, RemotePath: src.RemotePath, SnapshotFile: name}
	fail := func(err error) (model.Source, manifestFile, string) {
		src.Error = err.Error()
		src.AnalysisError = src.Error
		mf.Error = src.Error
		return src, mf, ""
	}
	if config.IsPlaceholder(host.Address) {
		return fail(fmt.Errorf("接続先は雛形のためSSHしません"))
	}
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.SSH.ConnectTimeoutSeconds+30)*time.Second)
	defer cancel()
	body, _, err := runner.Run(callCtx, "ssh", sshArgs(cfg.SSH, host.Address, postgresCommand(host.Postgres))...)
	if err != nil {
		return fail(fmt.Errorf("PostgreSQL統計を取得できません。SSH・psql・DB接続権限・pg_stat_statementsを確認してください: %w", err))
	}
	if int64(len(body)) > cfg.Collection.MaxBytesPerFile {
		return fail(fmt.Errorf("PostgreSQL統計が取得上限を超えています。JSONは切り詰めず失敗とします"))
	}
	snapshot, err := pgstat.Decode(body)
	if err != nil {
		return fail(err)
	}
	// Persist only sanitized SQL, never raw representative queries or connection secrets.
	body, err = json.Marshal(snapshot)
	if err != nil {
		return fail(err)
	}
	if err = writeSecret(filepath.Join(dir, name), body); err != nil {
		return fail(err)
	}
	src.Status = model.SourceOK
	src.AnalysisStatus = model.AnalysisPending
	src.SizeBytes = int64(len(body))
	src.OriginalSize = src.SizeBytes
	mf.OK = true
	mf.SizeBytes = src.SizeBytes
	mf.OriginalSize = src.SizeBytes
	return src, mf, snapshot.Scope(host.Name)
}
