#!/usr/bin/env bash
# DSH-Agent 启动器（web 模式）
#
# 自动完成两件事：
#   1. 把 tools/mcp-ops 加入 PATH，使 cordis.patch.yml 里的 command: mcp-ops.exe 能解析
#   2. 启动 dsh web 模式
#
# 用法：bash start_dsh.sh [--port PORT]
set -e
cd "$(dirname "$0")"

# ---- 自动载入 .env（若存在）----
# 把 API Key 等敏感变量放 .env，避免每次手动 export；.env 已被 .gitignore 忽略，不会提交
if [ -f "$(dirname "$0")/.env" ]; then
  set -a
  . "$(dirname "$0")/.env"
  set +a
  echo "已从 .env 载入环境变量"
fi

PORT=3080
[ "$1" = "--port" ] && PORT="$2"

# ---- 检查 mcp-ops.exe 并加入 PATH ----
# dsh 的 patch loader 不解析 !!js / ${ENV}，所以 cordis.patch.yml 里 command 写的是 mcp-ops.exe，
# 这里把它所在目录加入 PATH，dsh 拉起 MCP server 时即可按文件名找到。
MCP_DIR="$(pwd)/tools/mcp-ops"
MCP_EXE="$MCP_DIR/mcp-ops.exe"
if [ ! -f "$MCP_EXE" ]; then
  echo "错误：未找到 $MCP_EXE"
  echo "请先编译：cd tools/mcp-ops && go build -o mcp-ops.exe ."
  exit 1
fi
export PATH="$MCP_DIR:$PATH"
echo "已将 $MCP_DIR 加入 PATH"

# ---- 检查 ARK_API_KEY ----
if [ -z "$ARK_API_KEY" ]; then
  echo "警告：未设置 ARK_API_KEY，模型请求会失败"
  echo "  设置：export ARK_API_KEY=\"你的火山方舟 API Key\""
fi

# ---- 启动 dsh web ----
echo "启动 dsh web → http://127.0.0.1:${PORT}"
exec dsh web --patch ./cordis.patch.yml --no-open --port "$PORT"
