# DSH-Agent —— 基于 DeepSeek Harness 的智能运维助手

一个基于 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness)（dsh）构建的「主管 + 专家」智能运维助手。用户用自然语言报故障/查指标，系统自动路由到**主管**或**运维诊断专家**，用真实监控数据（自研 Go 采集器 + Prometheus）支撑结论。

## 架构

```
用户问「web-01 CPU 为什么高」
        │
        ▼
┌─────────────────────────────────────────────┐
│ 主管（glm/doubao，对话 + 意图路由 + 多轮记忆） │
│   ├─ 简单查询 → 自己直接调 mcp__ops__* 工具    │
│   └─ 复杂排障 → delegate_diagnosis 拉起专家    │
└────────────┬────────────────────────────────┘
             ▼
┌─────────────────────────────────────────────┐
│ 运维诊断专家（子代理：根因分析 + 排障报告）      │
│   调 mcp__ops__query_resource_usage /        │
│      query_process / query_logs              │
└────────────┬────────────────────────────────┘
             ▼
┌─────────────────────────────────────────────┐
│ Go 监控采集器（mcp-ops，自研）                │
│   stdio / streamable-http 双通道 MCP          │
│   数据源：Prometheus（CPU/内存/磁盘/网络）     │
│           Windows API（进程）文件系统（日志）  │
└─────────────────────────────────────────────┘
```

## 核心能力

- **主管 + 专家两层路由**：简单单点查询主管直接答；复杂排障（多工具、根因分析、处置建议）委派专家
- **真实数据支撑**：自研 Go 采集器（纯 Windows API，无第三方依赖），指标走 Prometheus/PromQL
- **领域 MCP 工具**（`mcp__ops__*`）：
  - `query_resource_usage` — 节点 CPU/内存/磁盘/网络指标
  - `query_process` — 进程枚举
  - `query_logs` — 日志检索
- **双通道部署**：stdio（同机）/ streamable-http（跨机）
- **三层防护**：dsh 沙箱（read-only）防越权 + persona 数据来源纪律/安全红线 + 主管转述前证据检查

## 技术栈

| 层 | 技术 |
|---|---|
| Agent 框架 | DeepSeek Harness（dsh）0.1.1-rc.2，Cordis 插件化 |
| 大模型 | 火山方舟 Ark（OpenAI Responses 协议），默认 `glm-5-2-260617` |
| 领域工具 | Go 1.26 + 官方 `modelcontextprotocol/go-sdk` |
| 监控存储 | Prometheus 3.3.1（PromQL） |
| 采集器 | 自研 `mcp-ops`（纯 Windows API） |

## 快速开始

```bash
# 1. 安装 dsh（需 Node >= 22.19）
npm i -g @deepseek-ai/dsh

# 2. 配置模型（编辑 cordis.patch.yml 或设环境变量）
export ARK_API_KEY="你的火山方舟 API Key"

# 3. 编译 Go 采集器
cd tools/mcp-ops && go build -o mcp-ops.exe .

# 4. 启动 Prometheus + exporter
bash tools/prometheus/start_prometheus.sh   # 需先另开终端跑 mcp-ops --exporter

# 5. 启动 web 界面
dsh web --patch ./cordis.patch.yml --port 3080
```

## 验证脚本

- `tools/prometheus/verify_promql.sh` — 同机 PromQL 链路验证
- `tools/mcp-ops/verify_http.sh` — 跨机 streamable-http 验证

## 文档

| 文档 | 内容 |
|---|---|
| `PLAN.md` | 实施计划与里程碑 |
| `DSH-开发指南.md` | 从零打造智能体的完整流程 |
| `DSH-框架详解.md` | dsh 核心机制源码级拆解 |
| `DSH-插件清单.md` | dsh 插件全量说明 |
| `DSH-部署与分发.md` | 打包分发四种形态 |
| `工具方案-Go监控采集器.md` | 领域工具方案与跨机架构 |

## 许可证

MIT
