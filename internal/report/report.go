// Package report は run の要約と比較をテキストまたは JSON にする。
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

const topN = 20

// Detail は1 run の表示用データである。
type Detail struct {
	Run       model.Run
	Sources   []model.Source
	Endpoints []model.EndpointStat
	Queries   []model.QueryStat
}

// DiffRow は比較可能な累積時間の差分である。
type DiffRow struct {
	Method      string  `json:"method,omitempty"`
	Route       string  `json:"route,omitempty"`
	Fingerprint string  `json:"fingerprint,omitempty"`
	BaseCount   int     `json:"base_count"`
	HeadCount   int     `json:"head_count"`
	BaseTotal   float64 `json:"base_total_seconds"`
	HeadTotal   float64 `json:"head_total_seconds"`
	DeltaTotal  float64 `json:"delta_total_seconds"`
}

// CompareResult は2つの run の差分である。
type CompareResult struct {
	BaseID              string    `json:"base_id"`
	HeadID              string    `json:"head_id"`
	ScoreDelta          *int      `json:"score_delta"`
	ScorePercent        string    `json:"score_percent"`
	EndpointsComparable bool      `json:"endpoints_comparable"`
	EndpointsReason     string    `json:"endpoints_reason,omitempty"`
	QueriesComparable   bool      `json:"queries_comparable"`
	QueriesReason       string    `json:"queries_reason,omitempty"`
	Endpoints           []DiffRow `json:"endpoints"`
	Queries             []DiffRow `json:"queries"`
}

// Compare は両方に有効なスコアがあるときだけ数値差分を出す。
func Compare(base, head Detail) CompareResult {
	res := CompareResult{BaseID: base.Run.ID, HeadID: head.Run.ID, ScorePercent: "N/A"}
	if base.Run.Score != nil && head.Run.Score != nil {
		d := *head.Run.Score - *base.Run.Score
		res.ScoreDelta = &d
		if *base.Run.Score == 0 {
			res.ScorePercent = "N/A"
		} else {
			pct := float64(d) / float64(*base.Run.Score) * 100
			res.ScorePercent = fmt.Sprintf("%.1f", pct)
		}
	}
	res.EndpointsComparable, res.EndpointsReason = comparable(base.Run.AccessAnalysis, head.Run.AccessAnalysis, "アクセスログ")
	res.QueriesComparable, res.QueriesReason = comparable(base.Run.SlowAnalysis, head.Run.SlowAnalysis, "スローログ")
	if res.EndpointsComparable {
		res.Endpoints = diffEndpoints(base.Endpoints, head.Endpoints)
	}
	if res.QueriesComparable {
		res.Queries = diffQueries(base.Queries, head.Queries)
	}
	return res
}

func comparable(base, head, label string) (bool, string) {
	if base != model.AnalysisOK || head != model.AnalysisOK {
		return false, fmt.Sprintf("%sは比較不能です（base=%s, head=%s）", label, labelOf(base), labelOf(head))
	}
	return true, ""
}

func labelOf(s string) string {
	switch s {
	case model.AnalysisOK:
		return "解析済み"
	case model.AnalysisFailed:
		return "解析失敗"
	case model.AnalysisPartial:
		return "一部失敗"
	case model.AnalysisAbsent:
		return "未収集"
	case model.AnalysisPending:
		return "未解析"
	default:
		return s
	}
}

func diffEndpoints(base, head []model.EndpointStat) []DiffRow {
	type pair struct{ b, h *model.EndpointStat }
	keys := []string{}
	m := map[string]*pair{}
	touch := func(ep model.EndpointStat, headSide bool) {
		k := ep.Method + " " + ep.Route
		p, ok := m[k]
		if !ok {
			p = &pair{}
			m[k] = p
			keys = append(keys, k)
		}
		cp := ep
		if headSide {
			p.h = &cp
		} else {
			p.b = &cp
		}
	}
	for _, ep := range base {
		touch(ep, false)
	}
	for _, ep := range head {
		touch(ep, true)
	}
	var rows []DiffRow
	for _, k := range keys {
		p := m[k]
		row := DiffRow{}
		if p.b != nil {
			row.Method, row.Route = p.b.Method, p.b.Route
			row.BaseCount = p.b.Count
			row.BaseTotal = p.b.TotalSeconds
		}
		if p.h != nil {
			row.Method, row.Route = p.h.Method, p.h.Route
			row.HeadCount = p.h.Count
			row.HeadTotal = p.h.TotalSeconds
		}
		row.DeltaTotal = row.HeadTotal - row.BaseTotal
		rows = append(rows, row)
	}
	sortDiff(rows)
	if len(rows) > topN {
		rows = rows[:topN]
	}
	return rows
}

func diffQueries(base, head []model.QueryStat) []DiffRow {
	keys := []string{}
	type pair struct{ b, h *model.QueryStat }
	m := map[string]*pair{}
	for _, q := range base {
		cp := q
		m[q.Fingerprint] = &pair{b: &cp}
		keys = append(keys, q.Fingerprint)
	}
	for _, q := range head {
		p, ok := m[q.Fingerprint]
		if !ok {
			p = &pair{}
			m[q.Fingerprint] = p
			keys = append(keys, q.Fingerprint)
		}
		cp := q
		p.h = &cp
	}
	var rows []DiffRow
	for _, k := range keys {
		p := m[k]
		row := DiffRow{Fingerprint: k}
		if p.b != nil {
			row.BaseCount = p.b.Count
			row.BaseTotal = p.b.TotalSeconds
		}
		if p.h != nil {
			row.HeadCount = p.h.Count
			row.HeadTotal = p.h.TotalSeconds
		}
		row.DeltaTotal = row.HeadTotal - row.BaseTotal
		rows = append(rows, row)
	}
	sortDiff(rows)
	if len(rows) > topN {
		rows = rows[:topN]
	}
	return rows
}

func sortDiff(rows []DiffRow) {
	for i := 1; i < len(rows); i++ {
		j := i
		for j > 0 && math.Abs(rows[j].DeltaTotal) > math.Abs(rows[j-1].DeltaTotal) {
			rows[j], rows[j-1] = rows[j-1], rows[j]
			j--
		}
	}
}

// WriteText は人間向けの要約を書く。
func WriteText(w io.Writer, d Detail) {
	fmt.Fprintf(w, "run: %s\n", d.Run.ID)
	fmt.Fprintf(w, "時刻: %s\n", d.Run.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "ラベル: %s\n", emptyDash(d.Run.Label))
	fmt.Fprintf(w, "スコア: %s\n", formatScore(d.Run.Score))
	fmt.Fprintf(w, "判定: %s\n", d.Run.BenchmarkStatus)
	fmt.Fprintf(w, "収集: %s\n", d.Run.CollectionStatus)
	fmt.Fprintf(w, "収集範囲: %s\n", emptyDash(d.Run.CollectionScope))
	fmt.Fprintf(w, "解析: %s\n", d.Run.AnalysisStatus)
	fmt.Fprintf(w, "アクセスログ: %s\n", labelOf(d.Run.AccessAnalysis))
	fmt.Fprintf(w, "スローログ: %s\n", labelOf(d.Run.SlowAnalysis))
	if d.Run.AnalysisError != "" {
		fmt.Fprintf(w, "解析エラー:\n%s\n", d.Run.AnalysisError)
	}
	if d.Run.TimingBasis != "" {
		fmt.Fprintf(w, "集計定義: %s\n", d.Run.TimingBasis)
	}
	fmt.Fprintf(w, "ランキング: total_seconds 降順 上位%d件\n", topN)
	fmt.Fprintln(w, "\n[endpoints]")
	if d.Run.AccessAnalysis == model.AnalysisAbsent {
		fmt.Fprintln(w, "未収集")
	} else if d.Run.AccessAnalysis == model.AnalysisFailed {
		fmt.Fprintln(w, "解析失敗")
	} else if len(d.Endpoints) == 0 {
		fmt.Fprintln(w, "(なし)")
	} else {
		for _, ep := range d.Endpoints {
			p95 := "n/a"
			if ep.P95Seconds != nil {
				p95 = fmt.Sprintf("%.3f", *ep.P95Seconds)
			}
			fmt.Fprintf(w, "%s %s count=%d total=%.3fs avg=%.3fs max=%.3fs p95=%s\n",
				ep.Method, ep.Route, ep.Count, ep.TotalSeconds, ep.AvgSeconds, ep.MaxSeconds, p95)
		}
	}
	fmt.Fprintln(w, "\n[queries]")
	if d.Run.SlowAnalysis == model.AnalysisAbsent {
		fmt.Fprintln(w, "未収集")
	} else if d.Run.SlowAnalysis == model.AnalysisFailed {
		fmt.Fprintln(w, "解析失敗")
	} else if len(d.Queries) == 0 {
		fmt.Fprintln(w, "(なし)")
	} else {
		for _, q := range d.Queries {
			fmt.Fprintf(w, "%s count=%d total=%.3fs avg=%.3fs max=%.3fs\n",
				q.Fingerprint, q.Count, q.TotalSeconds, q.AvgSeconds, q.MaxSeconds)
		}
	}
}

// WriteJSON は機械向けの要約を書く。
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// WriteDiffText は差分のテキストを書く。
func WriteDiffText(w io.Writer, c CompareResult) {
	fmt.Fprintf(w, "base: %s\nhead: %s\n", c.BaseID, c.HeadID)
	if c.ScoreDelta == nil {
		fmt.Fprintln(w, "score delta: N/A")
	} else {
		fmt.Fprintf(w, "score delta: %d\n", *c.ScoreDelta)
	}
	fmt.Fprintf(w, "score percent: %s\n", c.ScorePercent)
	fmt.Fprintln(w, "\n[endpoints]")
	if !c.EndpointsComparable {
		fmt.Fprintln(w, c.EndpointsReason)
	} else if len(c.Endpoints) == 0 {
		fmt.Fprintln(w, "(差分なし)")
	} else {
		for _, row := range c.Endpoints {
			fmt.Fprintf(w, "%s %s base_total=%.3fs head_total=%.3fs delta=%.3fs\n",
				row.Method, row.Route, row.BaseTotal, row.HeadTotal, row.DeltaTotal)
		}
	}
	fmt.Fprintln(w, "\n[queries]")
	if !c.QueriesComparable {
		fmt.Fprintln(w, c.QueriesReason)
	} else if len(c.Queries) == 0 {
		fmt.Fprintln(w, "(差分なし)")
	} else {
		for _, row := range c.Queries {
			fmt.Fprintf(w, "%s base_total=%.3fs head_total=%.3fs delta=%.3fs\n",
				row.Fingerprint, row.BaseTotal, row.HeadTotal, row.DeltaTotal)
		}
	}
}

func formatScore(score *int) string {
	if score == nil {
		return "未計測"
	}
	return fmt.Sprintf("%d", *score)
}

func emptyDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
