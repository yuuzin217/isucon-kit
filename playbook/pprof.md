# Go: pprof の使い方

ログ解析で「どのエンドポイントが遅いか」が分かったら、次はアプリ内部のどこが重いかを見る。Go なら `net/http/pprof` が最速の手段。

## 仕込み方

既存の Web サーバーとは別ポートで、localhost だけに晒すのが安全。

```go
package main

import (
	"log"
	"net/http"
	_ "net/http/pprof" // ハンドラ登録のための副作用 import
)

func init() {
	go func() {
		// 競技サーバー外から直接見られないよう 127.0.0.1 に限定する
		log.Println(http.ListenAndServe("127.0.0.1:6060", nil))
	}()
}
```

Echo などフレームワークを使っている場合も、標準の `http.DefaultServeMux` に登録されるので上記の別ポート方式がそのまま使える。

## 見る

```bash
# CPU プロファイル（30秒間サンプリング。ベンチ実行中に取る）
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# ヒープ（メモリ確保の発生源）
go tool pprof http://localhost:6060/debug/pprof/heap

# ブラウザで一覧（flame graph 的な表示は Graphviz が必要）
# http://localhost:6060/debug/pprof/
```

pprof の中では `top`（累積の大きい順）、`list <関数名>`（行単位）、`web`（グラフ表示）が基本。

## 競技中の注意

- pprof のポートを 0.0.0.0 で公開しない（ベンチマーカー以外からも見えてしまう）。
- CPU プロファイルはベンチが回っている最中に取る。アイドル時に取っても何も出ない。
- プロファイル取得自体にも多少の負荷がかかる。常時有効でも実害は小さいが、最終盤の計測では頭の片隅に置く。
- 「再現性チェック」では再起動後に効いている必要があるので、pprof の組み込みはコード側（上記）に入れる。手元の go tool 実行はその場限りでよい。
