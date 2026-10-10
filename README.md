# isucon-kit

ISUCON の作業用ノート PC で使う、読み取り専用のログ分析ツールです。競技サーバーの Nginx アクセスログと MySQL スローログを手元に集め、[alp](https://github.com/tkuchiki/alp) と [pt-query-digest](https://docs.percona.com/percona-toolkit/pt-query-digest.html) で集計し、スコア履歴と一緒に 1 画面で見ます。

外部クラウド、Web API、CDN は使いません。ログの削除、ローテート、サービス再起動、デプロイ、SQL の実行はしません。

## 構成

- `cmd/isucon-kit` : 単一バイナリの入口
- `internal/cli` : サブコマンド
- `internal/collector` : SSH またはローカルファイルからの読み取り専用コピー
- `internal/analyzer` : alp / pt-query-digest の起動と正規化
- `internal/store` : SQLite の履歴
- `internal/web` : `127.0.0.1` の 1 画面ダッシュボード

生ログは `<data_dir>/snapshots/<run-id>/` にファイルとして残し、データベースには集計とメタデータだけを保存します。

## 動作要件

- Go 1.24 以上
- Linux または macOS
- アクセスログを解析する場合: [alp](https://github.com/tkuchiki/alp)（動作確認は v1.0.22）
- スローログを解析する場合: [pt-query-digest](https://docs.percona.com/percona-toolkit/pt-query-digest.html)（動作確認は Percona Toolkit 3.2.1 の `--output json-anon`）
- リモート収集と `verify` を使う場合: OpenSSH の `ssh`

実行中にこれらのツールをネットワークから自動インストールすることはありません。

### インストール例

macOS (Homebrew):

```sh
brew install go alp percona-toolkit
```

Debian / Ubuntu:

```sh
sudo apt-get install percona-toolkit
# alp は GitHub Releases の linux バイナリを /usr/local/bin に置きます
# https://github.com/tkuchiki/alp/releases
```

## ビルドとテスト

```sh
go build -o isucon-kit ./cmd/isucon-kit
go test ./...
go vet ./...
```

`go test` は alp と pt-query-digest が PATH に無くても、同梱の機械可読出力と fake コマンドで通ります。実バイナリがある環境では、fixture に対する実ツールのテストも実行します。

## 最短の手順

デモデータは実測ではありません。

```sh
go run ./cmd/isucon-kit init
go run ./cmd/isucon-kit doctor
go run ./cmd/isucon-kit import \
  --access testdata/nginx-access.jsonl --access-format json \
  --slowlog testdata/mysql-slow.log --label demo
# 表示された run ID を使う
go run ./cmd/isucon-kit analyze --run RUN_ID
go run ./cmd/isucon-kit score set --run RUN_ID --status pass --value 12850 --note "demo"
go run ./cmd/isucon-kit report --run RUN_ID
go run ./cmd/isucon-kit diff --base OLD_ID --head NEW_ID
go run ./cmd/isucon-kit serve
curl -fsS http://127.0.0.1:8080/healthz
```

ブラウザでは `http://127.0.0.1:8080/` だけを開きます。`Refresh` で API を再取得します。リモートへの収集は、画面を開いただけでは行いません。

LTSV は次のとおりです。

```sh
go run ./cmd/isucon-kit import \
  --access testdata/nginx-access.ltsv --access-format ltsv --label ltsv-demo
```

## コマンド

| コマンド | 内容 |
|---|---|
| `init` | `isucon-kit.json` の雛形を作る。既存ファイルは上書きしない |
| `doctor` | 設定、ディレクトリ権限、必要コマンドを確認する。`--remote` のときだけ SSH と読み取り権限を見る |
| `import` | ローカルログをスナップショットへコピーする |
| `collect --label TEXT` | 設定済みホストからログの末尾を読み取り専用で取得する |
| `analyze --run ID` | その run の集計を置き換える。再実行しても二重加算しない |
| `score set` | スコア、判定、メモ、git SHA を記録する。pass は `--value` 必須（0 以上）。fail と unknown は省略すると未計測 |
| `runs` | 新しい順の一覧 |
| `report --run ID` | 要約。`--format text` または `json` |
| `diff --base ID --head ID` | スコアと累積時間の差分 |
| `verify` | `systemctl is-active` だけを実行する |
| `serve --port 8080` | `127.0.0.1` で 1 画面を配信する |

`--config` を省略すると、カレントディレクトリの `isucon-kit.json` を読みます。`data_dir` は設定ファイルの場所を基準に解決します。

使い方の誤りは終了コード 2、収集や解析の失敗は 0 以外です。一部だけ成功した収集は `partial` と表示し、失敗したホストの理由を残します。

## 設定

追跡する例は `config.example.json` です。`isucon-kit init` が書く `isucon-kit.json` と `.isucon-kit/` は個人用なので Git に含めません。

雛形のアドレスは `example.invalid` です。このアドレスへは自動接続しません。SSH は公開鍵と `known_hosts` を使い、`BatchMode=yes` と `StrictHostKeyChecking=yes` を指定します。`StrictHostKeyChecking=no` は使いません。パスワードやトークンを設定ファイルへ書く欄はありません。

リモートのパスは絶対パスだけを許します。取得量は `collection.max_bytes_per_file`（既定 32MiB）で末尾から切ります。切れた先頭の不完全な行、スローログでは不完全なイベントを捨て、収集範囲を「ログ末尾」と表示します。ファイル全体を `import` した場合も、その内容がベンチマーク 1 回分とは限りません。

`route_patterns` はクエリ文字列を除いたパスに、上から順に最初の一致だけ適用します。

## ログ形式

集計する時間は Nginx のリクエスト処理時間です。単位は秒です。upstream の `response_time` / `apptime` が欠けた代わりに使われることはありません。欠損している場合は解析失敗です。

JSON は 1 行 1 オブジェクトで、少なくとも `method`、`uri`、数値の `request_time` が必要です。

```nginx
log_format json escape=json '{"time":"$time_iso8601",'
                            '"method":"$request_method",'
                            '"uri":"$request_uri",'
                            '"status":$status,'
                            '"body_bytes":$body_bytes_sent,'
                            '"request_time":$request_time,'
                            '"response_time":"$upstream_response_time"}';
```

LTSV は alp の既定ラベル `reqtime`（`$request_time`）を使います。

```nginx
log_format ltsv "time:$time_iso8601"
                "\tmethod:$request_method"
                "\turi:$request_uri"
                "\tstatus:$status"
                "\treqtime:$request_time"
                "\tapptime:$upstream_response_time";
```

このツールから Nginx や MySQL の設定は変更しません。スローログを有効にするかどうかは、当日のマニュアルとレギュレーションを確認してから作業してください。

alp は `--format tsv` と列を明示した `--output` で起動し、その TSV を読みます。複数 URI や複数ホストを 1 つの route に畳むとき、count と total は足し、avg は合計から計算し直します。p95 は平均しません。正しく合算できないときは空です。

pt-query-digest は `--output json-anon` だけを読みます。保存・表示するのはフィンガープリントです。SQL リテラルは保存しません。出力形式が想定と違う場合は 0 件とは扱わず、解析失敗にします。

## 画面

URL は `/` だけです。上から、選択中の run、スコアと判定と差分、遅いエンドポイント Top10、遅いクエリ Top10、履歴です。ランキングは累積秒数の降順です。スコアが null のときは `未計測`、0 は 0 点です。比較の分母が 0 の百分率は `N/A` です。

## 当日の注意

- 競技のレギュレーションと当日マニュアルが最優先です。サービス名、ログパス、ベンチマークの出力形式は設定で渡し、特定大会の環境は埋め込んでいません。
- ログはプロセス起動以降の累積であることが多いです。収集範囲とベンチマーク区間が一致するとは限りません。
- リモート操作は `collect`、`doctor --remote`、`verify` を実行したときだけです。
- root が必要なログは失敗として表示します。sudo は自動実行しません。
- 公開されている ISUCON の案内: https://isucon.net/archives/59966826.html

## トラブルシュート

| 症状 | 確認 |
|---|---|
| `alp が見つかりません` | PATH に alp があるか。`doctor` を実行する |
| `pt-query-digest が見つかりません` | Percona Toolkit を入れる |
| SSH で止まる / 失敗する | `BatchMode` のため対話しない。`known_hosts` と鍵、ユーザー、ポートを確認する。雛形の `example.invalid` のままになっていないか |
| ログの権限不足 | 読み取り専用の確認に失敗する。ツールは sudo しない |
| `request_time` / `reqtime` がない | upstream 時間では代替しない。ログ形式を直してから取り直す |
| 解析結果が 0 件と出ないで失敗する | alp や pt-query-digest の出力形式が未対応。バージョンを確認する |
| ポート使用中 | `serve --port` を空いているポートにする |
| 画面が空 | `collect` または `import` のあと `analyze` と、必要なら `score set` |

## 同梱 fixture

`testdata/` はデモです。実測値ではありません。

- `nginx-access.jsonl` / `nginx-access.ltsv` : 動的 ID、複数メソッド、クエリ文字列を含む小さいログ
- `mysql-slow.log` : フィンガープリントが 2 種類になるスローログ
- `alp-v1.0.22-json.tsv` / `alp-v1.0.22-ltsv.tsv` : alp v1.0.22 の TSV
- `pt-query-digest-3.2.1.json` : pt-query-digest 3.2.1 の `json-anon`。リテラル付きの SQL は含みません
