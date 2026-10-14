// アクセスログに集計対象の時間フィールドがあるかを、alp を呼ぶ前に確認する。
// alp は request_time が無い JSON を response_time で黙って集計することがあるため、ここでは推測で埋めない。
package analyzer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxLine = 1024 * 1024

// ValidateAccess は形式に応じた時間フィールドの存在を確認する。
// JSON は request_time、LTSV は reqtime だけを認める。
func ValidateAccess(path, format string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("アクセスログを開けません: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !utf8.ValidString(line) {
			return fmt.Errorf("%s:%d が UTF-8 ではありません。文字化けの行は推測しません", path, lineNo)
		}
		switch format {
		case "json":
			if err := validateJSONLine(line); err != nil {
				return fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
		case "ltsv":
			if err := validateLTSVLine(line); err != nil {
				return fmt.Errorf("%s:%d: %w", path, lineNo, err)
			}
		default:
			return fmt.Errorf("未対応のアクセスログ形式です: %s", format)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("アクセスログの読み取りに失敗しました: %w", err)
	}
	return nil
}

func validateJSONLine(line string) error {
	dec := json.NewDecoder(strings.NewReader(line))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return fmt.Errorf("JSON として読めません: %w", err)
	}
	rest, _ := io.ReadAll(dec.Buffered())
	if strings.TrimSpace(string(rest)) != "" {
		return fmt.Errorf("1行に複数の JSON があります")
	}
	raw, ok := obj["request_time"]
	if !ok {
		return fmt.Errorf("request_time がありません。upstream の response_time では集計しません")
	}
	if _, err := parseJSONNumber(raw); err != nil {
		return fmt.Errorf("request_time が数値ではありません: %w", err)
	}
	return nil
}

func parseJSONNumber(raw json.RawMessage) (float64, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return 0, fmt.Errorf("空です")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, err
		}
		return strconv.ParseFloat(s, 64)
	}
	return strconv.ParseFloat(string(raw), 64)
}

func validateLTSVLine(line string) error {
	var found bool
	var value string
	for _, field := range strings.Split(line, "\t") {
		k, v, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		if k == "reqtime" {
			found = true
			value = v
		}
	}
	if !found {
		return fmt.Errorf("reqtime がありません。apptime（upstream）では集計しません")
	}
	if _, err := strconv.ParseFloat(value, 64); err != nil {
		return fmt.Errorf("reqtime が数値ではありません: %s", value)
	}
	return nil
}
