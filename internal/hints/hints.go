// Package hints は計測値の大小だけから確認ポイントを作る。原因は断定しない。
package hints

import (
	"fmt"

	"github.com/yuuzin217/isucon-kit/internal/model"
)

// Item は画面に出す調査候補である。
type Item struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// FromStats は上位の API と SQL から確認ポイントを最大3件返す。
func FromStats(endpoints []model.EndpointStat, queries []model.QueryStat) []Item {
	var out []Item
	if ep, ok := maxBy(endpoints, func(e model.EndpointStat) float64 { return e.TotalSeconds }); ok && ep.TotalSeconds > 0 {
		out = append(out, Item{
			Title:  "合計処理時間が大きめのAPIがあります",
			Detail: fmt.Sprintf("確認ポイント: %s %s の合計処理時間は %.3f 秒です。呼び出し回数と平均のどちらが寄与しているかを見てください。原因とは断定しません。", ep.Method, ep.Route, ep.TotalSeconds),
		})
	}
	if ep, ok := maxBy(endpoints, func(e model.EndpointStat) float64 { return e.AvgSeconds }); ok && ep.AvgSeconds >= 0.05 {
		out = append(out, Item{
			Title:  "平均処理時間が長めのAPIがあります",
			Detail: fmt.Sprintf("調査候補: %s %s の平均は %.0f ミリ秒です。処理の内訳を確認する候補です。", ep.Method, ep.Route, ep.AvgSeconds*1000),
		})
	}
	if ep, ok := maxBy(endpoints, func(e model.EndpointStat) float64 { return float64(e.Count) }); ok && ep.Count >= 20 {
		out = append(out, Item{
			Title:  "呼び出し回数が多いAPIがあります",
			Detail: fmt.Sprintf("確認ポイント: %s %s は %d 回呼ばれています。回数の多さ自体は異常とは限りません。", ep.Method, ep.Route, ep.Count),
		})
	}
	if q, ok := maxBy(queries, func(q model.QueryStat) float64 { return q.TotalSeconds }); ok && q.TotalSeconds > 0 && len(out) < 3 {
		out = append(out, Item{
			Title:  "合計処理時間が大きめのSQLがあります",
			Detail: fmt.Sprintf("調査候補: %s の合計は %.3f 秒、呼び出しは %d 回です。遅い理由まではこの数値だけでは分かりません。", q.Fingerprint, q.TotalSeconds, q.Count),
		})
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func maxBy[T any](items []T, value func(T) float64) (T, bool) {
	var best T
	if len(items) == 0 {
		return best, false
	}
	best = items[0]
	bestV := value(best)
	for _, item := range items[1:] {
		if v := value(item); v > bestV {
			best, bestV = item, v
		}
	}
	return best, true
}
