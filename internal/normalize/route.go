// Package normalize は alp の行を route_patterns で正規化し、合算する。
package normalize

import (
	"strings"

	"github.com/yuuzin217/isucon-kit/internal/config"
	"github.com/yuuzin217/isucon-kit/internal/model"
)

// Row は1つの alp 集計行である。複数行を同一 route へ畳むときは p95 を捨てる。
type Row struct {
	Method string
	URI    string
	Count  int
	Total  float64
	Max    float64
	P95    *float64
}

// Merge はクエリを除いたパスを正規化し、method+route で合算する。
// p95 は寄与した alp 行がちょうど1つのときだけ残す。平均してはならない。
func Merge(rows []Row, cfg *config.File) []model.EndpointStat {
	type acc struct {
		stat    model.EndpointStat
		sources int
		p95     *float64
	}
	order := []string{}
	byKey := map[string]*acc{}
	for _, row := range rows {
		if row.Count <= 0 {
			continue
		}
		path := stripQuery(row.URI)
		route := path
		if cfg != nil {
			route = cfg.ApplyRoute(path)
		}
		key := row.Method + " " + route
		a, ok := byKey[key]
		if !ok {
			a = &acc{stat: model.EndpointStat{Method: row.Method, Route: route}}
			byKey[key] = a
			order = append(order, key)
		}
		a.stat.Count += row.Count
		a.stat.TotalSeconds += row.Total
		if row.Max > a.stat.MaxSeconds {
			a.stat.MaxSeconds = row.Max
		}
		a.sources++
		if a.sources == 1 {
			a.p95 = row.P95
		} else {
			a.p95 = nil
		}
	}
	out := make([]model.EndpointStat, 0, len(order))
	for _, key := range order {
		a := byKey[key]
		if a.stat.Count > 0 {
			a.stat.AvgSeconds = a.stat.TotalSeconds / float64(a.stat.Count)
		}
		if a.sources == 1 {
			a.stat.P95Seconds = a.p95
		}
		out = append(out, a.stat)
	}
	sortEndpoints(out)
	return out
}

func stripQuery(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		return uri[:i]
	}
	return uri
}

func sortEndpoints(items []model.EndpointStat) {
	for i := 1; i < len(items); i++ {
		j := i
		for j > 0 && lessEndpoint(items[j], items[j-1]) {
			items[j], items[j-1] = items[j-1], items[j]
			j--
		}
	}
}

func lessEndpoint(a, b model.EndpointStat) bool {
	if a.TotalSeconds != b.TotalSeconds {
		return a.TotalSeconds > b.TotalSeconds
	}
	if a.Count != b.Count {
		return a.Count > b.Count
	}
	if a.Method != b.Method {
		return a.Method < b.Method
	}
	return a.Route < b.Route
}
