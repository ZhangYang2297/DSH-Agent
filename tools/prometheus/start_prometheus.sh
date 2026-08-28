#!/usr/bin/env bash
# 启动本地 Prometheus（抓取 mcp-ops 自研 exporter 的 /metrics）
# 用法：bash start_prometheus.sh
# 前置：mcp-ops exporter 已在 127.0.0.1:9100 运行（见 tools/mcp-ops/exporter 说明）
set -e
cd "$(dirname "$0")"

PROM_BIN="./prometheus-bin/prometheus.exe"
if [ ! -f "$PROM_BIN" ]; then
  echo "错误：未找到 $PROM_BIN，请先运行 download_prometheus.sh 或手动解压 Prometheus 到 prometheus-bin/"
  exit 1
fi

echo "=== 启动 Prometheus (127.0.0.1:9090) ==="
"$PROM_BIN" --config.file=prometheus.yml --web.listen-address=127.0.0.1:9090 --storage.tsdb.path=data &
PROM_PID=$!
echo "prometheus PID=$PROM_PID"

echo ""
echo "服务已后台启动："
echo "  Prometheus UI:    http://127.0.0.1:9090"
echo "  mcp-ops exporter: http://127.0.0.1:9100/metrics（需另开终端启动）"
echo ""
echo "等待 20s 让 Prometheus 完成首次抓取..."
sleep 20

echo "=== 验证抓取目标 ==="
curl -s http://127.0.0.1:9090/api/v1/targets | python3 -c "
import sys, json
d = json.load(sys.stdin)
for t in d['data']['activeTargets']:
    print(f\"{t['labels'].get('instance','?')} | {t['health']} | {t.get('lastError','')}\")
"

echo ""
echo "=== 验证指标存在 ==="
curl -s "http://127.0.0.1:9090/api/v1/query?query=node_cpu_usage_percent" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for r in d['data']['result']:
    print(f\"node_cpu_usage_percent{{instance={r['metric'].get('instance')}}} = {r['value'][1]}\")
"
