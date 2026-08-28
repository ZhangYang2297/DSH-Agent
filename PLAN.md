# DeepSeek Harness 智能运维助手 —— 实施计划

> 状态：M1-M3 全部完成，工具集落地，跨机能力就绪
> 日期：2026-08-28
> 项目路径：`deepseek-ops-assistant/`

## 1. 背景与动机

Codex Harness（openai-codex SDK 0.147）在火山方舟 Ark 环境下有 3 个平台级兼容问题：
1. **MCP 工具不可见**：Codex 0.144+ 强制把 MCP 工具打成 `type:"namespace"`，Ark `/api/v3` 不支持 namespace 工具类型 → 模型看不到专家工具
2. **写操作必被拒**：auto_review 审批依赖 OpenAI 内部模型 `codex-auto-review`，自定义 provider 下不存在（GitHub issue #31255）→ fail-closed 拒绝所有写操作
3. **流式崩溃**：Ark 流式 reasoning 事件与 Codex 状态机不兼容（已用 `model_reasoning_summary="none"` 缓解）

结论：Codex Harness 与国内 API 兼容性差。改用 **DeepSeek Harness（dsh）** 重做「主管 + 专家」架构。

## 2. 为什么选 DeepSeek Harness

- **MIT 开源**，`github.com/deepseek-ai/deepseek-harness`，2026-08-13 发布，17.8 万 star
- **一切皆插件（Cordis）**：模型、工具、会话、沙箱、子代理、UI 全部可替换/扩展
- **原生子代理**：`dsh-subagent` / `dsh-subagent-spawn-in-process` / `tool-subagent` 等插件 → 主管+专家模式是原生能力
- **可换模型**：`dsh-llm-pi-ai` 适配器支持任意 OpenAI-compatible 自定义 provider → 可直接接火山方舟 Ark
- **本地优先**：会话、凭据、状态存本地
- 环境：Node 22.22.2 已满足（>=22.19）；Python SDK 目前是占位包，运行时为 Node

## 3. 目标架构（主管 + 专家）

```
用户对话 / 事件
   │
   ▼
┌───────────────────────────────────────────────┐
│ 主管 agent（dsh-agent，主会话）                │
│   工具：                                       │
│     • 内置：fs / bash(pwsh) / web / skill      │
│     • 子代理调度：subagent（spawn/fork）        │
│       ├─ 运维诊断专家（子代理，只挂只读工具）    │
│       └─ 交通研判专家（子代理，本地视觉模型）    │
└───────────────────────────────────────────────┘
```

- 主管 = 主 agent 会话（对话、意图路由、多轮记忆、流式汇报）
- 专家 = 子代理（`subagent` 工具拉起，任务完成后结果回流主管）
- 这正好是 dsh 的 `dsh-subagent` 原生能力，不需要手写调度

## 4. 里程碑

### M1：环境与模型接入（✅ 已完成）
- [x] 安装 dsh（`npm i -g @deepseek-ai/dsh`，0.1.1-rc.2）
- [x] 确认配置机制：`--patch ./cordis.patch.yml`（id 定位插件、config 覆盖）
- [x] 在 patch 配置 Ark provider（`llm-pi-ai.providers.ark`，`api: openai-responses`，`baseURL=https://ark.cn-beijing.volces.com/api/v3`）
- [x] 覆盖 `agent-default-model` → provider=ark, model=glm-5-2-260617
- [x] `dsh --profile headless "你好"` 跑通真实对话（glm-5-2-260617）

### M2：主管框架（✅ 已完成）
- [x] headless 单任务模式验证（无多轮，单次问答）
- [x] 接入形态选择：**web 模式**（自带 UI + 会话记忆 + 轨迹视图，`dsh web`）
- [x] 主管角色 prompt：项目 `AGENTS.md`（dsh 的 agent-instructions 插件自动加载）
- [x] 端到端验证（headless）：
  - 对话 ✓（中文回复）
  - 文件读取 ✓（真实返回 cordis.patch.yml 内容）
  - shell 命令 ✓（`echo DSH_HARNESS_OK_42` 真实执行）
  - **子代理 ✓（主管拉起子代理拿到结论并转述）**
- [x] web 服务启动：`dsh web --patch ./cordis.patch.yml --no-open --port 3080` → http://127.0.0.1:3080
- [x] 用户浏览器体验 web UI（多轮对话 + 子代理）——期间修复 stale lock / 残留进程问题

### M3：运维诊断专家 + 领域工具集（✅ 已完成）

**子代理链路**
- [x] 子代理工具注册：`cordis.patch.yml` 用 `- insert:` 块新增 `tool-ops-expert`（`@deepseek-ai/dsh-tool-subagent`，toolName=`delegate_diagnosis`，persona=运维诊断专家角色）
- [x] 主管通过 `delegate_diagnosis` 工具拉起专家，结果回流并转述（headless 端到端验证 PASS）
- 关键踩坑：新增插件实例必须用 `- insert:` 块（`- id:` 只能覆盖已有条目，报 `entry not found`）；`maxDepth` 显式数字会导致工具未注册（mount 校验 depthLimit），先用默认/简化配置

**领域工具集（Go 监控采集器 + Prometheus + MCP，方案见 `工具方案-Go监控采集器.md`）**
- [x] T1 装 Go + 建 MCP server 骨架（`tools/mcp-ops/`，3 个工具，Go 1.26.7 + 官方 go-sdk v1.7.0）
- [x] T2 dsh 接入 `dsh-mcp-client`（stdio），确认 `mcp__ops__*` 工具可见可调（主管真实调用成功）
- [x] T3 Prometheus 3.3.1 + 自研 exporter（`mcp-ops --exporter :9100`，纯 syscall 采集），`query_resource_usage` 走 PromQL 返回真实 CPU/内存/磁盘/网络指标（端到端 PASS）
- [x] T4 `query_process`（CreateToolhelp32Snapshot）/ `query_logs`（纯文件检索）真实数据可用
- [x] **跨机能力**：`mcp-ops --http` 起 streamable-http MCP server（go-sdk StreamableHTTPHandler），实测 Python 远程连接调用成功；dsh 跨机接入用 `transport: streamable-http + url`
- [x] **意图路由重构**：主管可直接用 `mcp__ops__*` 答简单查询；仅复杂排障/根因分析/给建议才调专家（AGENTS.md 判断口诀 + 专家 persona 结构化排障报告 + 安全红线备用方案，三重验证 PASS）
- [x] 验证脚本：`verify_promql.sh`（同机链路）/ `verify_http.sh`（跨机）/ `start_prometheus.sh`（监控栈）
- 说明：因工具主管/专家共用，原"toolFilter 收敛"不再需要

### M4：交通研判专家（后续）
- [ ] 本地视觉模型 + `run_vision_model` 工具
- [ ] 图片事件入口

## 5. 关键待验证风险

| 风险 | 说明 | 状态 |
|---|---|---|
| Ark 与 pi-ai 的 Responses 兼容性 | pi-ai 用 `openai-responses` 协议，Ark `/api/v3` 工具调用 | ✅ 实测兼容（工具调用正常，无 Codex 的 namespace 问题） |
| headless 无会话记忆 | headless 是"单任务即退" | ✅ 用 web 模式（自带会话管理）；SDK 占位待正式版 |
| 子代理是否支持自定义模型 | 子代理默认继承主模型 | ✅ 实测继承正常 |
| Windows 下沙箱 | bash-sandbox 禁用，pwsh-sandbox 启用 | ✅ 用 pwsh 路径 |
| 沙箱拦截系统命令 | Go 进程内调 tasklist/wevtutil 被安全策略拦截 | ✅ 改纯 Windows API 采集 |
| MCP stdio 无法跨机 | stdio 是子进程管道，dsh 与采集器异地时不可用 | ✅ `--http` streamable-http 跨机方案就绪（已实测） |
| 采集器数据真实性 | 模型可能编造数据 / 拿本机冒充目标 | ✅ 专家 persona 数据来源纪律 + 主管转述证据检查 + 工具真实返回 |

## 6. 验证方式

- 每个里程碑有可运行的验证命令（见各里程碑）
- 用真实 Ark key（`ARK_API_KEY`）端到端测试
- 保留 `tests/` 目录放验证脚本（吸取 codex-ops-assistant 根目录散落测试文件的教训）
