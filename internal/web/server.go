// Package web は 127.0.0.1 向けの参照専用ダッシュボードを提供する。
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

	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/report"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

//go:embed assets/*
var assets embed.FS

const (
	displayLimit  = 20
	diffLoadLimit = 5000
)

// NewMux は GUI と参照 API のハンドラを返す。
func NewMux(st *store.Store, logger *log.Logger) http.Handler {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	s := &server{st: st, logger: logger}
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
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		return errors.New("待受は 127.0.0.1 だけです")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("ポートを開けませんでした。使用中でないか確認してください: %w", err)
	}
	srv := &http.Server{
		Handler:           NewMux(st, logger),
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
}

func (s *server) route(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "この API は参照専用です"})
		return
	}
	switch r.URL.Path {
	case "/":
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
	default:
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
	runs, err := s.st.ListRuns(limit)
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

func (s *server) latest(w http.ResponseWriter, _ *http.Request) {
	run, err := s.st.LatestRun()
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "計測がありません"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "最新 run を取得できません"})
		return
	}
	s.writeDetail(w, run.ID)
}

func (s *server) runByID(w http.ResponseWriter, _ *http.Request, id string) {
	s.writeDetail(w, id)
}

func (s *server) writeDetail(w http.ResponseWriter, id string) {
	d, err := LoadDetail(s.st, id, displayLimit)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run が見つかりません"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "run を取得できません"})
		return
	}
	writeJSON(w, http.StatusOK, detailView(d))
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
	writeJSON(w, http.StatusOK, report.Compare(b, h))
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
}

func toRunView(run model.Run) runView {
	return runView{
		ID: run.ID, CreatedAt: run.CreatedAt.UTC().Format(time.RFC3339), Label: run.Label, GitSHA: run.GitSHA,
		Note: run.Note, Score: run.Score, BenchmarkStatus: run.BenchmarkStatus, CollectionStatus: run.CollectionStatus,
		AnalysisStatus: run.AnalysisStatus, CollectionScope: run.CollectionScope, AccessAnalysis: run.AccessAnalysis,
		SlowAnalysis: run.SlowAnalysis,
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

func detailView(d report.Detail) map[string]any {
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
		"analysis_error": d.Run.AnalysisError,
		"ranking":        "total_seconds desc",
		"endpoint_limit": displayLimit,
		"query_limit":    displayLimit,
		"sources":        sources,
		"endpoints":      d.Endpoints,
		"queries":        d.Queries,
	}
}
