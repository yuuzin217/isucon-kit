package hints

import (
	"strings"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

func TestFromStatsDoesNotAssertCause(t *testing.T) {
	items := FromStats(
		[]model.EndpointStat{{Method: "GET", Route: "/api/items/:id", Count: 40, TotalSeconds: 3.2, AvgSeconds: 0.08}},
		[]model.QueryStat{{Fingerprint: "select * from items where id = ?", Count: 40, TotalSeconds: 2.5, AvgSeconds: 0.06}},
	)
	if len(items) == 0 {
		t.Fatal("確認ポイントがありません")
	}
	joined := ""
	for _, item := range items {
		joined += item.Title + item.Detail
	}
	for _, banned := range []string{"インデックスがありません", "SQLが原因", "原因です"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("原因を断定しています: %s", banned)
		}
	}
	if !strings.Contains(joined, "確認ポイント") && !strings.Contains(joined, "調査候補") {
		t.Fatalf("推測のラベルがありません: %s", joined)
	}
	if !strings.Contains(joined, "秒") || !strings.Contains(joined, "ミリ秒") {
		t.Fatalf("単位がありません: %s", joined)
	}
}

func TestFromStatsEmpty(t *testing.T) {
	if len(FromStats(nil, nil)) != 0 {
		t.Fatal("空データなのにヒントがあります")
	}
}
