// Package analyzer は収集済みスナップショットを alp と pt-query-digest で集計する。
package analyzer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/analyzer/alp"
	"github.com/yuuzin217/isucon-kit/internal/analyzer/ptdigest"
	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/normalize"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

// TimingBasis は画面とレポートに出す集計定義である。
const TimingBasis = "Nginx の request_time（JSON キー request_time / LTSV ラベル reqtime）。upstream の response_time / apptime は使いません。単位は秒です。p95 は単一の alp 行だけ採用し、複数パスや複数ホストでは合算できないため空にします。"

// Result は解析の成否である。成功側の集計は DB に残る。
type Result struct {
	Status string
}

// Analyze は run の集計を原子的に置き換える。
func Analyze(ctx context.Context, st *store.Store, cfg *config.File, runID string, runner command.Runner) (Result, error) {
	if runner == nil {
		runner = command.OSRunner{}
	}
	run, err := st.GetRun(runID)
	if err != nil {
		return Result{}, err
	}
	sources, err := st.Sources(runID)
	if err != nil {
		return Result{}, err
	}
	snapDir := filepath.Join(cfg.DataDir, "snapshots", run.ID)
	var rows []normalize.Row
	var queries []model.QueryStat
	var versions []string
	var errs []string
	accessSeen, accessOK, accessFail := false, false, false
	slowSeen, slowOK, slowFail := false, false, false

	for i := range sources {
		src := &sources[i]
		if src.Status != model.SourceOK {
			src.AnalysisStatus = "skipped"
			if src.AnalysisError == "" {
				src.AnalysisError = src.Error
			}
			if src.Kind == model.KindAccess {
				accessSeen = true
				accessFail = true
			}
			if src.Kind == model.KindSlow {
				slowSeen = true
				slowFail = true
			}
			continue
		}
		path := filepath.Join(snapDir, src.SnapshotRel)
		switch src.Kind {
		case model.KindAccess:
			accessSeen = true
			if err := ValidateAccess(path, src.Format); err != nil {
				src.AnalysisStatus = model.AnalysisFailed
				src.AnalysisError = err.Error()
				accessFail = true
				errs = append(errs, err.Error())
				continue
			}
			fileCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			got, ver, err := alp.Run(fileCtx, runner, src.Format, path)
			cancel()
			if ver != "" {
				versions = append(versions, ver)
			}
			if err != nil {
				src.AnalysisStatus = model.AnalysisFailed
				src.AnalysisError = err.Error()
				accessFail = true
				errs = append(errs, err.Error())
				continue
			}
			rows = append(rows, got...)
			src.AnalysisStatus = model.AnalysisOK
			src.AnalysisError = ""
			accessOK = true
		case model.KindSlow:
			slowSeen = true
			fileCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			got, ver, err := ptdigest.Run(fileCtx, runner, path)
			cancel()
			if ver != "" {
				versions = append(versions, ver)
			}
			if err != nil {
				src.AnalysisStatus = model.AnalysisFailed
				src.AnalysisError = err.Error()
				slowFail = true
				errs = append(errs, err.Error())
				continue
			}
			queries = append(queries, mergeQueries(got)...)
			src.AnalysisStatus = model.AnalysisOK
			src.AnalysisError = ""
			slowOK = true
		default:
			src.AnalysisStatus = model.AnalysisFailed
			src.AnalysisError = "不明なログ種別です"
		}
	}

	endpoints := normalize.Merge(rows, cfg)
	queries = mergeQueries(queries)
	accessStatus := sideStatus(accessSeen, accessOK, accessFail)
	slowStatus := sideStatus(slowSeen, slowOK, slowFail)
	status := overall(accessStatus, slowStatus)
	upd := store.AnalysisUpdate{
		AnalysisStatus: status,
		AccessAnalysis: accessStatus,
		SlowAnalysis:   slowStatus,
		AnalysisError:  strings.Join(errs, "\n"),
		TimingBasis:    TimingBasis,
		ToolVersions:   strings.Join(unique(versions), "; "),
		Sources:        sources,
		Endpoints:      endpoints,
		Queries:        queries,
	}
	if err := st.ReplaceAnalysis(runID, upd); err != nil {
		return Result{}, err
	}
	res := Result{Status: status}
	if status != model.AnalysisOK {
		return res, fmt.Errorf("解析は %s です。成功した側の集計は保存しました。%s", status, upd.AnalysisError)
	}
	return res, nil
}

func sideStatus(seen, ok, failed bool) string {
	if !seen {
		return model.AnalysisAbsent
	}
	switch {
	case ok && failed:
		return model.AnalysisPartial
	case ok:
		return model.AnalysisOK
	default:
		return model.AnalysisFailed
	}
}

func overall(access, slow string) string {
	anyFail := false
	anyOK := false
	for _, s := range []string{access, slow} {
		switch s {
		case model.AnalysisFailed:
			anyFail = true
		case model.AnalysisPartial:
			anyFail = true
			anyOK = true
		case model.AnalysisOK:
			anyOK = true
		}
	}
	switch {
	case anyFail && anyOK:
		return model.AnalysisPartial
	case anyFail:
		return model.AnalysisFailed
	default:
		return model.AnalysisOK
	}
}

func mergeQueries(in []model.QueryStat) []model.QueryStat {
	order := []string{}
	by := map[string]*model.QueryStat{}
	for _, q := range in {
		cur, ok := by[q.Fingerprint]
		if !ok {
			cp := q
			by[q.Fingerprint] = &cp
			order = append(order, q.Fingerprint)
			continue
		}
		cur.Count += q.Count
		cur.TotalSeconds += q.TotalSeconds
		if q.MaxSeconds > cur.MaxSeconds {
			cur.MaxSeconds = q.MaxSeconds
		}
	}
	out := make([]model.QueryStat, 0, len(order))
	for _, fp := range order {
		q := *by[fp]
		if q.Count > 0 {
			q.AvgSeconds = q.TotalSeconds / float64(q.Count)
		}
		out = append(out, q)
	}
	return out
}

func unique(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
