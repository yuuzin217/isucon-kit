// Package web は 127.0.0.1 向けのダッシュボードを提供する。
// 画面からの計測と収集は既定で無効で、有効化しても許可した操作だけを受け付ける。
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/hints"
	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/report"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

// Options は画面からの実行に必要な依存である。未指定なら参照専用のまま。
type Options struct {
	Config           *config.File
	ExecutionEnabled bool
	Runner           command.Runner
}

//go:embed assets/*
var assets embed.FS

const (
	displayLimit  = 20
	diffLoadLimit = 5000
)

// NewMux は GUI と参照 API のハンドラを返す。
func NewMux(st *store.Store, logger *log.Logger) http.Handler {
	return NewMuxWith(st, logger, Options{})
}

// NewMuxWith は実行機能の有無を指定してハンドラを作る。
func NewMuxWith(st *store.Store, logger *log.Logger, opt Options) http.Handler {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	s := &server{st: st, logger: logger, opt: opt}
	markInterrupted(st)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// embed 以外のファイルは開かない。.. を含むパスは静的配信へ渡さない。
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		s.route(w, r)
	})
}

// Serve は 127.0.0.1 だけで待ち受け、ctx のキャンセル後 5 秒以内に停止する。
func Serve(ctx context.Context, addr string, st *store.Store, logger *log.Logger) error {
	return ServeWith(ctx, addr, st, logger, Options{})
}

// ServeWith は実行オプション付きで 127.0.0.1 だけを待ち受ける。
func ServeWith(ctx context.Context, addr string, st *store.Store, logger *log.Logger, opt Options) error {
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		return errors.New("待受は 127.0.0.1 だけです")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ポートを開けませんでした。使用中でないか確認してください: %w", err)
	}
	srv := &http.Server{
		Handler:           NewMuxWith(st, logger, opt),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

type server struct {
	st     *store.Store
	logger *log.Logger
	opt    Options
	jobs   jobRunner
}

func (s *server) routePost(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/mode":
		s.setMode(w, r)
	case "/api/actions/remote-collect":
		s.remoteCollect(w, r)
	case "/api/actions/local-cycle":
		s.localCycle(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/api/runs/") && strings.HasSuffix(r.URL.Path, "/score") {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/runs/"), "/score")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
				return
			}
			s.postScore(w, r, id)
			return
		}
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "この API は参照専用です"})
	}
}

func (s *server) hints(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("run")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run が必要です"})
		return
	}
	if _, err := s.st.GetRun(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ヒントを計算できません"})
		return
	}
	total, err := firstEndpoint(s.st, id, "total")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ヒントを計算できません"})
		return
	}
	avg, err := firstEndpoint(s.st, id, "avg")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ヒントを計算できません"})
		return
	}
	count, err := firstEndpoint(s.st, id, "count")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ヒントを計算できません"})
		return
	}
	queries, _, err := s.st.SearchQueries(id, store.StatQuery{Sort: "total", Desc: true, Limit: 1})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ヒントを計算できません"})
		return
	}
	var query *model.QueryStat
	if len(queries) > 0 {
		query = &queries[0]
	}
	writeJSON(w, http.StatusOK, map[string]any{"hints": hints.FromLeaders(total, avg, count, query)})
}

func firstEndpoint(st *store.Store, id, sort string) (*model.EndpointStat, error) {
	rows, _, err := st.SearchEndpoints(id, store.StatQuery{Sort: sort, Desc: true, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

func splitStats(path string) (string, string, bool) {
	rest := strings.TrimPrefix(path, "/api/runs/")
	if rest == path {
		return "", "", false
	}
	id, kind, ok := strings.Cut(rest, "/")
	if !ok || id == "" || strings.Contains(kind, "/") {
		return "", "", false
	}
	if kind != "endpoints" && kind != "queries" {
		return "", "", false
	}
	return id, kind, true
}

func (s *server) stats(w http.ResponseWriter, r *http.Request, id, kind string) {
	if _, err := s.st.GetRun(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ランキングを取得できません"})
		return
	}
	q, err := parseStatQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var items any
	var total int
	if kind == "endpoints" {
		rows, n, qerr := s.st.SearchEndpoints(id, q)
		if qerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ランキングを取得できません"})
			return
		}
		if rows == nil {
			rows = []model.EndpointStat{}
		}
		items, total = rows, n
	} else {
		rows, n, qerr := s.st.SearchQueries(id, q)
		if qerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ランキングを取得できません"})
			return
		}
		if rows == nil {
			rows = []model.QueryStat{}
		}
		items, total = rows, n
	}
	order := "asc"
	if q.Desc {
		order = "desc"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"total": total, "limit": q.Limit, "offset": q.Offset, "sort": q.Sort, "order": order, "items": items,
	})
}

func parseStatQuery(r *http.Request) (store.StatQuery, error) {
	q := store.StatQuery{Sort: r.URL.Query().Get("sort"), Desc: true, Limit: 20}
	if q.Sort == "" {
		q.Sort = "total"
	}
	if q.Sort != "total" && q.Sort != "avg" && q.Sort != "count" {
		return q, errors.New("sort は total、avg、count です")
	}
	switch r.URL.Query().Get("order") {
	case "", "desc":
		q.Desc = true
	case "asc":
		q.Desc = false
	default:
		return q, errors.New("order は asc か desc です")
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			return q, errors.New("limit は 1 から 100 です")
		}
		q.Limit = n
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 1000000 {
			return q, errors.New("offset が不正です")
		}
		q.Offset = n
	}
	q.Search = strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q.Search) > 200 {
		return q, errors.New("検索文字列が長すぎます")
	}
	return q, nil
}

func (s *server) route(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.routePost(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "このメソッドは使えません"})
		return
	}
	switch r.URL.Path {
	case "/":
		s.ensureCSRF(w, r)
		s.static(w, r, "assets/index.html", "text/html; charset=utf-8")
	case "/style.css":
		s.static(w, r, "assets/style.css", "text/css; charset=utf-8")
	case "/app.js":
		s.static(w, r, "assets/app.js", "text/javascript; charset=utf-8")
	case "/healthz":
		s.health(w, r)
	case "/api/runs":
		s.runs(w, r)
	case "/api/runs/latest":
		s.latest(w, r)
	case "/api/compare":
		s.compare(w, r)
	case "/api/session":
		s.session(w, r)
	case "/api/execution":
		s.execution(w, r)
	case "/api/hints":
		s.hints(w, r)
	default:
		if id, kind, ok := splitStats(r.URL.Path); ok {
			s.stats(w, r, id, kind)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/runs/") {
			id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
			if id == "" || strings.Contains(id, "/") {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
				return
			}
			s.runByID(w, r, id)
			return
		}
		http.NotFound(w, r)
	}
}

func (s *server) static(w http.ResponseWriter, r *http.Request, name, contentType string) {
	b, err := assets.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

func (s *server) health(w http.ResponseWriter, _ *http.Request) {
	if err := s.st.Ping(); err != nil {
		s.logger.Printf("healthz: db")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) runs(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	mode := r.URL.Query().Get("mode")
	if mode != "" && mode != model.ModeLocal && mode != model.ModeRemote && mode != model.ModeLegacy && mode != "all" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode が不正です"})
		return
	}
	filter := mode
	if mode == "all" {
		filter = ""
	}
	runs, err := s.st.ListRunsFiltered(limit, filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "一覧を取得できません"})
		return
	}
	views := make([]runView, 0, len(runs))
	for _, run := range runs {
		views = append(views, toRunView(run))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"limit":   limit,
		"ranking": "created_at desc, id desc",
		"runs":    views,
	})
}

func (s *server) latest(w http.ResponseWriter, r *http.Request) {
	mode := r.URL.Query().Get("mode")
	if mode != "" && mode != model.ModeLocal && mode != model.ModeRemote && mode != model.ModeLegacy && mode != "all" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mode が不正です"})
		return
	}
	var run model.Run
	var err error
	if mode == model.ModeLocal || mode == model.ModeRemote || mode == model.ModeLegacy {
		list, lerr := s.st.ListRunsFiltered(1, mode)
		if lerr != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "最新 run を取得できません"})
			return
		}
		if len(list) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "計測がありません"})
			return
		}
		run = list[0]
	} else {
		run, err = s.st.LatestRun()
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "計測がありません"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "最新 run を取得できません"})
		return
	}
	s.writeDetail(w, r, run.ID)
}

func (s *server) runByID(w http.ResponseWriter, r *http.Request, id string) {
	s.writeDetail(w, r, id)
}

func (s *server) writeDetail(w http.ResponseWriter, r *http.Request, id string) {
	limit := displayLimit
	if r != nil {
		if raw := r.URL.Query().Get("stats_limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 2000 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "stats_limit は 1 から 2000 です"})
				return
			}
			limit = n
		}
	}
	d, err := LoadDetail(s.st, id, limit)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "run を取得できません"})
		return
	}
	writeJSON(w, http.StatusOK, detailView(d, limit))
}

func (s *server) compare(w http.ResponseWriter, r *http.Request) {
	base := r.URL.Query().Get("base")
	head := r.URL.Query().Get("head")
	if base == "" || head == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base と head が必要です"})
		return
	}
	b, err := LoadDetail(s.st, base, diffLoadLimit)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "base の run が見つかりません"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "比較元を取得できません"})
		return
	}
	h, err := LoadDetail(s.st, head, diffLoadLimit)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "head の run が見つかりません"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "比較先を取得できません"})
		return
	}
	cmp := report.Compare(b, h)
	if b.Run.Mode != h.Run.Mode {
		cmp.EndpointsComparable = false
		cmp.EndpointsReason = "実行モードまたは過去の計測条件が異なるため、APIの改善率は出しません"
		cmp.QueriesComparable = false
		cmp.QueriesReason = "実行モードまたは過去の計測条件が異なるため、SQLの改善率は出しません"
		cmp.Endpoints = nil
		cmp.Queries = nil
	}
	writeJSON(w, http.StatusOK, cmp)
}

// LoadDetail は表示用の run 詳細を読む。スナップショットの絶対パスは含めない。
func LoadDetail(st *store.Store, id string, limit int) (report.Detail, error) {
	run, err := st.GetRun(id)
	if err != nil {
		return report.Detail{}, err
	}
	sources, err := st.Sources(id)
	if err != nil {
		return report.Detail{}, err
	}
	eps, err := st.Endpoints(id, limit)
	if err != nil {
		return report.Detail{}, err
	}
	qs, err := st.Queries(id, limit)
	if err != nil {
		return report.Detail{}, err
	}
	return report.Detail{Run: run, Sources: sources, Endpoints: eps, Queries: qs}, nil
}

func parseLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 30, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 100 {
		return 0, errors.New("limit は 1 から 100 の整数です")
	}
	return n, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

type runView struct {
	ID               string  `json:"id"`
	CreatedAt        string  `json:"created_at"`
	Label            string  `json:"label"`
	GitSHA           *string `json:"git_sha"`
	Note             string  `json:"note"`
	Score            *int    `json:"score"`
	BenchmarkStatus  string  `json:"benchmark_status"`
	CollectionStatus string  `json:"collection_status"`
	AnalysisStatus   string  `json:"analysis_status"`
	CollectionScope  string  `json:"collection_scope"`
	AccessAnalysis   string  `json:"access_analysis"`
	SlowAnalysis     string  `json:"slow_analysis"`
	Mode             string  `json:"mode"`
}

func toRunView(run model.Run) runView {
	return runView{
		ID: run.ID, CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339), Label: run.Label, GitSHA: run.GitSHA,
		Note: run.Note, Score: run.Score, BenchmarkStatus: run.BenchmarkStatus, CollectionStatus: run.CollectionStatus,
		AnalysisStatus: run.AnalysisStatus, CollectionScope: run.CollectionScope, AccessAnalysis: run.AccessAnalysis,
		SlowAnalysis: run.SlowAnalysis, Mode: run.Mode,
	}
}

type sourceView struct {
	Host           string `json:"host"`
	Kind           string `json:"kind"`
	Format         string `json:"format"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	SizeBytes      int64  `json:"size_bytes"`
	Truncated      bool   `json:"truncated"`
	AnalysisStatus string `json:"analysis_status"`
	AnalysisError  string `json:"analysis_error,omitempty"`
}

func detailView(d report.Detail, limit int) map[string]any {
	sources := make([]sourceView, 0, len(d.Sources))
	for _, src := range d.Sources {
		sources = append(sources, sourceView{
			Host: src.HostName, Kind: src.Kind, Format: src.Format, Status: src.Status, Error: src.Error,
			SizeBytes: src.SizeBytes, Truncated: src.Truncated, AnalysisStatus: src.AnalysisStatus, AnalysisError: src.AnalysisError,
		})
	}
	if d.Endpoints == nil {
		d.Endpoints = []model.EndpointStat{}
	}
	if d.Queries == nil {
		d.Queries = []model.QueryStat{}
	}
	return map[string]any{
		"run":            toRunView(d.Run),
		"timing_basis":   d.Run.TimingBasis,
		"sql_cumulative": report.HasPGStat(d.Sources),
		"analysis_error": d.Run.AnalysisError,
		"ranking":        "total_seconds desc",
		"endpoint_limit": limit,
		"query_limit":    limit,
		"sources":        sources,
		"endpoints":      d.Endpoints,
		"queries":        d.Queries,
	}
}
