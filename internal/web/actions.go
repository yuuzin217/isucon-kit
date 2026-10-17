package web

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/analyzer"
	"github.com/yuuzin217/isucon-kit/internal/collector"
	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/id"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/practice"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

type jobRunner struct {
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func (j *jobRunner) busy() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running
}

func (j *jobRunner) start(parent context.Context) (context.Context, func(), error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.running {
		return nil, nil, errors.New("すでに実行中です")
	}
	ctx, cancel := context.WithCancel(parent)
	j.running = true
	j.cancel = cancel
	return ctx, func() {
		cancel()
		j.mu.Lock()
		j.running = false
		j.cancel = nil
		j.mu.Unlock()
	}, nil
}

func (s *server) session(w http.ResponseWriter, r *http.Request) {
	token := s.ensureCSRF(w, r)
	mode, _ := s.st.GetSetting("mode", model.ModeRemote)
	ex, _ := s.st.LoadExecution()
	hosts := []map[string]string{}
	if s.opt.Config != nil {
		for _, host := range s.opt.Config.Hosts {
			hosts = append(hosts, map[string]string{"name": host.Name, "address": host.Address})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"csrf":              token,
		"mode":              mode,
		"execution_enabled": s.opt.ExecutionEnabled,
		"execution":         executionView(ex),
		"local_configured":  s.opt.Config != nil && s.opt.Config.Local != nil && s.opt.Config.Local.WorkDir != "",
		"hosts":             hosts,
	})
}

func (s *server) execution(w http.ResponseWriter, _ *http.Request) {
	ex, err := s.st.LoadExecution()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "実行状態を読めません"})
		return
	}
	writeJSON(w, http.StatusOK, executionView(ex))
}

func (s *server) setMode(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	if s.jobs.busy() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "実行中はモードを変更できません"})
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON が不正です"})
		return
	}
	if body.Mode != model.ModeLocal && body.Mode != model.ModeRemote {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode は local か remote です"})
		return
	}
	if err := s.st.SetSetting("mode", body.Mode); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "モードを保存できません"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": body.Mode})
}

func (s *server) remoteCollect(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	if !s.opt.ExecutionEnabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "実行機能は無効です。serve --enable-execution または execution_enabled で有効化してください"})
		return
	}
	if s.opt.Config == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "設定が読み込まれていません"})
		return
	}
	ctx, done, err := s.jobs.start(context.Background())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	go s.runRemote(ctx, done)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": model.ExecRunning})
}

func (s *server) localCycle(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeMutation(w, r) {
		return
	}
	if !s.opt.ExecutionEnabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "実行機能は無効です。serve --enable-execution または execution_enabled で有効化してください"})
		return
	}
	if s.opt.Config == nil || s.opt.Config.Local == nil || s.opt.Config.Local.WorkDir == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "local.workdir が未設定です。練習ディレクトリは設定ファイルで指定します"})
		return
	}
	ctx, done, err := s.jobs.start(context.Background())
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	go s.runLocal(ctx, done)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": model.ExecRunning})
}

func (s *server) postScore(w http.ResponseWriter, r *http.Request, id string) {
	if !s.authorizeMutation(w, r) {
		return
	}
	var body struct {
		Value  *int   `json:"value"`
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON が不正です"})
		return
	}
	if body.Status != model.BenchmarkPass && body.Status != model.BenchmarkFail && body.Status != model.BenchmarkUnknown {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status は pass、fail、unknown です"})
		return
	}
	if body.Status == model.BenchmarkPass && body.Value == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pass には 0 以上の value が必要です"})
		return
	}
	if body.Value != nil && *body.Value < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "value は 0 以上です"})
		return
	}
	if err := s.st.SetScore(id, body.Value, body.Status, body.Note, nil); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "スコアを保存できません"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) runRemote(ctx context.Context, done func()) {
	defer done()
	now := time.Now().UTC()
	ex := store.Execution{Mode: model.ModeRemote, Status: model.ExecRunning, Step: "ログを収集しています", StartedAt: &now}
	_ = s.st.SaveExecution(ex)
	runner := s.runner()
	res, err := collector.Collect(ctx, s.st, s.opt.Config, "remote", runner)
	if res.RunID != "" {
		_ = s.st.SetRunMode(res.RunID, model.ModeRemote)
		ex.RunID = res.RunID
	}
	if err != nil && res.RunID == "" {
		failExec(&ex, "収集に失敗しました", err.Error())
		ex.Log = store.RedactLog(joinLines(res.Lines))
		_ = s.st.SaveExecution(ex)
		return
	}
	ex.Step = "ログを解析しています"
	_ = s.st.SaveExecution(ex)
	var analyzed analyzer.Result
	var analyzeErr error
	if res.RunID != "" {
		analyzed, analyzeErr = analyzer.Analyze(ctx, s.st, s.opt.Config, res.RunID, runner)
	}
	finish := time.Now().UTC()
	ex.FinishedAt = &finish
	ex.Log = store.RedactLog(joinLines(res.Lines))
	partial := res.Status == model.CollectionPartial || analyzed.Status == model.AnalysisPartial
	hardFail := res.RunID == "" || res.Status == model.CollectionFailed || analyzed.Status == model.AnalysisFailed || (analyzeErr != nil && analyzed.Status != model.AnalysisPartial)
	switch {
	case hardFail:
		ex.Status = model.ExecFailed
		ex.Step = "失敗"
		if analyzeErr != nil {
			ex.Error = analyzeErr.Error()
		} else if err != nil {
			ex.Error = err.Error()
		}
	case partial:
		ex.Status = model.ExecDone
		ex.Step = "完了（一部のログは取得できませんでした）"
		ex.Error = ""
	default:
		ex.Status = model.ExecDone
		ex.Step = "完了"
		ex.Error = ""
	}
	_ = s.st.SaveExecution(ex)
}

func (s *server) runLocal(ctx context.Context, done func()) {
	defer done()
	now := time.Now().UTC()
	ex := store.Execution{Mode: model.ModeLocal, Status: model.ExecRunning, Step: "計測コマンドを実行しています", StartedAt: &now}
	_ = s.st.SaveExecution(ex)
	local := s.opt.Config.Local
	target := local.Target
	if target == "" {
		target = "cycle"
	}
	timeout := 900 * time.Second
	if local.TimeoutSeconds > 0 {
		timeout = time.Duration(local.TimeoutSeconds) * time.Second
	}
	log, err := practice.Run(ctx, s.runner(), practice.Spec{
		WorkDir: local.WorkDir,
		Make:    local.Make,
		Target:  target,
		Timeout: timeout,
	})
	ex.Log = store.RedactLog(log)
	if err != nil {
		failExec(&ex, "失敗", err.Error())
		_ = s.st.SaveExecution(ex)
		return
	}
	ex.Step = "結果を取り込んでいます"
	_ = s.st.SaveExecution(ex)
	var problems []string
	runID := ""
	if local.AccessLog != "" || local.SlowLog != "" {
		format := local.AccessFormat
		if format == "" {
			format = model.FormatJSON
		}
		res, ierr := collector.Import(ctx, s.st, s.opt.Config, local.AccessLog, format, local.SlowLog, "local-cycle")
		if res.RunID != "" {
			runID = res.RunID
			_ = s.st.SetRunMode(runID, model.ModeLocal)
			ex.RunID = runID
			if _, aerr := analyzer.Analyze(ctx, s.st, s.opt.Config, runID, s.runner()); aerr != nil {
				problems = append(problems, aerr.Error())
			}
		}
		if ierr != nil {
			problems = append(problems, ierr.Error())
		}
	}
	rec, hasScore, serr := practice.ReadScore(local.ScoreFile)
	if serr != nil {
		failExec(&ex, "失敗", serr.Error())
		ex.Log = store.RedactLog(log)
		_ = s.st.SaveExecution(ex)
		return
	}
	if hasScore {
		var saveErr error
		if runID == "" {
			runID, saveErr = s.createLocalScoreRun(rec)
			ex.RunID = runID
		} else {
			saveErr = s.st.SetScore(runID, rec.Score, rec.Status, "", nil)
		}
		if saveErr != nil {
			problems = append(problems, saveErr.Error())
		}
	}
	finish := time.Now().UTC()
	ex.FinishedAt = &finish
	ex.Log = store.RedactLog(log)
	if hasScore && rec.Status == model.BenchmarkFail {
		problems = append(problems, "ベンチマークは失敗として記録されています。成功したスコアとは分けています")
	}
	if len(problems) > 0 {
		ex.Status = model.ExecFailed
		ex.Step = "失敗"
		ex.Error = problems[0]
	} else {
		ex.Status = model.ExecDone
		ex.Step = "完了"
		ex.Error = ""
	}
	_ = s.st.SaveExecution(ex)
}

func (s *server) createLocalScoreRun(rec practice.ScoreRecord) (string, error) {
	runID, err := id.New()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	err = s.st.CreateRun(model.Run{
		ID: runID, CreatedAt: now, Label: "local-cycle", Mode: model.ModeLocal,
		Score: rec.Score, BenchmarkStatus: rec.Status,
		CollectionStatus: model.CollectionOK, AnalysisStatus: model.AnalysisAbsent,
		AccessAnalysis: model.AnalysisAbsent, SlowAnalysis: model.AnalysisAbsent,
		CollectionScope: "ログ未設定（スコアのみ）",
	}, nil)
	if err != nil {
		return "", err
	}
	return runID, nil
}

func markInterrupted(st *store.Store) {
	ex, err := st.LoadExecution()
	if err != nil || ex.Status != model.ExecRunning {
		return
	}
	failExec(&ex, "失敗", "プロセスが停止したため、実行は完了していません")
	_ = st.SaveExecution(ex)
}

func failExec(ex *store.Execution, step, msg string) {
	now := time.Now().UTC()
	ex.Status = model.ExecFailed
	ex.Step = step
	ex.Error = msg
	ex.FinishedAt = &now
}

func (s *server) runner() command.Runner {
	if s.opt.Runner != nil {
		return s.opt.Runner
	}
	return command.OSRunner{}
}

func executionView(ex store.Execution) map[string]any {
	var elapsed int64
	if ex.StartedAt != nil {
		end := time.Now()
		if ex.FinishedAt != nil {
			end = *ex.FinishedAt
		}
		elapsed = int64(end.Sub(*ex.StartedAt).Seconds())
		if elapsed < 0 {
			elapsed = 0
		}
	}
	return map[string]any{
		"mode":            ex.Mode,
		"status":          ex.Status,
		"step":            ex.Step,
		"error":           ex.Error,
		"log":             ex.Log,
		"run_id":          ex.RunID,
		"elapsed_seconds": elapsed,
	}
}

func joinLines(lines []string) string {
	out := ""
	for _, line := range lines {
		out += line + "\n"
	}
	return out
}
