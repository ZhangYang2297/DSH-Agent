#!/usr/bin/env bash
# 验证 mcp-ops --http 模式（streamable-http MCP server，跨机部署用）
# 用法：bash verify_http.sh [addr]   默认 127.0.0.1:8000
set -e
cd "$(dirname "$0")"

ADDR="${1:-127.0.0.1:8000}"
EXE="./mcp-ops.exe"
LOG="/tmp/mcpops_http_verify.log"

# 1. 检查二进制
[ -f "$EXE" ] || { echo "错误：未找到 $EXE，请先 go build"; exit 1; }

# 2. 释放占用端口（若有）
PORT_NUM="${ADDR##*:}"
EXISTING=$(netstat -ano 2>/dev/null | grep "LISTENING" | grep ":$PORT_NUM " | awk '{print $NF}' | sort -u)
if [ -n "$EXISTING" ]; then
  echo "端口 $PORT_NUM 被占用，释放进程: $EXISTING"
  for pid in $EXISTING; do
    taskkill //F //PID "$pid" >/dev/null 2>&1 || true
  done
  sleep 1
fi

# 3. 启动 --http 模式
echo "=== 1. 启动 mcp-ops --http $ADDR ==="
"$EXE" --http "$ADDR" > "$LOG" 2>&1 &
SERVER_PID=$!
echo "  PID=$SERVER_PID"

sleep 2
if ! kill -0 "$SERVER_PID" 2>/dev/null; then
  echo "  启动失败，日志："
  cat "$LOG"
  exit 1
fi
head -1 "$LOG"

echo ""
echo "=== 2. streamable-http 连接 + 工具列表 ==="
python3 - "$ADDR" <<'PYEOF'
import asyncio, json, sys

ADDR = sys.argv[1] if len(sys.argv) > 1 else "127.0.0.1:8000"
URL = f"http://{ADDR}/mcp"

async def main():
    from mcp.client.streamable_http import streamablehttp_client
    from mcp.client.session import ClientSession
    async with streamablehttp_client(URL) as (read, write, _):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = await session.list_tools()
            print(f"  连接 {URL} OK")
            print(f"  工具数: {len(tools.tools)}")
            for t in tools.tools:
                print(f"    - {t.name}: {t.description[:40]}")

            # 3. 调用一个真实工具
            print()
            print("=== 3. 调用 query_resource_usage ===")
            r = await session.call_tool("query_resource_usage", {"host": "web-01", "metric": "cpu", "time_range": "5m"})
            sc = r.structuredContent
            print("  status:", sc.get("status"))
            print("  fallback:", sc.get("fallback", "无"))
            for name, series in sc.get("metrics", {}).items():
                if isinstance(series, list) and series:
                    pts = len(series[0].get("values", []))
                    last = series[0]["values"][-1] if pts else None
                    print(f"  [{name}] {len(series)} 序列, {pts} 点, 最新={last}")
                else:
                    print(f"  [{name}] {series}")

asyncio.run(main())
PYEOF

# 4. 收尾
echo ""
echo "=== 4. 停止服务 ==="
kill "$SERVER_PID" 2>/dev/null || true
echo "  已停止 PID $SERVER_PID"
echo ""
echo "验证完成。跨机部署提示："
echo "  - 服务器端：mcp-ops.exe --http 0.0.0.0:8000（监听所有网卡）"
echo "  - dsh 侧配置：dsh-mcp-client 用 transport=streamable-http + url=http://<服务器IP>:8000/mcp"
