#!/usr/bin/env bash
# 下载并解压 Prometheus（clone 后首次运行前执行一次）
#
# 默认从 GitHub 官方 release 下载；如网络慢可换镜像：
#   bash download_prometheus.sh --mirror ghfast   # 走 ghfast.top 加速
set -e
cd "$(dirname "$0")"

VER="3.3.1"
MIRROR="${2:-github}"

case "$MIRROR" in
  ghfast) URL="https://ghfast.top/https://github.com/prometheus/prometheus/releases/download/v${VER}/prometheus-${VER}.windows-amd64.zip" ;;
  github) URL="https://github.com/prometheus/prometheus/releases/download/v${VER}/prometheus-${VER}.windows-amd64.zip" ;;
  *) echo "未知镜像：$MIRROR (可选 github / ghfast)"; exit 1 ;;
esac

ZIP="prometheus-${VER}.windows-amd64.zip"
DIR="prometheus-${VER}.windows-amd64"

if [ -f "prometheus-bin/prometheus.exe" ]; then
  echo "已存在 prometheus-bin/prometheus.exe，跳过"
  exit 0
fi

echo "下载 Prometheus v${VER} ($MIRROR)..."
curl -sL --retry 3 -o "$ZIP" "$URL"
echo "下载完成：$(du -h "$ZIP" | cut -f1)"

echo "解压..."
unzip -q -o "$ZIP"
rm -rf prometheus-bin
mv "$DIR" prometheus-bin
rm -f "$ZIP"

echo "完成：prometheus-bin/prometheus.exe"
echo "下一步：bash start_prometheus.sh"
