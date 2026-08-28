#!/usr/bin/env bash
# DSH-Agent 启动器（web 模式）
#
# 自动完成两件事：
#   1. 设置 OPS_MCP_EXE 为当前项目 mcp-ops.exe 的绝对路径（供 cordis.patch.yml 使用）
#   2. 启动 dsh web 模式
#
# 用法：bash start_dsh.sh [--port PORT]
set -e
cd "$(dirname "$0")"

PORT=3080
[ "$1" = "--port" ] && PORT="$2"

# ---- 检查 mcp-ops.exe ----
MCP_EXE="$(pwd)/tools/mcp-ops/mcp-ops.exe"
if [ ! -f "$MCP_EXE" ]; then
  echo "错误：未找到 $MCP_EXE"
  echo "请先编译：cd tools/mcp-ops && go build -o mcp-ops.exe ."
  exit 1
fi
export OPS_MCP_EXE="$MCP_EXE"
echo "OPS_MCP_EXE=$OPS_MCP_EXE"

# ---- 检查 ARK_API_KEY ----
if [ -z "$ARK_API_KEY" ]; then
  echo "警告：未设置 ARK_API_KEY，模型请求会失败"
  echo "  设置：export ARK_API_KEY=\"你的火山方舟 API Key\""
fi

# ---- 启动 dsh web ----
echo "启动 dsh web → http://127.0.0.1:${PORT}"
exec dsh web --patch ./cordis.patch.yml --no-open --port "$PORT"
