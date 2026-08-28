# DeepSeek Harness 插件清单（web profile 全量，约 80 个）

> 这是 `dsh --profile web --dump-config` 实际加载的插件树。按职责分 10 类。
> 每个插件一句话说明"它干什么"。带 ⭐ 的是你面试/开发最该认识的。

---

## 一、Cordis 基础（插座板本身）

| 插件 | 作用 |
|---|---|
| `cordis-plugin-timer` | 定时器能力 |
| `cordis-plugin-hmr` | 热模块替换：配置/插件变更热加载 |
| `dsh-typert-registry` | RPC 类型系统（Typert）的运行时注册表 |
| `dsh-typert-loader` | 加载插件包的 Typert 产物（RPC schema） |
| `dsh-api-gateway` | Host↔Client 的 RPC 网关（前端↔后端的通信桥梁） |

## 二、Agent 核心（大脑）

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-agent` | Agent 接口、实时注册表、`agent/*` 事件 |
| ⭐ `dsh-agent-loop` | **唯一的 agent loop 实现**：会话/轮次/步骤生命周期驱动 |
| `dsh-agent-default-model` | 设置默认 provider/model（我们配了 ark+doubao） |
| `dsh-agent-instructions` | 加载并注入 AGENTS.md 指令 |
| ⭐ `dsh-system-prompt` | 提示词组装：把 persona/环境/工具 schema 拼成发给模型的系统提示 |
| ⭐ `dsh-tools` | 工具注册表 + 受控执行管道（门禁/超时/重试/结果） |
| `dsh-agent-tool-presentation` | 工具呈现模式（native/code/both） |

## 三、模型层

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-llm` | LLM 服务：适配器注册表 + 统一流式调用 |
| `dsh-llm-deepseek` | DeepSeek 官方 API 适配器 |
| ⭐ `dsh-llm-pi-ai` | **通用适配器**：接任意 OpenAI 兼容端点（我们用它接 Ark） |
| ⭐ `dsh-llm-retry` | 模型请求失败重试（退避策略，监听 agent/request-error） |

## 四、会话与记忆

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-session` | 会话抽象：SessionId、事件日志、消息 |
| ⭐ `dsh-session-persistence-jsonl` | **会话持久化**：append-only zstd JSONL |
| `dsh-session-query-sqlite` | 会话 SQLite 索引（支撑 UI 搜索会话） |
| `dsh-session-projection` | 会话投影注册表（派生视图） |
| `dsh-session-telemetry-otel` | 可观测性/轨迹（OpenTelemetry） |
| `dsh-session-checkpoint-policy` | 持久化检查点（工具副作用前落盘） |
| ⭐ `dsh-compaction-basic` | **上下文压缩**：长对话摘要旧文、保留尾部 |
| `dsh-compaction-tool-result-pruner` | 超大工具结果裁剪（压缩前的预处理） |
| `dsh-spill-local` / `dsh-spill-policy` | 超大工具输出"溢出"到会话文件（不占上下文） |
| `dsh-token-meter` | token 用量计量（压缩/计费的依据） |
| `dsh-session-title` / `dsh-session-title-first-prompt-llm` | 自动生成会话标题 |

## 五、工具（手脚）

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-tool-fs` | 文件读写（read/write/edit） |
| `dsh-tool-fs-search` | 文件/目录搜索（rg） |
| ⭐ `dsh-tool-bash` / `dsh-tool-pwsh` | shell 命令执行（Windows 用 pwsh） |
| `dsh-tool-web` / `dsh-web-search-deepseek` | 网页搜索 |
| `dsh-tool-skill` | 技能调用 |
| ⭐ `dsh-tool-subagent` | **子代理工具**（拉起专家）——主管+专家核心 |
| `dsh-tool-subagent-fork` | fork 子代理（带父级历史） |
| `dsh-tool-subagent-control` / `-report` / `-list-agents` | 子代理管理/结果回传/列表 |
| `dsh-tool-workflow` | 工作流编排 |
| `dsh-tool-todo` | 任务清单（todo_write） |
| `dsh-tool-goal` | 目标管理（create/update_goal） |
| `dsh-tool-ralph` | 把目标依次交给多个全新子代理的编排策略 |
| `dsh-tool-str-replace-editor` | 字符串替换编辑器 |
| `dsh-tool-jobs` | 后台任务（job_output/job_kill） |
| `dsh-tool-call-timeout-policy` | 工具调用超时策略 |
| `dsh-repeat-tool-reminder` | 重复调用提醒（防止工具循环卡死） |

## 六、子代理与工作流

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-subagent` | 子代理服务：注册表、生命周期、可继续子代理 |
| ⭐ `dsh-subagent-spawn-in-process` | spawn 提供方：创建独立上下文子 Agent |
| `dsh-subagent-fork-in-process` | fork 提供方：克隆父级历史子 Agent |
| `dsh-workflow-worker-thread` | 工作流在 worker 线程执行 |

## 七、沙箱与审批（安全）

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-sandbox-policy` | 沙箱策略归属（read-only/workspace-write/full-access） |
| `dsh-sandbox-local` | 本地沙箱实现 |
| ⭐ `dsh-fs-sandbox` | 文件系统沙箱：写操作路径围栏 |
| `dsh-bash-sandbox` / `dsh-pwsh-sandbox` | shell 命令内核级隔离（Windows 用 pwsh） |
| `dsh-subprocess-local` | 子进程管理 |
| `dsh-shell-env` | shell 环境变量 |
| `dsh-fs-observation-policy` | 文件系统观察策略 |
| ⭐ `dsh-user-approval` | 审批策略（ask/never） |
| `dsh-permission-presets` | 权限预设（read-only=ask / workspace-write=ask / full-access=never） |

## 八、技能 / 命令 / 目标 / 计划

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-skill` | 技能系统（可复用流程，类似 Codex Skills） |
| `dsh-skill-filesystem` | 技能文件系统 |
| `dsh-skill-badge` | 技能徽章（UI 展示，默认禁用） |
| `dsh-commands` | 插件面向用户的命令注册表 |
| `dsh-command-feedback` | 命令执行反馈 |
| `dsh-command-goal` / `dsh-command-compact` | 目标命令 / 压缩命令 |
| ⭐ `dsh-goal` / `dsh-goal-round-driver` | 目标状态机 + 同会话目标续行驱动 |
| ⭐ `dsh-plan-mode` | 计划模式（先规划后执行） |

## 九、UI 与用户交互

| 插件 | 作用 |
|---|---|
| ⭐ `dsh-web` | **Web UI**：工作区、会话、轨迹、设置 |
| `dsh-user-questions` | 用户提问服务（工具需要人工决策时询问） |
| `dsh-attachment-local` | 本地附件（图片等） |
| `dsh-jobs-local` | 本地任务队列 |
| `dsh-settings-file` | settings.yaml 配置管理 |
| `dsh-credentials-local` | 凭据存储（API key） |
| `dsh-storage` | 非会话数据的存储中心（KV） |
| `dsh-code-runtime-worker-thread` | 代码运行时（PTC 模式用） |

## 十、我们项目自己加的

| 插件 | 作用 |
|---|---|
| ⭐ `tool-ops-expert` | 运维诊断专家（一个 dsh-tool-subagent 实例，toolName=delegate_diagnosis） |

---

## 面试要点

- 这 ~80 个插件不是"八十个独立功能"，而是**同一棵树上的分层**：基础设施→核心→能力→UI
- 前端设置里看到的插件 = 这棵树的**可见部分**；工具类插件通过 `--dump-config` 看更全
- 你实际只需认识带 ⭐ 的约 15 个，其余"存在但不用改"
- 你真正会动的是：模型适配器（pi-ai）、子代理（subagent）、工具（自己 register）、记忆（自研）
