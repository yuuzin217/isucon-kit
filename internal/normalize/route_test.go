package normalize

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

func testConfig(t *testing.T) *config.File {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/isucon-kit.json"
	if err := config.WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestMergeRouteQueryAndP95(t *testing.T) {
	cfg := testConfig(t)
	p1, p2, p3 := 0.1, 0.9, 0.01
	rows := []Row{
		{Method: "GET", URI: "/api/items/1?x=1", Count: 1, Total: 0.1, Max: 0.1, P95: &p1},
		{Method: "GET", URI: "/api/items/2", Count: 1, Total: 0.3, Max: 0.3, P95: &p2},
		{Method: "GET", URI: "/health", Count: 1, Total: 0.01, Max: 0.01, P95: &p3},
	}
	got := Merge(rows, cfg)
	if len(got) != 2 {
		t.Fatalf("件数 %d", len(got))
	}
	var items, health bool
	for _, ep := range got {
		switch ep.Route {
		case "/api/items/:id":
			items = true
			if ep.Count != 2 || math.Abs(ep.TotalSeconds-0.4) > 1e-9 || math.Abs(ep.AvgSeconds-0.2) > 1e-9 || math.Abs(ep.MaxSeconds-0.3) > 1e-9 {
				t.Fatalf("合算が違います: %+v", ep)
			}
			if ep.P95Seconds != nil {
				t.Fatalf("複数行の p95 を残しています: %v", *ep.P95Seconds)
			}
		case "/health":
			health = true
			if ep.P95Seconds == nil || math.Abs(*ep.P95Seconds-0.01) > 1e-9 {
				t.Fatalf("単一行の p95 を捨てています: %+v", ep)
			}
		}
	}
	if !items || !health {
		t.Fatalf("route が不足: %+v", got)
	}
}

func TestSortEndpointsOrderAndScale(t *testing.T) {
	items := []model.EndpointStat{
		{Method: "POST", Route: "/b", Count: 1, TotalSeconds: 1},
		{Method: "GET", Route: "/b", Count: 2, TotalSeconds: 5},
		{Method: "GET", Route: "/a", Count: 9, TotalSeconds: 5},
		{Method: "GET", Route: "/a", Count: 1, TotalSeconds: 5},
		{Method: "GET", Route: "/m", Count: 3, TotalSeconds: 2},
		{Method: "GET", Route: "/c", Count: 3, TotalSeconds: 2},
		{Method: "GET", Route: "/z", Count: 1, TotalSeconds: 0},
	}
	sortEndpoints(items)
	got := make([]string, len(items))
	for i, ep := range items {
		got[i] = fmt.Sprintf("%s %s %d %.0f", ep.Method, ep.Route, ep.Count, ep.TotalSeconds)
	}
	want := []string{
		"GET /a 9 5",
		"GET /b 2 5",
		"GET /a 1 5",
		"GET /c 3 2",
		"GET /m 3 2",
		"POST /b 1 1",
		"GET /z 1 0",
	}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("順位 %d: got %s want %s\n全部: %v", i, got[i], want[i], got)
		}
	}

	const n = 40000
	large := make([]model.EndpointStat, n)
	for i := 0; i < n; i++ {
		large[i] = model.EndpointStat{
			Method:       []string{"GET", "POST", "PUT"}[i%3],
			Route:        fmt.Sprintf("/id/%06d", (i*97)%n),
			Count:        (i * 13) % 50,
			TotalSeconds: float64((i * 29) % 5000),
		}
	}
	start := time.Now()
	sortEndpoints(large)
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("40000件のソートが遅すぎます: %s", elapsed)
	}
	for i := 1; i < len(large); i++ {
		if lessEndpoint(large[i], large[i-1]) {
			t.Fatalf("順位が壊れています: %d %+v %+v", i, large[i-1], large[i])
		}
	}
}

func TestMultiHostDoesNotAverageP95(t *testing.T) {
	cfg := testConfig(t)
	a, b := 0.2, 0.8
	rows := []Row{
		{Method: "GET", URI: "/health", Count: 2, Total: 0.4, Max: 0.3, P95: &a},
		{Method: "GET", URI: "/health", Count: 2, Total: 0.6, Max: 0.5, P95: &b},
	}
	got := Merge(rows, cfg)
	if len(got) != 1 {
		t.Fatal(got)
	}
	ep := got[0]
	if ep.Count != 4 || math.Abs(ep.TotalSeconds-1.0) > 1e-9 || math.Abs(ep.AvgSeconds-0.25) > 1e-9 || math.Abs(ep.MaxSeconds-0.5) > 1e-9 {
		t.Fatalf("%+v", ep)
	}
	if ep.P95Seconds != nil {
		t.Fatalf("ホスト間の p95 を平均または残しています: %v", *ep.P95Seconds)
	}
}
