package alp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCapturedTSV(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "alp-v1.0.22-json.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseTSV(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatalf("行数 %d", len(rows))
	}
	var sum float64
	for _, row := range rows {
		sum += row.Total
		if row.P95 == nil {
			t.Fatalf("p95 がありません: %+v", row)
		}
	}
	if sum < 0.60 || sum > 0.62 {
		t.Fatalf("合計 %v", sum)
	}
}

func TestParseRejectsMissingColumn(t *testing.T) {
	if _, err := ParseTSV([]byte("Count\tMethod\n1\tGET\n")); err == nil {
		t.Fatal("列不足を検出しませんでした")
	}
	if _, err := ParseTSV(nil); err == nil {
		t.Fatal("空出力を検出しませんでした")
	}
}
