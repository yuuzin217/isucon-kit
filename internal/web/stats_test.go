package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/model"
	"github.com/yuuzin217/isucon-kit/internal/store"
)

func TestRankingIsNotLimitedToTotalTop(t *testing.T) {
	st := newStore(t)
	mux := NewMux(st, nil)
	score := 1
	seed(t, st, "rank-run", &score, model.BenchmarkPass)
	eps := make([]model.EndpointStat, 501)
	queries := make([]model.QueryStat, 501)
	for i := 0; i < 501; i++ {
		eps[i] = model.EndpointStat{
			Method: "GET", Route: fmt.Sprintf("/r/%04d", i), Count: i + 1,
			TotalSeconds: float64(501 - i), AvgSeconds: float64(i+1) / 10, MaxSeconds: 1,
		}
		queries[i] = model.QueryStat{
			Fingerprint: fmt.Sprintf("select %04d", i), Count: i + 1,
			TotalSeconds: float64(501 - i), AvgSeconds: float64(i+1) / 10, MaxSeconds: 1,
		}
	}
	eps[500].Route = "/rare_avg"
	queries[500].Fingerprint = "select rare_avg"
	if err := st.ReplaceAnalysis("rank-run", store.AnalysisUpdate{
		AnalysisStatus: model.AnalysisOK, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK,
		Endpoints: eps, Queries: queries,
	}); err != nil {
		t.Fatal(err)
	}
	top, err := st.Endpoints("rank-run", 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range top {
		if ep.Route == "/rare_avg" {
			t.Fatal("合計時間の上位500件に、平均が最大の行が含まれています")
		}
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/rank-run/endpoints?sort=avg&order=desc&limit=1", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/rare_avg") || !strings.Contains(rec.Body.String(), `"total":501`) {
		t.Fatalf("平均の全体順位: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/rank-run/endpoints?sort=count&order=desc&q="+escapeQuery("rare_avg")+"&limit=10", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/rare_avg") || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("検索: %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/runs/rank-run/queries?sort=avg&order=desc&limit=1", nil))
	if !strings.Contains(rec.Body.String(), "select rare_avg") {
		t.Fatalf("SQL の全体順位: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/hints?run=rank-run", nil))
	if !strings.Contains(rec.Body.String(), "/rare_avg") {
		t.Fatalf("確認ポイントが合計上位だけを見ています: %s", rec.Body.String())
	}
}

func escapeQuery(s string) string {
	return strings.ReplaceAll(s, "_", "%5F")
}
