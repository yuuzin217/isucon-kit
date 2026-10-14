// Package ptdigest は pt-query-digest の json-anon 出力を解釈する。
package ptdigest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

// InstallURL は pt-query-digest のドキュメントである。
const InstallURL = "https://docs.percona.com/percona-toolkit/pt-query-digest.html"

type file struct {
	Classes []class `json:"classes"`
	Global  *struct {
		QueryCount int `json:"query_count"`
	} `json:"global"`
}

type class struct {
	Fingerprint string            `json:"fingerprint"`
	QueryCount  int               `json:"query_count"`
	Metrics     map[string]metric `json:"metrics"`
}

type metric struct {
	Avg string `json:"avg"`
	Max string `json:"max"`
	Sum string `json:"sum"`
}

// Parse は json-anon の classes からフィンガープリント集計を作る。
// 例示クエリは読まない。時間は秒の数値文字列だけを認める。
func Parse(out []byte, sourceNonEmpty bool) ([]model.QueryStat, error) {
	trim := bytes.TrimSpace(out)
	if len(trim) == 0 {
		if sourceNonEmpty {
			return nil, fmt.Errorf("pt-query-digest が JSON を出力しませんでした。スローログ形式ではないか、未対応のバージョンです。0件とはみなしません")
		}
		return nil, nil
	}
	var doc file
	dec := json.NewDecoder(bytes.NewReader(trim))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("pt-query-digest の JSON を読めません: %w", err)
	}
	if doc.Classes == nil && sourceNonEmpty {
		return nil, fmt.Errorf("pt-query-digest の JSON に classes がありません。未対応の形式です")
	}
	var stats []model.QueryStat
	for i, c := range doc.Classes {
		if strings.TrimSpace(c.Fingerprint) == "" {
			return nil, fmt.Errorf("classes[%d] の fingerprint が空です", i)
		}
		if c.QueryCount < 1 {
			return nil, fmt.Errorf("classes[%d] の query_count が不正です", i)
		}
		m, ok := c.Metrics["Query_time"]
		if !ok {
			return nil, fmt.Errorf("classes[%d] に Query_time がありません", i)
		}
		sum, err := strconv.ParseFloat(m.Sum, 64)
		if err != nil {
			return nil, fmt.Errorf("Query_time.sum を秒の数値として読めません (%q)。単位付き文字列は未対応です", m.Sum)
		}
		max, err := strconv.ParseFloat(m.Max, 64)
		if err != nil {
			return nil, fmt.Errorf("Query_time.max を秒の数値として読めません (%q)", m.Max)
		}
		avg := sum / float64(c.QueryCount)
		stats = append(stats, model.QueryStat{
			Fingerprint:  c.Fingerprint,
			Count:        c.QueryCount,
			TotalSeconds: sum,
			AvgSeconds:   avg,
			MaxSeconds:   max,
		})
	}
	return stats, nil
}

// Run は pt-query-digest --output=json-anon を実行する。
func Run(ctx context.Context, runner command.Runner, path string) ([]model.QueryStat, string, error) {
	bin, err := runner.LookPath("pt-query-digest")
	if err != nil {
		return nil, "", fmt.Errorf("pt-query-digest が見つかりません。%s を参照してインストールしてください", InstallURL)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("スローログを確認できません: %w", err)
	}
	stdout, stderr, runErr := runner.Run(ctx, bin, "--output", "json-anon", path)
	ver := version(ctx, runner, bin)
	if runErr != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, ver, fmt.Errorf("pt-query-digest が失敗しました: %s", msg)
	}
	stats, err := Parse(stdout, info.Size() > 0)
	if err != nil {
		return nil, ver, err
	}
	return stats, ver, nil
}

func version(ctx context.Context, runner command.Runner, bin string) string {
	stdout, stderr, err := runner.Run(ctx, bin, "--version")
	text := strings.TrimSpace(string(stdout))
	if text == "" {
		text = strings.TrimSpace(string(stderr))
	}
	if err != nil && text == "" {
		return "pt-query-digest (version unknown)"
	}
	if text == "" {
		return "pt-query-digest"
	}
	return "pt-query-digest " + text
}
