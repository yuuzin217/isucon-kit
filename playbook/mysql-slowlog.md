# MySQL: スローログ有効化と調査クエリ

pt-query-digest（isucon-kit の analyzer）に渡すスローログを確実に取るための手順。

## スローログを有効化する

その場で効かせる（再起動で消えるので、永続化は下記）:

```sql
SET GLOBAL slow_query_log = ON;
SET GLOBAL long_query_time = 0;        -- 競技中はまず全件取る（0秒）。うるさければ 0.1 などに
SET GLOBAL slow_query_log_file = '/var/log/mysql/slow.log';
-- SHOW VARIABLES LIKE 'slow_query%';
-- SHOW VARIABLES LIKE 'long_query_time';
```

永続化（再現性チェック対策）: my.cnf（`/etc/mysql/my.cnf` や `/etc/mysql/mysql.conf.d/mysqld.cnf` など当日の構成を確認）に追記して再起動で確認する。

```ini
[mysqld]
slow_query_log = 1
long_query_time = 0
slow_query_log_file = /var/log/mysql/slow.log
```

注意: `long_query_time = 0` はログが大量に出る。本番相当の負荷ではファイル肥大と I/O に注意し、区間を区切って収集する。

## 調査の型

```sql
-- インデックスが効いているか（type=ALL はフルスキャン、key が NULL は未使用）
EXPLAIN <問題のクエリ>;

-- テーブルごとの行数と容量感
SELECT table_name, table_rows,
       ROUND((data_length + index_length) / 1024 / 1024, 1) AS mb
FROM information_schema.tables
WHERE table_schema = DATABASE()
ORDER BY data_length + index_length DESC;

-- 既存インデックスの確認
SHOW INDEX FROM <テーブル名>;

-- インデックス追加（型: WHERE の等値 + 範囲、ORDER BY の列順に合わせる）
-- ALTER TABLE <テーブル名> ADD INDEX idx_x (col1, col2);
```

## 進め方

1. pt-query-digest で総時間の大きいクエリから順に見る（回数 × 平均時間）。
2. EXPLAIN で type / key / rows を確認する。
3. インデックスで直るものはインデックス、N+1 はアプリ側で一括取得に直す。
4. 変更は SQL ファイルとして残し、初期化や再適用の手順に組み込む（再現性チェック対策）。
