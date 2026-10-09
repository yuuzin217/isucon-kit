package ptdigest

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCapturedJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "pt-query-digest-3.2.1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "SELECT * FROM items WHERE id=1") {
		t.Fatal("実リテラルを含む出力をフィクスチャにしています")
	}
	stats, err := Parse(raw, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 2 {
		t.Fatalf("%d", len(stats))
	}
	found := false
	for _, s := range stats {
		if s.Fingerprint == "select * from items where id=?" {
			found = true
			if s.Count != 2 || math.Abs(s.TotalSeconds-1.7) > 1e-9 || math.Abs(s.AvgSeconds-0.85) > 1e-9 || math.Abs(s.MaxSeconds-1.2) > 1e-9 {
				t.Fatalf("%+v", s)
			}
		}
	}
	if !found {
		t.Fatalf("%+v", stats)
	}
}

func TestParseEmptyNonEmptyFileIsError(t *testing.T) {
	if _, err := Parse(nil, true); err == nil {
		t.Fatal("空出力を 0 件とみなしました")
	}
	stats, err := Parse(nil, false)
	if err != nil || len(stats) != 0 {
		t.Fatalf("空ファイル: %v %+v", err, stats)
	}
}
