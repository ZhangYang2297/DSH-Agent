# 用 DeepSeek Harness 打造自己的智能体 —— 完整开发指南

> 本文回答三个问题：**DeepSeek Harness 到底是什么？它包含哪些部分？我们在开发中做了什么、还能做什么？**
> 面向面试项目准备，重点是真正理解框架，而不是"照着配置跑通"。

---

## 一、核心认知：Agent = Model + Harness

DeepSeek Harness（dsh）官方给出的公式：

```
Agent（智能体）= Model（模型）+ Harness（运行时）
```

- **Model**：负责推理、规划、生成工具调用（我们用的是火山方舟 Ark 的 doubao/glm）
- **Harness**：负责读文件、跑命令、管会话、做沙箱、调度子代理、把结果写回日志——**这些全部由 dsh 提供**

所以你说的"harness 包含了整个智能体的全流程，我们做的仅仅是提示词"——**方向是对的**。dsh 是一个**开箱即用的 agent 运行时**：agent loop、工具、记忆、沙箱、审批、子代理、UI 全部内置。我们当前在"配置 + 提示词"层面使用它，因为能力不需要重写。

**但"一切皆插件"意味着：这些内置的东西每一个都可以被替换或扩展**。这是 dsh 与 Claude Code / Codex 的本质区别——那两者是成品（黑盒多、可改的少），dsh 是"乐高底座"（组件可热插拔）。

---

## 二、框架全貌：插件地图（dsh 由哪些部分组成）

dsh 基于 **Cordis** 插件框架（和 Koishi 同源）。整个智能体由几十个插件组合而成，按职责分 7 层：

### 1. Agent 循环层（最核心）
| 插件 | 职责 |
|---|---|
| `dsh-agent-loop` | **唯一的 agent loop 实现**。驱动"会话→轮次→步骤"生命周期，即经典的 `思考→调用工具→观察结果→继续` 循环 |
| `dsh-agent` | Agent 接口与工厂抽象 |
| `dsh-agent-instructions` | 加载并注入 AGENTS.md 指令 |
| `dsh-agent-presets` | 角色预设组装（见下文第 6 点） |
| `dsh-agent-default-model` | 设置默认 provider/model |

### 2. 模型层
| 插件 | 职责 |
|---|---|
| `dsh-llm` | 模型适配器注册表 + 统一流式调用接口 |
| `dsh-llm-deepseek` | DeepSeek 官方 API 适配器 |
| `dsh-llm-pi-ai` | **通用适配器**：接任意 OpenAI 兼容端点（我们用它接火山方舟） |
| `dsh-llm-retry` | 请求重试策略 |

### 3. 会话与记忆层
| 插件 | 职责 |
|---|---|
| `dsh-session` | 会话抽象（SessionId、消息、事件） |
| `dsh-session-persistence-jsonl` | **会话持久化**：append-only 事件日志，zstd 压缩的 JSONL |
| `dsh-session-query-sqlite` | 会话检索（UI 的"搜索会话"功能依赖它） |
| `dsh-session-telemetry-otel` | 可观测性/轨迹（OpenTelemetry） |
| `dsh-compaction-*` | 上下文压缩（长对话裁剪） |

### 4. 工具层
| 插件 | 职责 |
|---|---|
| `dsh-tools` | 工具注册表 |
| `dsh-tool-fs` / `dsh-tool-fs-search` | 文件读写、搜索 |
| `dsh-tool-bash` / `dsh-tool-pwsh` | shell 命令（Windows 用 pwsh） |
| `dsh-tool-web` | 网页搜索 |
| `dsh-tool-skill` | 技能调用 |
| `dsh-tool-subagent` | **子代理工具**（拉起专家） |
| `dsh-tool-workflow` | 工作流编排 |
| `dsh-tool-todo` / `dsh-tool-goal` | 任务清单 / 目标管理 |
| `dsh-tool-str-replace-editor` | 字符串替换编辑器 |

### 5. 子代理层（主管+专家的核心）
| 插件 | 职责 |
|---|---|
| `dsh-subagent` | 子代理服务抽象（注册表 + 生命周期） |
| `dsh-subagent-spawn-in-process` | spawn 提供方：创建独立上下文的子 Agent |
| `dsh-subagent-fork-in-process` | fork 提供方：克隆父级历史的子 Agent |
| `dsh-tool-subagent-control` / `report` | 子代理管理/结果回传 |

### 6. 沙箱与审批层
| 插件 | 职责 |
|---|---|
| `dsh-sandbox` / `dsh-fs-sandbox` | 文件系统/进程沙箱 |
| `dsh-sandbox-policy` | 沙箱模式（read-only / workspace-write / full-access） |
| `dsh-permission-presets` | 权限预设 |
| `dsh-user-approval` | 审批策略（ask / never） |
| `dsh-tool-call-timeout-policy` | 工具调用超时 |

### 7. UI 与基础设施层
| 插件 | 职责 |
|---|---|
| `dsh-web` + `dsh-client-*` | 浏览器 UI（工作区、会话列表、搜索、轨迹视图） |
| `dsh-settings-file` | settings.yaml 配置管理 |
| `dsh-credentials-local` | API key 凭据存储 |
| `dsh-skill` | 技能系统（类似 Codex 的 Skills） |
| `dsh-jobs-local` | 后台任务管理 |

---

## 三、核心机制详解（面试要能讲清楚）

### 1. Agent Loop 怎么运转

`dsh-agent-loop` 是**唯一包含循环逻辑的包**。它的循环：

```
用户输入 → 组装上下文（历史 + AGENTS.md + 工具 schema + persona）
       → 调模型（llm.stream）→ 模型返回文本 或 工具调用
       → 执行工具（fs/pwsh/subagent…）→ 把工具结果作为新上下文
       → 再调模型 → …直到完成
       → 写回会话日志（session.jsonl.zstd）
```

每个 agent 有唯一的 SessionId，循环驱动它的生命周期。**子代理也是完整的 agent loop**——`spawn` 提供方在当前进程里创建另一个 `Agent`，它有自己的 loop、会话、工具。

### 2. 记忆系统：会话级记忆，不是长期记忆

- 每个会话的**完整事件日志**（用户消息、系统提示、工具调用/结果、推理链、模型输出）append-only 写入 `~/.dsh/sessions/<工作区>/<会话id>/session.jsonl.zstd`
- 多轮对话记忆 = 重放这个日志（或其中的摘要）
- **注意**：这是**会话级记忆**，不是"跨会话的长期知识库"。dsh 没有内置向量记忆；如果智能体需要"记住用户偏好/历史事实"这种长期记忆，**需要自己实现**（写存储插件或记忆工具）——这是你可以做的点。

### 3. 工具怎么工作

- 工具通过 `ctx.tools.register()` 注册，暴露为模型的 function schema
- 内置工具由各个 `dsh-tool-*` 插件提供
- **自定义工具 = 写一个 Cordis 插件**，在插件里注册 `defineTool({...})`，模型就能调用
- 子代理**继承父级的工具注册表**（除非用 toolFilter 收窄）

### 4. 子代理 = 主管+专家的实现方式

- `dsh-tool-subagent` 暴露 `subagent` 工具（模型可调用）
- 主管模型调用它 → `spawn` 提供方创建**全新子 Agent**（独立会话、独立 loop、看不到主管历史）
- 子 Agent 跑完，最终文本回传给主管
- 三种模式：
  - `spawn`：全新上下文（我们当前用这个）
  - `fork`：克隆父级历史（子代理能看到主管对话）
  - `continuable`：可继续子代理（持久会话，主管可多次发任务）
- **一个专家 = 一个 subagent 工具实例**（不同 toolName + persona + toolFilter）

### 5. Agent Preset：比 AGENTS.md 更结构化的角色封装

- `dsh-agent-presets`：preset = 一个目录，含 `agent.cordis.yml`，封装"工具集 + 提示段"
- 会话 mount 一个 preset → 获得该 preset 定义的工具和提示
- **preset 决定模型看到的工具 schema 与提示词**，比 AGENTS.md 结构化、可复制、可版本化
- 子代理通过 `composeFrom` 继承父级 preset
- 当前项目还没用 preset（用的是 AGENTS.md + persona），这是**可以升级的方向**

### 6. UI：完整的管理端

web 模式（`dsh web`）提供：
- 工作区管理、多会话、会话搜索（sqlite 索引）
- 轨迹视图（查看 agent 每一步干了什么）
- 设置界面（模型、凭据、权限）
- 这些都是 dsh 自带的前端，**白标替换**属于"自己开发"的范畴

---

## 四、我们当前做了什么（拆解）

`deepseek-ops-assistant/` 项目实际由两部分组成：**配置 + 提示词（角色）** + **自研工具代码**。

### 4.1 配置与提示词

| 文件 | 作用 | 性质 |
|---|---|---|
| `cordis.patch.yml` | ① `llm-pi-ai` 注册 Ark provider ② 改默认模型 ③ `insert` subagent 工具实例（运维专家）④ `insert` `dsh-mcp-client`（连接自研 Go 采集器） | 配置 |
| `AGENTS.md` | 主管角色定义（职责、**意图路由规则**、上下文纪律、转述检查） | 提示词 |
| `persona`（在 patch 里） | 运维诊断专家角色（根因分析、排障报告结构、数据来源纪律、安全红线） | 提示词 |

### 4.2 自研工具代码（`tools/mcp-ops/`，Go）

| 文件 | 作用 |
|---|---|
| `main.go` | MCP server：3 个工具 + 三种启动模式 |
| `collect.go` / `collect_windows.go` | 本机资源采集（CPU/内存/磁盘/进程），纯 syscall |
| `network_windows.go` | 网络字节计数（GetIfTable） |
| `logs.go` | 日志检索（纯文件读取） |
| `promql.go` | Prometheus HTTP API 轻客户端 |
| `exporter.go` | `--exporter`（/metrics）与 `--http`（streamable-http）两种模式 |

**三种运行模式**：
1. **stdio（默认）**：dsh 同机拉起，进程内管道
2. **`--http`**：streamable-http MCP server（跨机部署，dsh 远程连接）
3. **`--exporter`**：Prometheus 指标端点（/metrics）

**数据链路**：`MCP 工具 → PromQL → Prometheus → mcp-ops --exporter → 本机真实指标`。

### 4.3 意图路由设计（主管的决策逻辑，面试可讲）

主管是系统的"大脑"，决定**自己直接干**还是**派专家**。这是整个系统质量的入口，核心规则：

**主管可直接用的工具**（`mcp__ops__*`）：单点查询自己答——查 CPU/内存/磁盘/网络、查进程、查日志。**判断口诀：单点查询 → 自己调工具。**

**调度专家的场景**（`delegate_diagnosis`）：需要多工具/多次查询才能完成、需要根因分析、需要排查思路、需要给运维建议、故障关联判断。**判断口诀：关联分析/根因/建议 → 调专家。**

**为什么这样设计**（工程理由）：
- 简单查询调专家 = 过度委派：多一次 spawn、多一轮推理、慢且费 token
- 复杂故障自己硬查 = 能力不足：没有排查思路、容易漏证据、结论不可靠
- 两层都有真实数据兜底：主管直接查=快、专家深挖=准，各司其职

### 4.4 专家 persona 的安全设计

专家 persona 里写死了两条硬约束（决定它"敢不敢乱动"）：
1. **数据来源纪律**：明确区分诊断目标与实际数据来源，本机数据绝不冒充目标服务器，证据不足如实说
2. **处置建议安全红线**：绝不建议删库/删文件/kill/重启生产等破坏性操作；处置按"最小干预→逐步升级"；涉及变更必须给备用方案 + 风险评估 + 回滚方案 + 标注需人工确认

这两条是**提示词层面的安全护栏**，与 dsh 的沙箱（read-only）形成**双层防护**：沙箱拦住"真动手"，persona 拦住"嘴上建议"。

**也就是说：当前 = 配置（组合已有插件）+ 提示词（定义角色）+ 自研 Go 工具（领域能力落地）。** 这是"浅层 + 中层"的组合，既有框架的组合能力，又有自己写的可运行代码。

---

## 五、哪些是自带的，哪些可以自己开发（面试重点）

### 保留（框架核心，一般不自己写）
- agent loop、会话持久化、沙箱、审批、Web UI、settings、凭据、技能系统
- 说明你"理解并善用框架"，而不是重复造轮子

### 可以自己实现（面试亮点的来源）

| 可自研点 | 怎么做 | 面试价值 |
|---|---|---|
| **自定义工具插件** | 写 Cordis 插件，`ctx.tools.register(defineTool(...))`。如 `run_vision_model`、`query_logs`、`query_monitor` | 展示领域能力落地 |
| **Agent Preset** | 创建 `agent.cordis.yml` 封装主管/专家角色（工具+提示），替代散装的 AGENTS.md+persona | 展示对 dsh 组装机制的理解 |
| **长期记忆层** | 写存储插件或记忆工具，接向量库/数据库，让 agent 跨会话记住事实 | 展示架构设计能力 |
| **自定义模型适配器** | 实现 `LlmAdapter`，接入 pi-ai 覆盖不了的端点 | 展示对 LLM 协议的理解 |
| **白标 UI** | 替换 `dsh-client-ui-*`，做自己的前端 | 展示工程化能力 |
| **Agent Loop 扩展** | 监听 `llm/stream`、`agent/request-error` 等事件，加日志/缓存/路由 | 展示对循环机制的理解 |
| **审批/沙箱策略** | 自定义 permission-presets | 展示安全设计 |

### 判断标准
> dsh 的哲学是"**一切皆插件，按需替换**"。**通用能力**（loop/UI/沙箱/持久化）用自带的；**领域能力**（你的运维/交通业务）写插件；**体验层**（角色/边界）用配置和提示词。

---

## 六、从零打造一个智能体的完整流程

```
第 1 步  安装环境：Node ≥ 22.19，npm i -g @deepseek-ai/dsh
第 2 步  了解三种运行形态：web（UI）/ headless（单次任务）/ tui（终端）
第 3 步  接入模型：cordis.patch.yml 里用 llm-pi-ai 注册你的 provider（base_url/协议/key/模型列表）
第 4 步  定义角色：项目根目录写 AGENTS.md（agent-instructions 自动注入）
第 5 步  启动 web，验证基础对话、多轮记忆、内置工具（fs/pwsh）
第 6 步  加专家：patch 里 insert 一个 dsh-tool-subagent 实例（toolName + persona + toolFilter）
第 7 步  验证主管→专家链路（指代消解、结果回流）
第 8 步  写领域工具插件（如视觉研判、日志查询），挂到主管或专家
第 9 步  升级为 agent-preset（结构化角色），加长期记忆层
第10 步  白标/嵌入（SDK 发布后，或替换 UI）
```

**当前进度**：到第 7 步（主管 + 运维专家链路已通）。第 8-10 步是"自己开发"的主要空间。

---

## 七、面试怎么讲（3 分钟版）

1. **选型**：秋招项目要用 agent 架构，对比了 Codex（闭源、与国内模型兼容差）、LangChain（库不是运行时）、DeepSeek Harness（MIT 开源、一切皆插件、原生子代理、可接任意模型）→ 选 dsh
2. **架构**：主管（主 agent）+ 专家（subagent），用 dsh 的子代理机制实现，无需手写调度
3. **角色与约束**：角色用 AGENTS.md/persona 定义，通过"数据来源纪律""转述前证据检查"解决模型幻觉问题（讲这个真实踩坑很有说服力）
4. **自己做的东西**：领域工具插件、agent-preset 封装、长期记忆层
5. **理解框架**：能讲清 agent loop 事件流、会话持久化机制、插件替换边界（这才是面试官最想听的）
