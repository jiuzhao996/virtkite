#!/usr/bin/env bash
# 生成 Prometheus / Alertmanager 真实配置：从 .env 提取令牌注入 .example 模板。
# 产物 deploy/prometheus.yml 与 deploy/alertmanager.yml 均已 gitignore——令牌永不入库。
# 用法：仓库根目录执行 ./deploy/gen-monitor-conf.sh（install.sh 已自动挂接）。
set -euo pipefail
cd "$(dirname "$0")/.."

[ -f .env ] || { echo "[gen-monitor-conf] 缺少 .env，先复制 .env.example 并填写令牌"; exit 1; }

# 只提取需要的两个变量（不 source 整个 .env，避免值里特殊字符产生副作用）
get_var() { grep -E "^$1=" .env | head -1 | cut -d= -f2- | tr -d '\r'; }
METRICS_TOKEN=$(get_var METRICS_TOKEN)
ALERT_WEBHOOK_TOKEN=$(get_var ALERT_WEBHOOK_TOKEN)

# fail-closed：任一令牌缺失即拒绝生成——半套配置会把无认证的抓取/回推悄悄暴露出去
[ -n "$METRICS_TOKEN" ] || { echo "[gen-monitor-conf] !!! .env 缺 METRICS_TOKEN（/metrics 抓取令牌），拒绝生成"; exit 1; }
[ -n "$ALERT_WEBHOOK_TOKEN" ] || { echo "[gen-monitor-conf] !!! .env 缺 ALERT_WEBHOOK_TOKEN（告警网关令牌），拒绝生成"; exit 1; }

export METRICS_TOKEN ALERT_WEBHOOK_TOKEN
# 只替换这两个占位符，模板里其余内容原样保留
envsubst '${METRICS_TOKEN} ${ALERT_WEBHOOK_TOKEN}' < deploy/prometheus.yml.example  > deploy/prometheus.yml
envsubst '${METRICS_TOKEN} ${ALERT_WEBHOOK_TOKEN}' < deploy/alertmanager.yml.example > deploy/alertmanager.yml

echo "[gen-monitor-conf] 已生成 deploy/prometheus.yml、deploy/alertmanager.yml（令牌来自 .env，未入库）"
echo "[gen-monitor-conf] 若监控容器已在运行，请重启以加载新配置：docker compose restart prometheus alertmanager"
