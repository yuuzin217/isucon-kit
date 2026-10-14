// Package shellquote はリモートシェルへ渡す単一引数をシングルクォートする。
package shellquote

import "strings"

// Single は POSIX シェルの単一引数として安全な引用符付き文字列を返す。
func Single(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
