// Package alp は alp の TSV 出力を解釈する。表組みの文字列は解析しない。
package alp

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/yuuzin217/isucon-kit/internal/command"
	"github.com/yuuzin217/isucon-kit/internal/normalize"
)

const (
	// InstallURL は alp の入手先である。
	InstallURL = "https://github.com/tkuchiki/alp"
	// TimingJSON は JSON ログで集計するフィールドである。
	TimingJSON = "request_time"
	// TimingLTSV は LTSV ログで集計するラベルである。Nginx の $request_time に対応する。
	TimingLTSV = "reqtime"
)

// ParseTSV は alp --format=tsv の出力を行へ変換する。
func ParseTSV(out []byte) ([]normalize.Row, error) {
	out = bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("alp の TSV 出力が空です。対応していないバージョンの可能性があります")
	}
	r := csv.NewReader(bytes.NewReader(out))
	r.Comma = '\t'
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	records, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("alp の TSV を読めません: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("alp の TSV にヘッダがありません")
	}
	col := map[string]int{}
	for i, h := range records[0] {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	for _, name := range []string{"count", "method", "uri", "sum", "avg", "max"} {
		if _, ok := col[name]; !ok {
			return nil, fmt.Errorf("alp の TSV に列 %s がありません。--output で明示してください", name)
		}
	}
	var rows []normalize.Row
	for i, rec := range records[1:] {
		if len(rec) == 1 && strings.TrimSpace(rec[0]) == "" {
			continue
		}
		row, err := parseRecord(rec, col)
		if err != nil {
			return nil, fmt.Errorf("alp TSV %d 行目: %w", i+2, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func parseRecord(rec []string, col map[string]int) (normalize.Row, error) {
	get := func(name string) (string, error) {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return "", fmt.Errorf("列 %s がありません", name)
		}
		return strings.TrimSpace(rec[i]), nil
	}
	countS, err := get("count")
	if err != nil {
		return normalize.Row{}, err
	}
	count, err := strconv.Atoi(countS)
	if err != nil {
		return normalize.Row{}, fmt.Errorf("count が整数ではありません: %s", countS)
	}
	method, err := get("method")
	if err != nil {
		return normalize.Row{}, err
	}
	uri, err := get("uri")
	if err != nil {
		return normalize.Row{}, err
	}
	sum, err := mustFloat(get, "sum")
	if err != nil {
		return normalize.Row{}, err
	}
	max, err := mustFloat(get, "max")
	if err != nil {
		return normalize.Row{}, err
	}
	row := normalize.Row{Method: method, URI: uri, Count: count, Total: sum, Max: max}
	if _, ok := col["p95"]; ok {
		raw, err := get("p95")
		if err != nil {
			return normalize.Row{}, err
		}
		if raw != "" && raw != "-" {
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return normalize.Row{}, fmt.Errorf("p95 が数値ではありません: %s", raw)
			}
			row.P95 = &v
		}
	}
	return row, nil
}

func mustFloat(get func(string) (string, error), name string) (float64, error) {
	raw, err := get(name)
	if err != nil {
		return 0, err
	}
	if raw == "" || raw == "-" {
		return 0, fmt.Errorf("%s が欠損しています。0 とはみなしません", name)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s が数値ではありません: %s", name, raw)
	}
	return v, nil
}

// Run は alp を TSV で起動する。クエリ文字列は alp 側で除外する（-q を付けない）。
func Run(ctx context.Context, runner command.Runner, format, path string) ([]normalize.Row, string, error) {
	bin, err := runner.LookPath("alp")
	if err != nil {
		return nil, "", fmt.Errorf("alp が見つかりません。%s からインストールしてください", InstallURL)
	}
	sub := format
	if sub != "json" && sub != "ltsv" {
		return nil, "", fmt.Errorf("alp のサブコマンドにできない形式です: %s", format)
	}
	args := []string{
		sub,
		"--file", path,
		"--format", "tsv",
		"--output", "count,method,uri,sum,avg,max,p95",
		"--percentiles", "95",
		"--sort", "sum",
		"--reverse",
		"--nosave-pos",
		"--limit", "100000",
	}
	stdout, stderr, runErr := runner.Run(ctx, bin, args...)
	ver := version(ctx, runner, bin)
	if runErr != nil {
		msg := strings.TrimSpace(string(stderr))
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, ver, fmt.Errorf("alp が失敗しました: %s", msg)
	}
	rows, err := ParseTSV(stdout)
	if err != nil {
		return nil, ver, err
	}
	return rows, ver, nil
}

func version(ctx context.Context, runner command.Runner, bin string) string {
	stdout, stderr, err := runner.Run(ctx, bin, "--version")
	text := strings.TrimSpace(string(stdout))
	if text == "" {
		text = strings.TrimSpace(string(stderr))
	}
	if err != nil && text == "" {
		return "alp (version unknown)"
	}
	if text == "" {
		return "alp"
	}
	return "alp " + text
}
