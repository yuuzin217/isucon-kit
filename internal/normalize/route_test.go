package normalize

import (
	"math"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/config"
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
