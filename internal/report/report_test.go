package report

import (
	"strings"
	"testing"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

func TestScoreZeroIsNotUnmeasured(t *testing.T) {
	zero := 0
	var buf strings.Builder
	WriteText(&buf, Detail{Run: model.Run{ID: "a", BenchmarkStatus: model.BenchmarkPass, Score: &zero, AccessAnalysis: model.AnalysisAbsent, SlowAnalysis: model.AnalysisAbsent}})
	if !strings.Contains(buf.String(), "スコア: 0\n") {
		t.Fatalf("%s", buf.String())
	}
	buf.Reset()
	WriteText(&buf, Detail{Run: model.Run{ID: "b", BenchmarkStatus: model.BenchmarkUnknown, AccessAnalysis: model.AnalysisAbsent, SlowAnalysis: model.AnalysisAbsent}})
	if !strings.Contains(buf.String(), "スコア: 未計測\n") {
		t.Fatalf("%s", buf.String())
	}
}

func TestPercentZeroDivision(t *testing.T) {
	baseZero, head := 0, 10
	cmp := Compare(
		Detail{Run: model.Run{ID: "b", Score: &baseZero, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK}},
		Detail{Run: model.Run{ID: "h", Score: &head, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK}},
	)
	if cmp.ScoreDelta == nil || *cmp.ScoreDelta != 10 || cmp.ScorePercent != "N/A" {
		t.Fatalf("%+v", cmp)
	}
	base, head2 := 100, 150
	cmp = Compare(
		Detail{Run: model.Run{ID: "b", Score: &base, AccessAnalysis: model.AnalysisFailed, SlowAnalysis: model.AnalysisOK}},
		Detail{Run: model.Run{ID: "h", Score: &head2, AccessAnalysis: model.AnalysisOK, SlowAnalysis: model.AnalysisOK}},
	)
	if cmp.ScorePercent != "50.0" {
		t.Fatalf("percent %s", cmp.ScorePercent)
	}
	if cmp.EndpointsComparable {
		t.Fatal("解析失敗を比較可能にしています")
	}
}
