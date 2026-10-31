# isucon-kit

ISUCON の作業用ノート PC で使う、読み取り専用のログ分析ツールです。競技サーバーの Nginx アクセスログと MySQL スローログを手元に集め、[alp](https://github.com/tkuchiki/alp) と [pt-query-digest](https://docs.percona.com/percona-toolkit/pt-query-digest.html) で集計し、スコア履歴と一緒に 1 画面で見ます。

外部クラウド、Web API、CDN は使いません。ログの削除、ローテート、サービス再起動、デプロイ、SQL の実行はしません。

## 構成

- `cmd/isucon-kit` : 単一バイナリの入口
- `internal/cli` : サブコマンド
- `internal/collector` : SSH またはローカルファイルからの読み取り専用コピー
- `internal/analyzer` : alp / pt-query-digest の起動と正規化
- `internal/store` : SQLite の履歴
- `internal/web` : `127.0.0.1` の 1 画面ダッシュボード。計測と収集は既定で無効

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

`go test` は alp と pt-query-digest が PATH に無くても、同梱の機械可読出力と fake コマンドで通ります。実バイナリがある環境では、fixture に対する実ツールのテストも実行します。Chrome と Node.js は不要です。

ブラウザでの run 切替、比較、Refresh はタグ `browser_e2e` のときだけ実行します。Google Chrome（`google-chrome` または `google-chrome-stable`）と Node.js が必要です。

```sh
go test -tags browser_e2e ./internal/web/ -count=1 -timeout 120s
```

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
| `serve --port 8080` | `127.0.0.1` で 1 画面を配信する。画面からの計測と収集は `--enable-execution` か設定の `execution_enabled` のときだけ |

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

URL は `/` だけです。左のサイドバーはなく、上部に名称、実行モード、モード切替、主操作、更新を置きます。主操作は1つです。ローカル練習では「計測実行」、リモートでは「ログ収集・解析」です。

スコア欄は、現在のスコア、PASS / FAIL / 未計測、比較元のスコア、差分、増減率、計測日時です。スコアが null のときは「未計測」、0 は 0 点です。比較元が 0 点の増減率は `N/A` です。比較元に「比較しない」を選ぶと、更新してもその選択を維持します。

分析は次のタブです。

- 遅いAPI
- 遅いSQL
- スコア推移
- 計測履歴

API と SQL は、合計処理時間、平均処理時間、呼び出し回数で昇順と降順を切り替えられます。並び替えと検索は保存済みの全行に対して行い、合計時間の上位だけを対象にはしません。表示は 10 件、50 件、または全件です。全件は 100 件ずつページします。API はパス、SQL はフィンガープリントで検索します。合計処理時間の単位は秒、平均処理時間の単位はミリ秒です。

右側には実行状態、確認ポイント、直近の計測、リモートの接続先があります。狭い画面では本文の下に回ります。確認ポイントは計測値の大小だけを見ます。「調査候補」「確認ポイント」と書き、インデックス欠如や原因の断定はしません。

初期の比較対象は、同じモードで PASS かつ 0 点ではない計測です。FAIL や 0 点は自動では選びません。モードが違う計測どうしはスコア差だけを出し、API と SQL の改善率は出しません。

計測履歴の「すべての履歴」で、モード導入前の `legacy` を含む過去の run を開けます。既存の行は削除しません。データベースは起動時に schema version 2 へ上げ、`runs.mode` の既定値は `legacy` です。

## 実行モード

画面上部で「ローカル練習」と「リモート」を切り替えます。選択は SQLite の `settings` に残ります。実行中は切り替えできません。切り替えだけではコマンドは走りません。計測実行はローカル練習のときだけ、ログ収集はリモートのときだけ受け付けます。別のモードへの POST は 409 です。モードの保存と実行開始は同じロックで直列化します。

CLI の `collect` と `import` が作る run は、従来どおり `legacy` です。画面のリモート収集は `remote`、画面の計測実行は `local` として保存します。

### ローカル練習

Mac 上の Docker 練習ディレクトリなど、手元の `Makefile` があるディレクトリを設定します。画面の「計測実行」は、次の引数だけを実行します。

```text
make -C <local.workdir> cycle
```

`make` の代わりに `gmake` を指定できます。ターゲットは `cycle` だけです。シェル文字列へは連結せず、自由入力のコマンドも受け付けません。`make up` や `make down` は画面からは実行しません。

このリポジトリからは、利用者の練習用 Makefile の中身は参照できません。`cycle` が何を出力するかは練習環境側の定義です。isucon-kit は終了コードと、今回の実行で更新されたファイルだけを見ます。

実行前にスコアファイルとログの更新時刻、サイズ、内容ハッシュ、ファイル識別子を記録します。実行後に内容も更新時刻も変わっていないファイルは、前回の結果として捨てます。`make` が成功してもスコアファイルを書き直さなかった場合、前回の PASS は新しい run に保存しません。新しいスコアも新しいログも無いときは、完了ではなく失敗にし、「今回の計測結果を取得できませんでした」と出します。同じ内容でも今回の結果にするには、スコアファイルを書き直して更新時刻が新しくなるようにしてください。スコア JSON は一時ファイルへ書いてから rename すると、途中の内容を読みません。

アクセスログとスローログは、同じファイルに追記された分だけをスナップショットにします。先頭が前回の内容と一致するときだけ増分とみなします。同じ inode のまま切り詰めて、前回より大きく書き直した場合は、ファイル全体を今回のログとします。ファイルが新しい inode になった場合や、短くなった場合も全体です。コピーする長さは判定した時点で固定し、その後の追記は混ぜません。取り込んだ範囲は画面の「ログ範囲」に出します。

タイムアウトや中断では、`make` だけでなく同じプロセスグループの子プロセスも SIGKILL で止めます。標準出力と標準エラーは合わせて直近 256KiB を保持します。上限を超えても、新しい出力で表示を更新し続けます。画面に残す実行ログは、そのうち末尾 64KiB です。実行中も約 100 ミリ秒ごとにそのログを更新します。成功かどうかは、プロセスの終了コードと、更新された成果物だけで決めます。ログが流れ始めただけでは完了にしません。SSH 収集の出力上限とは別です。

1. `make -C <workdir> cycle` が終了コード 0 で終わる
2. `local.access_log` と `local.slow_log` があれば、そのローカルファイルを取り込み、alp と pt-query-digest で解析する
3. `local.score_file` があれば、次の JSON をスコアとして記録する

```json
{"score": 12850, "status": "pass"}
```

`pass` は 0 以上の `score` が必要です。`fail` と `unknown` は `score` を省略できます。ファイルが無ければスコアは未計測のままです。終了コードが 0 でも `status` が `fail` なら、実行状態は失敗とし、成功した計測とは分けます。ログを設定していない場合でも、スコアファイルがあればスコアだけの run を残します。

練習ディレクトリの例です。`isucon-kit init` の雛形にはこのブロックはありません。必要なときだけ追加します。

```json
"execution_enabled": true,
"local": {
  "workdir": "/absolute/path/to/practice",
  "make": "make",
  "target": "cycle",
  "access_log": "/absolute/path/to/practice/logs/access.log",
  "access_format": "json",
  "slow_log": "/absolute/path/to/practice/logs/mysql-slow.log",
  "score_file": "/absolute/path/to/practice/logs/score.json",
  "timeout_seconds": 900
}
```

`workdir`、ログ、スコアファイルは絶対パスです。`..` とファイルシステムのルートは拒否します。`timeout_seconds` は 0 から 1800 です。0 または未指定のときは 900 秒です。時間経過だけでは成功にしません。

### リモート

SSH できる Linux サーバーが対象です。AWS 専用ではありません。既存の `collect` と同じ読み取り専用の取得を、画面の「ログ収集・解析」から起動します。

1. SSH 接続
2. Nginx アクセスログの末尾を取得
3. MySQL スローログの末尾を取得
4. 手元で解析
5. ダッシュボードを更新

接続先が複数でも、ホストごとに結果を残します。一部だけ失敗した場合は run の収集状態を `partial` とし、画面の実行ステップにその旨を出します。全部失敗した場合は失敗です。秘密鍵の中身はブラウザへ返さず、実行ログに混ざった場合は `[secret redacted]` に置き換えます。画面の接続先一覧はホスト名とアドレスだけです。

リモートモードでは公式ベンチマーカーを自動実行しません。大会ごとの実行方法が違うため、ベンチマークは手元で実行し、計測履歴の「スコアを記録」から pass / fail / 未計測を保存します。CLI の `score set` も同じです。存在を確認できないベンチマーク API は呼びません。

```sh
isucon-kit score set --run RUN_ID --status pass --value 12850 --note "manual"
```

SSH の例です。鍵ファイルの中身は JSON に書きません。

```json
"ssh": {
  "user": "isucon",
  "identity_file": "/Users/you/.ssh/id_ed25519",
  "port": 22,
  "connect_timeout_seconds": 10
},
"hosts": [
  {
    "name": "web1",
    "address": "203.0.113.10",
    "access_logs": [{ "path": "/var/log/nginx/access.log", "format": "json" }],
    "slow_logs": [{ "path": "/var/log/mysql/mysql-slow.log", "format": "mysql" }],
    "services": ["nginx", "mysql"]
  },
  {
    "name": "web2",
    "address": "203.0.113.11",
    "access_logs": [{ "path": "/var/log/nginx/access.log", "format": "ltsv" }],
    "slow_logs": [],
    "services": ["nginx"]
  }
]
```

`ssh` は `BatchMode=yes`、`StrictHostKeyChecking=yes`、`known_hosts` 検証です。パスワードは送りません。ログ取得は `stat` と `tail` を使い、設定変更、ログ削除、再起動、DB 更新はしません。下記のPostgreSQL収集を明示的に設定したホストだけ、固定の読み取りSQLも実行します。

### PostgreSQLのSQLランキング

PostgreSQL 14以降の `pg_stat_statements` を、DBホストのSSH経由で読み取れます。MySQLの `slow_logs` とは別に、ホストへ次を追加します。同じDBを複数のアプリホストから重複収集しないでください。

```json
"postgres": {
  "database": "app",
  "user": "stats_reader",
  "sudo": false
}
```

リモートに `psql` があり、対象DBで `pg_stat_statements` が有効であることが前提です。SQL本文を参照するDBユーザーには必要に応じて `pg_read_all_stats` 権限を別途設定してください。認証はリモートのソケット認証または `.pgpass` を使います。kitの設定にはパスワードや接続文字列を保存しません。

既存の権限で必要な場合だけ `sudo: true` にすると、固定の `sudo -n -u postgres` でpsqlを起動します。既定はfalseで、任意のコマンドやsudo先ユーザーは指定できません。収集SQLは読み取り専用・10秒のstatement timeout付きです。拡張作成、権限変更、統計リセットは行いません。

「ログ収集・解析」でSQLランキングに呼出回数・合計/平均実行時間を表示します。時間はPostgreSQLのミリ秒を秒へ変換したもので、計画時間やアプリとの通信時間は含みません。SQLの文字列・数値リテラルとコメントは保存前に除去します。権限不足、不正なJSON、取得上限超過は0件成功ではなく収集失敗として残します。

これは**統計リセット以降の累積値**で、ベンチマーク1回分ではありません。画面のログ範囲にリセット時刻・収集時刻（UTC）を表示します。累積統計を含むrun間のSQL差分比較は無効にし、アクセスログ側の比較は従来どおり行います。過去の未収集runには遡及しないため、設定後に新しく収集してください。統計の定義は[PostgreSQL公式ドキュメント](https://www.postgresql.org/docs/16/pgstatstatements.html)を参照してください。

## 計測実行を有効にする

既定では画面から計測も収集も始められません。参照、モードの保存、手動のスコア記録は有効です。計測と収集を使うときは、次のどちらかにします。

```sh
isucon-kit serve --enable-execution
```

```json
"execution_enabled": true
```

待受は `127.0.0.1` だけです。状態を変える POST は、Host が `127.0.0.1` であること、Origin がある場合は `http://` とその Host が一致すること、クッキー `isucon_csrf` とヘッダー `X-CSRF-Token` が一致することを要求します。実行は同時に1つまでです。実行ログは末尾 64KiB まで保存し、ブラウザを更新しても状態を復元します。プロセスが実行中のまま止まっていた場合は、次回起動時に失敗として記録します。

## Mac での確認手順

この開発環境では macOS と、利用者の練習ディレクトリそのものは実行していません。手元では次で確認します。

```sh
brew install go alp percona-toolkit
git clone <this-repo>
cd isucon-kit
go test ./...
go build -o isucon-kit ./cmd/isucon-kit
./isucon-kit init
```

`isucon-kit.json` の `local.workdir` を練習ディレクトリの絶対パスにし、`execution_enabled` を true にするか、次で起動します。

```sh
./isucon-kit serve --enable-execution
open http://127.0.0.1:8080/
```

画面を「ローカル練習」にし、「計測実行」を押します。結果は実行状態と計測履歴に出ます。リモートを試す前に、そのサーバーへ手動で `ssh -o BatchMode=yes -o StrictHostKeyChecking=yes` できることを確認します。

ブラウザの回帰は Chrome と Node.js がある環境で次を実行します。

```sh
go test -tags browser_e2e ./internal/web/ -count=1 -timeout 120s
```

## 当日の注意

- 競技のレギュレーションと当日マニュアルが最優先です。サービス名、ログパス、ベンチマークの出力形式は設定で渡し、特定大会の環境は埋め込んでいません。
- ログはプロセス起動以降の累積であることが多いです。収集範囲とベンチマーク区間が一致するとは限りません。
- 画面からの収集は `--enable-execution` のあと、ボタンを押したときだけです。画面を開いただけでは SSH しません。
- CLI のリモート操作は `collect`、`doctor --remote`、`verify` を実行したときだけです。
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
| 画面が空 | `collect` または `import` のあと `analyze` と、必要なら `score set`。表示範囲が現在のモードだけになっていないかも見る |
| 計測実行が 403 | `--enable-execution` または `execution_enabled` が無い。ブラウザは `http://127.0.0.1:` で開く |
| 作業ディレクトリが不正 | 絶対パスか、`Makefile` があるか、`..` や `/` になっていないか |
| 実行中のまま戻らない | タイムアウトは最大 1800 秒。プロセス再起動後は失敗として残る |
| スコアが増えない | `score_file` の JSON、または画面の「スコアを記録」。0 点と未計測は別 |

## 同梱 fixture

`testdata/` はデモです。実測値ではありません。

- `nginx-access.jsonl` / `nginx-access.ltsv` : 動的 ID、複数メソッド、クエリ文字列を含む小さいログ
- `mysql-slow.log` : フィンガープリントが 2 種類になるスローログ
- `alp-v1.0.22-json.tsv` / `alp-v1.0.22-ltsv.tsv` : alp v1.0.22 の TSV
- `pt-query-digest-3.2.1.json` : pt-query-digest 3.2.1 の `json-anon`。リテラル付きの SQL は含みません
