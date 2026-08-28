#!/usr/bin/env bash
# 验证 mcp-ops 的 query_resource_usage 通过 PromQL 拿到真实数据（需 Prometheus 已启动）
set -e
cd "$(dirname "$0")"

echo "=== 1. Prometheus 健康 ==="
curl -s http://127.0.0.1:9090/-/healthy || { echo "Prometheus 未启动"; exit 1; }
echo "OK"

echo ""
echo "=== 2. 抓取目标 ==="
curl -s http://127.0.0.1:9090/api/v1/targets | python3 -c "
import sys, json
d = json.load(sys.stdin)
for t in d['data']['activeTargets']:
    print(f\"  {t['labels'].get('instance','?')} | {t['health']} | {t.get('lastError','')}\")
"

echo ""
echo "=== 3. PromQL: node_cpu_usage_percent ==="
curl -s "http://127.0.0.1:9090/api/v1/query?query=node_cpu_usage_percent" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for r in d['data']['result']:
    print(f\"  {r['metric']} = {r['value'][1]}\")
"

echo ""
echo "=== 4. 调用 MCP 工具 query_resource_usage ==="
python3 -c "
import asyncio, json
async def main():
    from mcp.client.stdio import stdio_client, StdioServerParameters
    from mcp.client.session import ClientSession
    params = StdioServerParameters(command='../mcp-ops/mcp-ops.exe', args=[])
    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            await session.initialize()
            r = await session.call_tool('query_resource_usage', {'host':'web-01','metric':'all','time_range':'1h'})
            sc = r.structuredContent
            print('  status:', sc.get('status'))
            print('  prometheus:', sc.get('prometheus'))
            print('  fallback:', sc.get('fallback','无'))
            for name, series in sc.get('metrics', {}).items():
                if isinstance(series, list):
                    cnt = len(series)
                    pts = len(series[0].get('values',[])) if series and isinstance(series[0],dict) else 0
                    print(f'  [{name}] {cnt} 序列, 每序列 {pts} 点')
                else:
                    print(f'  [{name}] {series}')
asyncio.run(main())
"
