# DSH-Agent — AI Ops Assistant Built on DeepSeek Harness

基于 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 的智能运维助手。采用「主管 + 专家」两层架构：用户用自然语言报障/查指标，系统自动路由到主管或运维诊断专家，并由自研 Go 监控采集器 + Prometheus 提供**真实数据**支撑结论。

An AI-powered Ops Assistant built on [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness). It uses a "Supervisor + Expert" two-layer architecture: users report issues / query metrics in natural language, the system routes to a supervisor or the ops-diagnosis expert, and every conclusion is backed by **real data** from a self-built Go collector and Prometheus.

---

## Architecture / 架构

```
User: "web-01 CPU 为什么高" / "why is CPU high on web-01"
        │
        ▼
┌────────────────────────────────────────────────────┐
│ Supervisor (glm/doubao · dialogue + routing + memory)│
│  ├─ Simple query  → calls mcp__ops__* directly      │
│  └─ Complex fault → spawns expert via delegate_diagnosis
└─────────────────────────┬──────────────────────────┘
                          ▼
┌────────────────────────────────────────────────────┐
│ Ops Diagnosis Expert (subagent · root cause + report)│
│   mcp__ops__query_resource_usage / query_process /  │
│   query_logs                                        │
└─────────────────────────┬──────────────────────────┘
                          ▼
┌────────────────────────────────────────────────────┐
│ Go Collector (mcp-ops, self-built / 自研)            │
│   MCP over stdio / streamable-http (dual channel)   │
│   Sources: Prometheus (CPU/mem/disk/net),           │
│            Windows API (process), filesystem (logs) │
└────────────────────────────────────────────────────┘
```

---

## Features / 核心能力

| 能力 Capability | 说明 Description |
|---|---|
| **Supervisor + Expert routing** 主管+专家路由 | Simple single-point queries answered by the supervisor directly; complex diagnostics (multi-tool, root-cause, remediation) delegated to the expert. 简单查询主管直接答，复杂排障委派专家 |
| **Real data** 真实数据 | Self-built Go collector (pure Windows API, no third-party deps) + Prometheus/PromQL. 自研 Go 采集器（纯 Windows API）指标走 Prometheus |
| **Domain MCP tools** 领域工具 | `mcp__ops__query_resource_usage` / `query_process` / `query_logs` |
| **Dual-channel deploy** 双通道部署 | stdio (same host) / streamable-http (cross-host). 同机 stdio，跨机 HTTP |
| **Triple protection** 三层防护 | dsh sandbox (read-only) + persona data-source discipline & safety red-line + supervisor evidence check before reporting. 沙箱防越权 + 提示词约束数据来源/危险操作 + 转述前证据检查 |

---

## Tech Stack / 技术栈

| Layer 层 | Tech 技术 |
|---|---|
| Agent framework | DeepSeek Harness (dsh) 0.1.1-rc.2 · Cordis plugin architecture |
| LLM | Volcano Ark (OpenAI Responses API) · default `glm-5-2-260617` |
| Domain tools | Go 1.26 + official `modelcontextprotocol/go-sdk` v1.7.0 |
| Metrics storage | Prometheus 3.3.1 (PromQL) |
| Collector | self-built `mcp-ops` (pure Windows API) |

---

## Quick Start / 快速开始

> Prerequisites / 前置：Node >= 22.19, Go 1.22+, an Ark API Key

```bash
# 1. Install dsh (Node >= 22.19 required)
npm i -g @deepseek-ai/dsh

# 2. Set your model API key (via env var; key never stored in repo)
export ARK_API_KEY="your-ark-api-key"

# 3. Build the Go collector
cd tools/mcp-ops && go build -o mcp-ops.exe . && cd ../..

# 4. Download & start Prometheus + metrics exporter (two terminals)
#    terminal A: metrics exporter
cd tools/mcp-ops && ./mcp-ops.exe --exporter 127.0.0.1:9100
#    terminal B: Prometheus (first run downloads the binary)
bash tools/prometheus/download_prometheus.sh   # once, ~115MB
bash tools/prometheus/start_prometheus.sh

# 5. Launch the web UI (start_dsh.sh puts tools/mcp-ops on PATH so the MCP client finds mcp-ops.exe)
bash start_dsh.sh --port 3080
```

Open `http://127.0.0.1:3080` and try:
> 「帮我查一下 CPU」 → supervisor answers directly with real data
> 「web-01 磁盘快满了，分析原因」 → expert runs root-cause diagnosis and outputs a structured report

打开 `http://127.0.0.1:3080` 试一试用自然语言报障。

> ⚠️ `cordis.patch.yml` 里的 MCP client 用 `command: mcp-ops.exe`（只写文件名），由 `start_dsh.sh` 把 `tools/mcp-ops` 加入 `PATH` 来定位。
> 手动 `dsh web --patch ...` 时请先 `export PATH="$(pwd)/tools/mcp-ops:$PATH"`。不要写 `command: !!js process.env.OPS_MCP_EXE`——dsh 不解析该表达式。

---

## Verification Scripts / 验证脚本

| Script | Purpose 用途 |
|---|---|
| `tools/prometheus/verify_promql.sh` | Verify same-host PromQL chain 同机链路验证 |
| `tools/mcp-ops/verify_http.sh` | Verify cross-host streamable-http 跨机部署验证 |

---

## Project Layout / 目录结构

```
deepseek-ops-assistant/
├── AGENTS.md                # Supervisor role definition 主管角色定义
├── cordis.patch.yml         # dsh config: model provider + expert + MCP client
├── start_dsh.sh             # Launcher: prepends tools/mcp-ops to PATH, starts dsh web
├── tools/
│   ├── mcp-ops/             # Go collector (7 .go files + scripts) Go 采集器
│   └── prometheus/          # Prometheus config, download & start scripts
└── README.md
```

---

## Cross-host Deployment / 跨机部署

Deploy the collector on the target server and connect dsh remotely via streamable-http:
采集器部署在被监控服务器，dsh 通过 streamable-http 远程连接：

```yaml
# on the target server / 目标服务器
mcp-ops.exe --http 0.0.0.0:8000

# dsh side config (cordis.patch.yml) / dsh 侧配置
- id: mcp-ops
  name: '@deepseek-ai/dsh-mcp-client'
  config:
    serverName: ops
    transport: streamable-http
    url: http://<SERVER_IP>:8000/mcp
```

---

## License / 许可证

MIT
