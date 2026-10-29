#!/usr/bin/env bash
# アプリのビルド → 再起動 → 起動確認 の定型スクリプト（テンプレート）
#
# 使い方:
#   1. 下の「当日の環境に合わせて書き換える」部分を実際の構成に合わせる
#   2. chmod +x deploy.sh
#   3. 施策を入れるたびに ./deploy.sh で反映し、ベンチで効果を確認する
#
# 注意:
#   - サービス名・ビルド方法・配置先は大会/課題ごとに異なる。前提を埋め込まず必ず書き換える。
#   - DB のマイグレーションや初期化 SQL がある場合は、再起動前に適用する手順をここに足す。
set -euo pipefail

# ==== 当日の環境に合わせて書き換える ====
APP_DIR="/home/isucon/webapp/go"     # アプリのソースディレクトリ
SERVICE="isucon"                     # systemd のサービス名（systemctl list-units で確認）
HEALTH_URL="http://127.0.0.1:8080/"  # 起動確認に使う URL（ベンチ対象の入口など）
BUILD_CMD="go build -o app ."        # ビルドコマンド
# ========================================

cd "$APP_DIR"
echo "[deploy] build: $BUILD_CMD"
eval "$BUILD_CMD"

echo "[deploy] restart: $SERVICE"
sudo systemctl restart "$SERVICE"

echo "[deploy] health check: $HEALTH_URL"
for i in $(seq 1 30); do
  if curl -fs -o /dev/null "$HEALTH_URL"; then
    echo "[deploy] OK"
    exit 0
  fi
  sleep 1
done

echo "[deploy] FAILED: サービスが応答しません。journalctl -u $SERVICE -n 100 でログを確認してください" >&2
exit 1
