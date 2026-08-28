# DeepSeek Harness 框架详解 —— 核心机制深度拆解

> 本文回答你上次列出的每个问题：preset 到底是什么、记忆怎么存储/压缩/检索、工具怎么实现、沙箱怎么工作、agent loop 怎么兜底/熔断、以及如何接入自定义插件。
> 全部基于 dsh 0.1.1-rc.2 的实际源码/文档，不是泛泛而谈。

---

## 〇、先建立总览：一次对话请求的完整生命周期

理解框架最好的方式，是看"用户发一句话"后发生了什么：

```
用户输入
  │
  ▼
① Agent Loop 组装上下文
   ├─ 会话历史（session 重放 / 压缩摘要）
   ├─ AGENTS.md 指令（agent-instructions 注入）
   ├─ 工具 schema 列表（tools 注册表 → system-prompt）
   ├─ sandbox:policy（当前沙箱模式）
   └─ persona / system-prompt
  │
  ▼
② 调模型（llm.stream → 适配器 → Ark/DeepSeek API）
   │   失败 → agent/request-error → llm-retry 重试（退避）
   │   溢出 → compaction 压缩旧上下文
  │
  ▼
③ 模型返回：文本 或 工具调用
   │
   ▼
④ 工具执行流水线
   tools/pre-execute（允许/拒绝门禁）→ guard → tools/execute
   （超时/重试包装）→ tools/post-execute → finalizeContent → tools/result
   │
   ▼
⑤ 工具结果作为新上下文 → 回到 ② 继续循环，直到完成
  │
  ▼
⑥ 结果写入会话日志（session.jsonl.zstd，append-only）
```

每一层都对应一组插件。下面逐个拆解。

---

## 一、Agent Loop 层（循环 + 兜底 + 熔断）

**载体**：`dsh-agent-loop` —— 全框架**唯一**包含循环逻辑的包。

### 循环结构
每个 agent 有一个 `SessionId`，循环驱动三个粒度：
- **会话（session）**：从创建到关闭
- **轮次（turn）**：一次用户输入到回复完成
- **步骤（step）**：一次模型请求 + 一次工具执行

### 兜底 / 熔断机制（面试重点）

| 故障 | 机制 | 具体行为 |
|---|---|---|
| **模型 API 失败** | `agent/request-error` 事件 + `dsh-llm-retry` | normal 模式：对 `EMPTY_RESPONSE/RATE_LIMIT/SERVER/TIMEOUT/TRANSPORT` 重试 5 次，指数退避 500ms→10s + 10% jitter；always 模式：无限重试。**每次重试开新轮次** |
| **上下文溢出** | compaction 在 `agent/request-error` 上做"规范溢出修复" | 见记忆章节 |
| **工具执行失败** | 插件/工具失败**结束当前轮次**，不结束循环 | 失败结果回灌模型，模型可换策略重试 |
| **超时** | `tools/execute` 的 timeoutMs | 超时产生 `TOOL_TIMEOUT`，结果回灌模型 |
| **取消/中断** | 协作式 `AbortSignal` | 用户取消 → 中止信号；已输出前缀保留进历史（`interrupted: true`） |
| **未分发工具调用** | 合成结果 | `ABORTED_BEFORE_DISPATCH`，模型知道工具没跑 |

**关键设计**：恢复逻辑通过事件（`agent/request-error`）而非硬编码——这就是插件扩展点。你想加"自定义兜底"（比如模型挂了切备用模型），监听这个事件即可。

### 配置式定义 agent
```ts
// dsh-agent-loop 的 config
interface Config {
  maxParallelToolCalls?: number  // 默认 10；1 = 串行
  agents: Array<{
    id: string, provider?, model?, maxTokens?,
    resumeSessionId?, cwd?
  }>
}
```
说明：可以用配置声明多个 agent（不同模型/工作目录），不只是靠 UI 交互创建。

---

## 二、记忆系统（存储 / 压缩 / 检索）

### 2.1 存储（session-persistence-jsonl）

**物理格式**：每个会话一个 append-only 事件日志，默认 zstd 压缩：
```
~/.dsh/sessions/--<cwd>--/<session-id>/session.jsonl.zstd
```
- 首行是不可变的 `SessionHeader`：`{ type:'session', version, id, cwd, createdAt, parentSession, seedLength, delegationDepth, agentPreset }`
  - **`delegationDepth`**：子代理委派深度（顶层=0）
  - **`agentPreset`**：必须持久化——因为它决定恢复会话时用什么工具/提示词
- 之后每行一条会话事件：用户消息、系统提示、工具调用/结果、推理链、模型输出、**压缩摘要**
- `packChunks`（默认开）：连续 3 个以上同 block 的 delta 分片打包成一行，实测省约 60% 体积
- 写批合并：200ms 固定窗口批量落盘

**崩溃语义**：写入有 checksum frame（每批一个），损坏帧可检测。

### 2.2 压缩（compaction-basic）—— 长对话的"兜底"

**触发**：`thresholdRatio` 默认 0.8 —— token 用量达到 `contextWindow × 0.8` 时压缩。
**策略**：
- **保留近期尾部**：`retainRatio` 默认 0.16 —— 保留最近 16% 的表层原样
- **摘要旧上下文**：调用 `llm.stream()` 让模型把被压缩的部分**摘要**成一段文本，替换成 `<compacted-summary>` 标签
- **工具结果剪枝**（可选 pruner）：超大工具结果先裁剪再决定要不要摘要
- **KV Cache 友好**：摘要请求回放会话前缀，复用提供方热缓存

**溢出恢复（熔断）**：如果模型直接报 context window 溢出（不是渐进压力），compaction 在 `agent/request-error` 上做强制压缩：剪枝 → 压缩最旧单元 → 重试，最多重试 `compactionRetries` 次。

**模型看到的**：压缩后，模型只看到"摘要 + 最近 16% 原文"，而不是全部历史。

### 2.3 检索（session-query-sqlite）

- 会话历史建 **SQLite 索引**，支撑 UI 的"搜索会话"
- **注意**：这是给 UI/开发者用的检索，**不是模型的记忆工具**

### 2.4 关键结论：dsh 没有"记忆工具"

dsh 的记忆 = **自动**的：历史自动注入 + 自动压缩。模型**没有**"保存记忆/检索记忆"这样的工具调用（不像某些 agent 框架有 memory recall/save 工具）。

**如果你想做长期记忆**（跨会话记住用户偏好/历史事实）——必须自己写：写一个工具插件（`memory_save` / `memory_query`），后端接向量库或数据库，把它挂到 agent 的工具集。**这是你项目里一个很大的自研点**。

---

## 三、工具系统（注册 / 执行流水线 / 守卫）

**载体**：`dsh-tools` 工具注册表 + 执行流水线。

### 注册 API（写自定义工具的入口）
```ts
ctx.tools.register({
  name: 'my_tool',
  description: '...',
  parameters: { ... schema ... },
  output: { schema, render },          // 必填
  execute(args, exec) { ... },         // 必填执行器
  timeoutMs?, isConcurrencySafe?       // 可选
})
```

### 执行流水线（每次工具调用都走）
```
tools/pre-execute（可扩展门禁，可拒绝）
  → guard（单调守卫，拒绝返回理由）
  → tools/execute（超时/重试/指标包装）
  → tools/post-execute（检查/替换结果）
  → finalizeContent（唯一能改最终给模型的内容）
  → tools/result（只读通知）
```

### 关键概念
- **呈现模式**：`native`（function calling）/ `code`（保留 run_code 传输）/ `both` —— 决定工具怎么暴露给模型
- **作用域**：`ctx.tools.register` 在普通上下文 = 全局；在 `agent.ctx` = 只给该 agent
- **`ctx.tools.restrict(filter)`**：agent 级 allow/deny 掩码（subagent 的 toolFilter 就是这么实现的）
- **`ctx.tools.guard(guard)`**：安全门禁，可拒绝指定工具调用
- **取消**：协作式 `AbortSignal`，工具必须观察并停止
- **并发**：`isConcurrencySafe()` 分类器决定能否并行

### 内置工具清单（dsh-tool-* 插件）
fs 读写/搜索、bash/pwsh 命令、web 搜索、skill、subagent、workflow、todo、goal、str-replace-editor、jobs……

**你的自定义工具**（如 `run_vision_model`、`query_cls_logs`）就是按上面的 register API 写，然后挂载（见第七节）。

---

## 四、子代理系统（主管+专家的实现）

**载体**：`dsh-subagent`（服务）+ `dsh-subagent-spawn-in-process` / `fork-in-process`（提供方）+ `dsh-tool-subagent`（模型工具）。

### 三种模式
| 提供方 | 上下文 | 生命周期 | 适用 |
|---|---|---|---|
| `spawn` | 全新，**看不到父级历史** | one-shot（用完即焚）或后台 | 独立专家（当前用这个） |
| `fork` | **克隆父级已完成历史** | one-shot | 需要上下文延续 |
| `continuable` | 自己的持久会话 | 可继续（send_message 发后续） | 长期专家 |

### 子代理继承什么
- 默认继承父级**模型**、cwd、preset 组装（composeFrom）
- 权限：继承父级沙箱，**审批固定 never**（防越权）
- 子代理能力：`outputSchema`（结构化输出）、`depthLimit`（委派深度，默认3）、`toolFilter`（工具限制）、`persona`

### 一个专家 = 一个 subagent 工具实例
不同 `toolName` + `persona` + `toolFilter`。换专家 = 加一个新实例。

---

## 五、沙箱与审批

### 沙箱策略（sandbox-policy）—— 唯一的策略归属
三种模式（默认 **read-only**，故障安全）：
- `read-only`：文件读允许，**写全拒**（`FS_SANDBOX_DENIED`）
- `workspace-write`：写只允许在"工作区根 + /tmp"内
- `danger-full-access`：不加围栏

逐会话切换：追加一条 `sandbox/mode` 事件（回放保留），不会两个会话串状态。

### 两层隔离（面试要分清）
1. **fs-sandbox**：进程内路径围栏（检查目标路径是否在允许范围内）——"策略围栏"，不是安全边界
2. **bash-sandbox / pwsh-sandbox**：shell 命令的内核级隔离——Windows 上用 pwsh-sandbox

### 审批（user-approval + permission-presets）
- `approval_policy`：`ask`（越界操作问用户）/ `never`（不审批）/ 细粒度
- 默认权限预设：read-only(ask) / workspace-write(ask) / full-access(never)
- 子代理审批被固定为 `never`（继承沙箱内活动，禁止提权）

---

## 六、Agent Preset —— 到底是什么、怎么实现

### 概念
**Preset = 一个目录，内含一份 `agent.cordis.yml`，定义"这个 agent 用哪些工具 + 带哪些系统提示段"**。它是角色/能力的**结构化封装**。

目录结构：
```
presets/<preset-id>/
  agent.cordis.yml     # 插件行列表：挂哪些工具插件、注入哪些提示
  skill/               # 可附带技能
```

### 怎么实现（关键）
1. **创建**：`ctx.agentPresets.copy(sourceId, newId)` —— 从已有 preset 复制成新目录（唯一创作入口），然后编辑 `agent.cordis.yml`
2. **挂载**：会话创建时 `mount(id)` → agent 的 scope key 认父到该 preset → 获得 preset 定义的工具和提示
3. **子代理继承**：`composeFrom(parent)` —— 子代理加入父级正在运行的 preset 组装
4. **生效**：preset 决定**模型看到的工具 schema 和提示词**；会话 header 记录用的哪个 preset，恢复时重建同样的组装

### 和 AGENTS.md 的区别
| | AGENTS.md | Preset |
|---|---|---|
| 形态 | 纯文本指令 | 结构化目录（工具插件 + 提示段） |
| 工具 | 不能挂工具 | **能挂/卸工具** |
| 可复制 | 手动复制文件 | `copy()` 原生支持 |
| 版本化 | 无 | 目录可版本化 |

**实践建议**：主管/运维专家各建一个 preset（如 `supervisor`、`ops-diagnoser`），比现在的"AGENTS.md + persona"更符合 dsh 的组装哲学，也是面试能讲的"我对角色做了结构化封装"。

---

## 七、如何接入自定义插件（开发者的核心动作）

### 插件是什么
一个 Cordis 插件 = 一个 Node 模块，导出固定接口：
```ts
export const name = 'my-plugin';
export const inject = ['tools', 'llm'];   // 依赖的服务
export const Config = z.object({ ... });   // 配置 schema
export function apply(ctx, config) {
  ctx.tools.register({ ... });             // 注册工具
  // 或 ctx.on('agent/request-error', ...) // 监听事件
}
```

### 接入三步
1. **写插件**：放项目里（如 `plugins/my-ops-tools/`）
2. **安装到 profile**：`dsh plugin --profile web add <路径或包名>`（装进 profile 的 node_modules）
3. **挂载**：在 `cordis.patch.yml` 里用 `insert` 块加一条：
   ```yaml
   - insert:
       - id: my-ops-tools
         name: 'my-ops-tools'      # 或 'file:///绝对路径/index.js'
         config: { ... }
   ```

### 判断哪些自己写（决策矩阵）
| 需求 | 自带还是自研 | 怎么做 |
|---|---|---|
| 对话/loop/多轮记忆 | 自带 | 直接用 |
| UI/工作区/搜索 | 自带 | 直接用（白标再替换） |
| 沙箱/审批 | 自带 | 配置 |
| 领域工具（视觉研判/日志查询） | **自研** | 写工具插件 register |
| 长期记忆 | **自研** | 记忆工具 + 向量库 |
| 角色封装 | 机制自带、内容自研 | 建 preset |
| 自定义兜底（切模型） | **自研** | 监听 agent/request-error |
| 白标前端 | **自研** | 替换 dsh-client-ui-* |

---

## 八、面试自检：你该能回答这些问题

1. agent 请求的完整生命周期？（总览图）
2. 长对话超过 context window 会怎样？（compaction：摘要+保留尾部）
3. 模型 API 挂了会怎样？（llm-retry 退避重试，agent/request-error 事件）
4. 记忆存在哪、什么格式？（zstd jsonl，append-only）
5. 怎么防止子代理越权？（继承沙箱 + 审批 never）
6. 你加过什么？（自定义工具插件 / preset / 记忆层——讲出真实实现）
7. 为什么选 dsh 不自己写？（agent loop/沙箱/UI 都是成熟实现，你专注领域能力——这是工程判断）

---

## 九、配置机制原理与代码结构（回答"为什么只有配置就能跑"）

### 9.1 配置树：`id` / `name` / `config` / `insert` 到底是什么

dsh 启动时，Cordis loader 把**多份 patch 层叠**成一张"配置树"（plugin entry 列表）。树里的每个条目长这样：

```yaml
- id: tool-ops-expert        # ① 实例标识
  name: '@deepseek-ai/dsh-tool-subagent'  # ② npm 包名
  config: { provider: spawn, toolName: delegate_diagnosis, ... }  # ③ 插件配置
```

| 字段 | 含义 | 类比 |
|---|---|---|
| **`id`** | 该**插件实例**在配置树中的唯一标识。同一个插件可以加载多个实例（id 不同），比如多个专家工具 | 变量名 |
| **`name`** | 实际要加载的 **npm 包名**。`@deepseek-ai/dsh-tool-subagent` 中 `@deepseek-ai` 是 npm 组织 scope，`dsh-tool-subagent` 是包名 | import 的模块路径 |
| **`config`** | 传给插件 `apply(ctx, config)` 的配置对象，决定这个实例的行为 | 构造函数参数 |
| **`insert`** | patch 的一种操作：把**一段新条目**插入配置树（新增实例）；相对的 `- id: xxx` 是**覆盖**已有条目的 config | 数组 push vs 对象覆盖 |

配置树的层叠顺序（后写覆盖先写）：
```
bundle patch（dsh 自带核心）→ profile cordis.patch.yml → home 级 → --patch 覆盖
```

### 9.2 为什么"配置一下"就创建了功能？

因为**插件代码已经存在于 node_modules 里，配置只是"声明启动它们"**。Cordis loader 对配置树的每个条目执行：

```
import(name)             # ① 用 Node ESM import() 按包名加载插件模块
   ↓
new Entry(...)           # ② 创建插件实例
   ↓
apply(ctx, config)       # ③ 调用插件的 apply 函数，注入依赖服务
   ↓
ctx.tools.register(...)  # ④ 插件内部注册工具 / 监听事件 / 提供服务
   ↓
功能可用
```

**这就是"声明式装配"**：你声明"加载哪个包 + 给什么配置"，loader 负责加载、实例化、依赖注入、生命周期。dsh 的几十个插件（agent-loop、工具、会话、UI…）都是这样被"装配"起来的。

### 9.3 代码到底在哪里？

| 位置 | 内容 |
|---|---|
| `C:\Users\admin\.workbuddy\binaries\node\versions\22.22.2\node_modules\@deepseek-ai\dsh` | **dsh CLI 本体**（启动器、profile 解析、命令） |
| `~/.dsh/profiles/node_modules/@deepseek-ai/dsh-*` | **全部插件包**（agent-loop、tools、session、UI… 几十个，pnpm 安装） |
| `github.com/deepseek-ai/deepseek-harness` | **框架源码仓库**（pnpm workspace，所有包源码） |
| 你的项目 `deepseek-ops-assistant/` | 只有：`cordis.patch.yml`（声明）+ `AGENTS.md`（指令）+ `PLAN/指南`（文档） |

所以你的项目目录"只有配置和 AGENTS.md"是**正常的**——可执行代码全部在 dsh 安装目录里，你的项目是"**装配声明 + 角色定义**"。

### 9.4 记忆持久化在哪？

`~/.dsh/sessions/--<工作区>--/<会话id>/session.jsonl.zstd`（`DSH_HOME` 环境变量可改到项目内）。凭据 `~/.dsh/.credentials.yaml`，设置 `~/.dsh/settings.yaml`。

### 9.5 目前是"简单应用"吗？后续要基于源码开发吗？

**它不是"简单应用"，而是"浅层使用框架"**。按深度分层：

| 层级 | 做什么 | 需要 |
|---|---|---|
| **浅层（当前）** | 组合已有插件 + 配置 + 提示词 | 无代码 |
| **中层** | 写自定义工具插件 / preset / 记忆层 | 写插件（不碰源码） |
| **深层** | 改 agent loop / 替换 UI / fork 框架 | 基于源码开发 |

**是否要基于源码**：**不是必须**。
- 你的领域能力（视觉研判、日志查询、长期记忆）→ **写插件**，不需要源码
- 只有当你需要**改变框架自身行为**（自定义 loop 语义、深度白标 UI）才需要 fork 源码
- 对面试项目：**写到中层就足够有说服力**——能讲清"框架提供什么、你加了什么"，这比"我改了框架源码"更能体现工程判断

**一句话**：当前项目的 90% 能力来自 dsh 的插件生态，你的价值体现在"装配 + 角色设计 + 领域插件"——这也是真实企业里用 agent 框架的正确姿势。

---

## 十、四个核心机制的源码级拆解

> 这一节回答你最新的疑问：AGENTS.md 到底怎么进到模型？`agent/request-error` 事件是什么？
> `dsh-llm-retry` 具体怎么重试？工具执行流水线的完整阶段是什么？
> 全部来自 `~/.dsh/profiles/node_modules/@deepseek-ai/` 下插件的 README 与源码（0.1.1-rc.2）。

### 10.1 `agent-instructions`：AGENTS.md 注入的完整机制

**载体**：`dsh-agent-instructions`（lib/index.js + invariant.js）。

**一句话**：它把 `$DSH_HOME/AGENTS.md` + 工作区各级 `AGENTS.md` 作为**持久的 user 角色指令**注入每个会话，并随文件系统活动**增量更新**。

#### ① 注入时机

不是每次请求都注入，而是：**每个实时会话第一个符合条件的 `agent/pre-step` 时组合基线**（baseline）。组合完成后，直接提示词与持久基线一同进入 step 1，共同抵达第一次模型请求。被拒绝或为空的第一次决策会把基线留在 agent 的 `next-step` inbox，等待后续唤醒。

#### ② 读取哪些文件（扫描规则）

loader 依次读取：
1. `$DSH_HOME/AGENTS.md`（全局）
2. 项目根目录到 `agent.session.header.cwd`（会话工作目录）的**每一级目录**，每级读取"基础候选文件 + 本地 overlay 候选文件"（即 `AGENTS.md`、`CLAUDE.md` 等约定文件名）

**去重折叠**：同一目录内，如果候选文件去掉首尾空白后**字节完全一致**（比如 `CLAUDE.md` 只是复制了同级 `AGENTS.md`），就折叠到最早那个候选，只渲染一次。

#### ③ 注入后的提示词形态（模型实际看到的）

基线指令是持久 `user` 角色消息，包在 `system-reminder` 框架里：

```
<system-reminder>
The following workspace instructions may be relevant to your work. Use them as guidance when applicable.
More specific instructions take precedence over broader ones. They do not override system, developer, or direct user instructions.

Instructions from: ~/.dsh/AGENTS.md

(全局 AGENTS.md 内容)

Instructions from: AGENTS.md

(项目 AGENTS.md 内容)
</system-reminder>
```

**优先级语义**（框架写死的）：更具体的指令 > 更宽泛的指令；且**永远不能覆盖** system / developer / 直接用户指令。

#### ④ 增量更新（关键设计）

插件观察第一方 `read`/`write`/`edit` 工具**成功后的结果**，对每个"触摸"过的路径检查是否触及新的 scope：
- 新出现的指令文件 → 排入一条**新增** `user/message`：
  ```
  <system-reminder>Additional instructions from: packages/app/AGENTS.md ...</system-reminder>
  ```
- 已加载文件被修改 → 排入一条**替换**：`Updated instructions from: <path>`，说明用新内容替代之前加载的内容
- 文件消失或变成同级较早候选的重复 → 排入**移除**：`Instructions removed: <path>`

这样**不用重启会话**，改 AGENTS.md 后模型下一轮就能看到新指令。

#### ⑤ 两个防破坏细节

- **转义**：指令内容里出现字面 `</system-reminder>` 会被转义，防止仓库控制的文本"关闭"插件控制的框架（防 prompt 注入）。
- **恢复会话**：`resumeSessionId` 恢复时会保留一条兼容的可见基线，只追加当前文件的转换；如果发现路径/优先级/项目根变化，则折入一条明确取代旧基线的完整基线。

**面试点**：AGENTS.md 不是"每次拼进 system prompt"，而是**持久 user 消息 + 按文件系统活动增量维护**的机制——这是它和你手动写 system prompt 的本质区别。

---

### 10.2 `agent/request-error` 事件：模型请求失败的恢复扩展点

**载体**：`dsh-agent-loop/lib/index.js`（第 653 行附近）派发。

**它到底是什么**：一个**可恢复的 waterfall 事件**，在"模型请求终止失败"时触发。它是整个框架"模型故障兜底"的**唯一扩展点**——所有恢复逻辑（重试、压缩）都挂在这个事件上，而不是硬编码在循环里。

#### 触发条件（源码级）

```js
const finish = assembler.finish;
if (finish.kind === "error" || finish.kind === "aborted") {
  const action = await this.dispatch.waterfall("agent/request-error", {
    turn, step,
    provider: request.provider,
    failure: finish.failure,          // { message, code }
    retryPolicy: preparedCall?.retryPolicy,  // 适配器捕获的不可变策略
    signal
  }, () => Promise.resolve(void 0));
  signal.throwIfAborted();
  if (action?.kind !== "retry") throw new LlmError(finish.failure.message, finish.failure.code, ...);
  continue;                           // ← 重试：开新轮次继续循环
}
```

#### 载荷字段

| 字段 | 含义 |
|---|---|
| `turn` / `step` | 失败发生在哪个轮次/步骤（重试编号按这个对齐） |
| `provider` | 出错的提供方路由 |
| `failure` | `{ message, code }`，code 是分类码（如 `RATE_LIMIT`/`TIMEOUT`/`SERVER`…） |
| `retryPolicy` | **preparedCall 捕获的不可变重试策略**（适配器注册时快照）；若 middleware 接管了未准备路由，则缺失 |
| `signal` | 轮次取消信号 |

#### 恢复协议

- 监听器返回 `{ kind: 'retry' }` → 循环 `continue`，**开新编号轮次**重试
- 不返回 / 返回 undefined → 失败是**终态**，抛出 `LlmError` 结束轮次
- **谁在监听它**：
  - `dsh-llm-retry` → 退避重试
  - `dsh-compaction-basic` → 上下文溢出的强制压缩修复

**面试点**：这就是"插件化兜底"——框架把故障点定义成事件，恢复策略由插件决定。你要做"模型挂了自动切备用模型"，就是加一个监听器。

---

### 10.3 `dsh-llm-retry`：退避重试的源码实现

**载体**：`dsh-llm-retry/lib/index.js`（共 164 行）。

#### 架构定位

它不是包装 `ctx.llm.stream()`，而是**监听 `agent/request-error` 并返回 retry 动作**。每次适配器调用 = 一次提供方尝试；**每次重试 = 一个新的编号轮次**。

#### 两种模式

| 模式 | 行为 | 默认 |
|---|---|---|
| `normal` | 对 `EMPTY_RESPONSE`/`RATE_LIMIT`/`SERVER`/`TIMEOUT`/`TRANSPORT` 5 类失败重试 **5 次**，500ms→10s 指数退避 + 10% jitter | 省略策略时用 |
| `always` | 先请求下游恢复，再无次数上限重试每次模型请求失败；成功/取消/dispose 才终止 | 需显式配置 |

配置挂在**提供方 profile 里**（pi-ai 的每个 provider 下），不单独写执行器配置：

```yaml
- name: '@deepseek-ai/dsh-llm-deepseek'
  config:
    apiKeyEnv: DEEPSEEK_API_KEY
    retryPolicy:
      mode: always
      backoff:
        initialDelayMs: 1000
        maxDelayMs: 30000
        jitterRatio: 0.2
```

#### 退避算法（源码 `localDelay`）

```js
function localDelay(config, retry, random) {
  const exponent = Math.min(retry - 1, 1024);
  const exponential = Math.min(config.initialDelayMs * 2 ** exponent, config.maxDelayMs);
  const jitter = 1 - config.jitterRatio + 2 * config.jitterRatio * random();
  return Math.min(exponential * jitter, config.maxDelayMs);
}
```

即：`delay = min(initial × 2^(retry-1), maxDelay) × 对称 jitter`，再封顶到 maxDelay。

#### recover() 完整流程（源码级）

1. `always` 模式先按 waterfall 顺序调用下游恢复策略（先接受下游重试再自己回退）
2. `normal` 模式检查 `failure.code` 是否在 `retryableCodes` 里，不在 → `next()` 放行（不重试）
3. 计算 `policyKey`（canonical key：mode + maxRetries + 排序后的 retryableCodes + backoff 参数）
4. 从会话历史 `findLast` 查找同 `turn + step + provider + policyKey` 的 `llm/retry` 事件 → 得出 `previousRetry` → `retry = previousRetry + 1`（**换策略就开新历史**）
5. `localDelay()` 算延迟（若提供方给了 `providerRetryAfterMs` 且 ≤ maxDelayMs，则用它替换本地退避且不加 jitter）
6. 追加**非表层**事件 `llm/retry`（含 retryId/provider/mode/policyKey/failure/planned delay）
7. `cancellableDelay()` 可取消等待；等待完成追加 `llm/retry-started`（退避期间取消则不写）
8. 返回 `{ kind: 'retry' }` → 循环开新轮次

#### 关键行为

- **模型看不到重试**：重试事件、延迟、错误、失败的部分输出**全部不进表层**；重试轮次从持久历史重建**完全相同的请求**，失败分片绝不进入派生消息
- **Token 影响**：每次重试是新请求，可能重复计费输入 token；normal 有预算上限，always 无上限
- **KV Cache**：重建请求保留此前缀，可复用提供方热缓存；非表层事件不改变 cache 身份

---

### 10.4 `dsh-tools`：工具执行流水线的完整阶段

**载体**：`dsh-tools`（工具注册表 + 执行流水线）。每个工具调用走一条固定流水线：

```
模型产生工具调用
  ↓
① 参数快照与冻结（无损，分配不透明 token）
  ↓
② tools/pre-execute ── 可重排的 waterfall 门禁（allow / deny / ask）
  │                    （挂载 approval seam 时 ask 由它处理，否则退化为拒绝）
  ↓
③ ctx.tools.guard() ── 单调同步守卫（返回理由=拒绝；undefined=保持原决定）
  │                    （后面的 waterfall 无法把拒绝改回允许）
  ↓
④ tools/execute ──── 环绕分发包装层（超时/重试/指标插件挂这里）
  │                    （包装层只能替换 signal，进工具主体前会重新合并原始信号）
  ↓
⑤ 工具主体 execute(args, exec) 执行
  │                    （返回 output schema 声明的规范 JSON；遵守 exec.signal 协作停止）
  ↓
⑥ tools/post-execute ── 检查/替换结果、附加上下文
  │                    （接受：可替换 content 或 value（二选一）+ 附加 additionalContexts；
  │                       阻止：变成无值失败）
  ↓
⑦ finalizeContent ── 工具定义持有的同步回调，每个规范化结果恰好运行一次
  │                    （包括绕过后置策略的失败；只能替换 content）
  ↓
⑧ tools/result ──── 仅观测的实时通知
```

#### 各阶段细节

**① 参数快照**：`ctx.tools.execute(exec)` 无损快照并冻结参数，分配一个**不透明 `ToolExecutionToken`**（品牌 Symbol，只支持相等性关联，绝不跨模型/日志/worker 边界）。无效参数会走同一结果路径，但**不会到达策略或工具主体**。

**② pre-execute 门禁**：`PreToolDecision` = `{kind:'allow'}` / `{kind:'deny', reason}` / `{kind:'ask', reason?}`。**有意不提供输入改写**——门禁只管放行/拒绝/询问。`ask` 在挂载 approval 时处理，否则**退化为拒绝**。

**③ guard 单调守卫**：`(execution) => string | undefined`。返回字符串 = 最终**单调**拒绝理由；`undefined` = 保持前面的决定。后续 waterfall 监听器**无法**把 guard 的拒绝重新变允许——这是安全兜底的确定性。

**④ execute 包装层**：供超时/重试/指标插件做环绕分发。**只能替换 `signal`**（包装层自己不用负责执行工具），注册表在进入工具主体前把调用方原始信号重新合并回来。

**⑤ 工具主体**：`execute(args, exec)`，`exec.signal` 必填只读；每个异步工具必须观测或转发该信号，并**只能在自己的工作停止后结算**。

**⑥ post-execute**：`PostToolDecision`——接受决定可替换 `content` **或** `value`（不能同时），并可附加 `additionalContexts`；替换 value 会重新验证并重新渲染。阻止决定会把反馈变成无值失败，且**丢弃**工具延迟的上下文。

**⑦ finalizeContent**：工具定义持有的同步回调，**对每个规范化结果恰好运行一次**（包括绕过后置策略的失败），且只能替换面向模型的 `content`。它是"工具自己最后看一眼给模型的内容"的唯一钩子。

**⑧ tools/result**：只读观测通知。**注意区分**：`tools/result` 是实时事件；名称相近的 `tool/result` 是 agent loop 随后追加的**持久会话事件**。

#### 取消语义（源码 README 明确）

| 取消时机 | 结果 |
|---|---|
| 工具主体调用**前**发生取消 | `ABORTED_BEFORE_DISPATCH` |
| 工具主体被调用**后**取消 | 只能把成功结果替换为 `ABORTED` |
| 拒绝 / 包装层失败 / 工具失败 / 后置失败 / 超时 | 保留更具体的结果（如 `TOOL_TIMEOUT`） |

取消是**协作式**的：完整生命周期等待完全停稳（不会在已启动的同进程 Promise 未结算时提前返回）。

#### 三个关键 API 的作用域区别

| API | 普通上下文 | `agent.ctx` |
|---|---|---|
| `ctx.tools.register()` | 全局注册 | 只对该 agent 注册，遮蔽同名全局工具 |
| `ctx.tools.restrict(filter)` | 抛错 | agent 级 allow/deny 掩码（**subagent 的 toolFilter 就是它**） |
| `ctx.tools.guard(guard)` | 全局生效 | 只对该 agent 生效 |

**并发**：`ctx.tools.executionMode(exec)` 返回 `parallel` 的唯一条件是可见定义的 `isConcurrencySafe(exec.arguments)` 分类器恰好返回 `true`；未知/未声明/无效 → **独占**。

**呈现模式**（决定工具怎么暴露给模型）：`native`（函数定义）/ `code`（保留 `run_code` 传输）/ `both`。agent 可用 `presentAs()` 遮蔽默认值——**这就是 Codex 的 namespace 问题在 dsh 里不存在的答案：dsh 默认 native function calling，工具对 Ark 直接可见**。

**面试点**：工具流水线 = 3 道可转换 waterfall（pre-execute → execute → post-execute）+ 1 个内容终结器（finalizeContent）+ 1 个只读通知（result）。你的自定义工具插件写 `execute` 只是第 ⑤ 步，其余阶段由框架保证（安全、超时、取消、呈现）。

---

## 十一、把四个机制串成面试故事

面试被问"你理解 agent 框架的核心机制吗"，可以这样组织：

1. **指令注入**：AGENTS.md 不是拼进 system prompt，是 `agent-instructions` 在 `agent/pre-step` 组合成持久 user 消息，并随 fs 工具活动增量更新（新增/替换/移除），且做了 `</system-reminder>` 转义防注入。
2. **模型故障兜底**：模型请求终止失败时，agent-loop 派发 `agent/request-error` 事件（载荷含 provider/failure/retryPolicy）；`llm-retry` 监听它，按提供方策略计算指数退避（`min(initial×2^(n-1), max)×jitter`），追加重试事件后返回 `{kind:'retry'}`，循环开新轮次；重试对模型完全透明，且失败分片绝不进历史。
3. **长对话兜底**：compaction 也在 `agent/request-error` 上做强制压缩（这是另一个监听者）；渐进压力则在 pre-step 上按 0.8 阈值触发，保留 16% 尾部 + 摘要旧文。
4. **工具执行**：每次工具调用走 `pre-execute 门禁 → guard 单调守卫 → execute 包装 → 主体 → post-execute → finalizeContent → result`，取消/超时/并发有完整语义——我写的 `run_vision_model` 工具就是通过 `ctx.tools.register` 挂进这条流水线的第 ⑤ 步。

这四段就是"我不仅会配，还知道它为什么这么设计"。

---

## 十二、全框架机制图谱（剩余核心机制全量补齐）

> 前面各节拆了 loop/记忆/工具/子代理/沙箱/配置。这一节把**其余所有核心插件**的机制讲透，
> 按"一次对话请求里它们各自出现在哪一层"来组织。全部来自各插件 README 与源码。

### 12.1 系统提示词组装（`dsh-system-prompt`）——模型的"世界观"怎么拼出来

**定位**：提示词组装注册表。插件贡献**有序段**、**工具 schema**、**具名变量**；loop 在每个 step 组装一次，渲染成完整模型提示词。

**组装过程（源码级）**：
1. 收集**全局层 + agent 作用域层**的段，按 `order` 升序拼接
2. 分离工具 schema（经 `system-prompt/assemble` waterfall，替换监听器有权威）
3. 渲染：插值每段里的 `{{variable}}`，删空段，空行连接

**段顺序约定**（`PromptSection.order`）：

| order | 段 | 内容 |
|---|---|---|
| `-100` | `harness:identity` | 固定开场白 `You are an AI agent powered by DeepSeek Harness.`（可关） |
| `0` | `deployment:persona` | 部署级 persona（唯一由配置提供的段；agent 作用域 persona 遮蔽它） |
| `100–199` | 工具引导段 | 每个工具插件自带跨调用指导（`tool:bash`、`tool:read`…） |

**关键机制**：
- **`complete` 段**：某个段标记 `complete: true` 时，组装后它成为**精确的完整提示词**（抑制其他所有段）——白标/完全自定义提示词的口子。
- **`toolOrder`**：显式指定工具排序；列表须恰好含一个 `<unlisted-tools>` 占位（其余按字典序插入该位置）。
- **变量插值**：loop 注册 `{{model}}`/`{{cwd}}`，插件可注册自己的（如 `{{date}}`）；`renderPrompt` 对**未知/无值/格式错误的变量引用直接抛异常**——明确失败优于交付坏提示词。
- **组装结果 `PromptAssembly`**：`{ sections, tools, variables }`——工具 schema 是组装结果的一部分（"模型知道自己能做什么"是连贯整体，尽管 wire 上 schema 是独立字段）。

**面试点**：提示词 = 有序段拼接 + 变量插值 + 工具清单，不是一段写死的字符串。你要改角色 = 加段/改 persona；要彻底换提示词 = 用 complete 段。

---

### 12.2 技能系统（`dsh-skill` + `dsh-skill-filesystem`）——可检索的领域 SOP

**定位**：模型可调用的**技能注册表**。技能不是提示词，是"带元数据的指令文件"，模型按需加载。

**结构**：注册表（`ctx.skills`）+ 提供方（filesystem 扫描本地目录解析 `SKILL.md`）。插件可注册自己的提供方（HTTP/嵌入式）。

**发现与加载**：
- 扫描 `$DSH_HOME/skills`、项目根、用户根（可配 `customSkillDirs`、`watch` 热更新）
- `ctx.skills.list/get/snapshot` 带 `cwd` 解析作用域合并，按名称排序，最近层直接赢重名
- `skills/change` 失效通知，消费方各自重新 `snapshot()`

**调用策略**（关键设计）：每个技能有 `invocation` 四象限——`modelInvocable` / `userInvocable` 各自独立：

| 策略 | 模型目录 | 用户目录 |
|---|---|---|
| true / true | 包含（工具） | 包含（命令） |
| true / false | 包含 | 排除 |
| false / true | 排除 | 包含 |
| false / false | 都排除 | 都排除 |

**面试点**：skill 是"按需加载的指令"（省 token、可复用、可版本化）；AGENTS.md 是"每次都注入的全局指令"。你要给专家挂领域 SOP（如"CPU 排查标准流程"），用 skill 而不是塞进 persona。

---

### 12.3 工作流引擎（`dsh-tool-workflow` + `dsh-workflow-worker-thread`）——模型自己写编排脚本

**定位**：模型可调用 `workflow` 工具，提交一段 **JS 编排脚本**，框架在 worker thread 里执行，脚本可**扇出多个 subagent**，返回最终值。

**工具 schema**：`workflow({ meta:{name,description}, script, args? })`。`script` 是纯 JS 脚本体（`try/finally` 确保每条路径 dispose）。

**执行隔离（源码级）**：每次运行一个全新 `worker_threads.Worker`：
- 同步脚本循环**不阻塞 harness 事件循环**
- 忽略取消的脚本可随 worker 一起 `terminate()`
- 空环境启动（凭据不通过 `process.env` 跨边界）；宿主/worker 消息结构化克隆 + JSON 校验
- **明确不是安全沙箱**：信任立场与 bash 等价（`node:vm` 是塑造 API 不是安全边界）

**契约**：非 `completed` 结束 → `isError` 结果（绝不把局部输出当成功）；完成返回 `{ runId, agentsStarted, result }`。

**配套 `ralph`（`dsh-tool-ralph`）——多子代理编排的标准模板**：
- 把一个**不可变目标**依次交给多个**全新子代理**，每轮拿到上一个的结构化交接内容
- 报告协议：`{ status: continue|complete|blocked, summary, evidence, nextSteps }`
- 工作区是**唯一长期记忆**（不复制父级对话、不复制旧子代理会话）
- 每轮通过 `subagentProvider`（必须 `inheritsParentContext: false`）启动

**面试点**：workflow = "模型自己写协调脚本"（高级但危险，需要 worker 隔离）；ralph = 框架提供的**固定多子代理流水线**。你的"主管→专家"用 subagent 单层即可，任务拆解流水线可讲 ralph。

---

### 12.4 目标状态机（`dsh-goal` + `dsh-goal-round-driver`）——同会话"长期任务"如何推进

**定位**：事件溯源的同会话目标状态。目标 = `{ phase: active|paused|completed|blocked, blockerReason, roundsStarted }`。

**服务动词**：`create / edit / pause / resume / complete / block / clear`。每次变更追加持久 `goal/change` 事件（带完整快照）；**会话日志是唯一持久权威**。

**Goal Round（核心机制）**：`goal-round-driver` 把 active 目标转成**连续自动轮次**：
- agent idle + 目标 active + 有剩余容量 → 驱动器排入 `<goal_round>` 提示词（`GoalMessageSource`）
- 自动工作**让行**：人类消息进入 inbox 则自动工作停下，agent 空闲后再继续
- `defaultMaxGoalRounds` 默认 256（防无限跑）
- **续行启用状态不持久化**：恢复/fork 后保留目标但**不自动续跑**，须显式 `resume`

**`plan-mode`（计划模式）**：软引导状态（`plan/mode` 事件，仅日志）：
- `/plan [message]` 进入、`/plan off` 退出、`exit_plan_mode` 工具需用户批准
- 激活时渲染 `plan:policy` 段；**不读写沙箱/审批状态**——硬限制仍由那两层强制

**面试点**：goal = "同会话多轮推进一件事"的机制（自动续跑 + 人类让行 + 预算上限）。比"无限循环做任务"更可控——这是长任务 agent 的工程答案。

---

### 12.5 审批全流程（`dsh-user-approval`）——"越界操作"怎么过审

**定位**：与通道无关的一次性审批 seam。`ctx.approval.request(req)` 返回 `allowed-once / rejected / cancelled / unavailable`。

**完整流程（源码级）**：
1. 工具流水线产生 `ask` 决定（`PreToolDecision`）→ 走审批 seam
2. 每次请求追加**一对审计事件** `approval/asked` + `approval/decided`
3. 应答者是 `approval/request` waterfall 监听器（UI、ACP 自动化桥接层…）；应答者缺失/失败 → **拒绝关闭**
4. 授权是 **once**：只对所请求的操作有效，不是持续授权

**策略**：`ApprovalPolicy` = `ask`（问应答者）/ `never`（拒绝）。取值最后一条 `approval/policy` 事件，回退配置。
- `never` 下模型会看到："Approval prompts are disabled in this session: actions that require approval are rejected automatically — do not request sandbox escalation"——**非升权后果**直接告诉模型，避免它反复试探。
- `ask` 下应答者不可用时 fail-closed。

**模型体验**：策略变化时注入完整运行时上下文快照；审计事件**只写日志**，模型只看到消费方返回的结果。

**面试点**：审批 = 工具流水线 `ask` 决定的落点，**audit 全留痕**（asked+decided 成对）、**授权一次性**、**无应答者 fail-closed**。你项目的写操作外置方案正是绕过这个 seam。

---

### 12.6 沙箱的两层实现（`dsh-fs-sandbox` + `dsh-bash-sandbox`）——进程内 vs 内核级

**第一层：fs-sandbox（进程内路径围栏）**
- `SandboxedFileSystem` 替换 `dsh-fs-local` 注册为 `ctx.fs`；**读取永远直接通过**，只拦 `writeText`/`editText`
- 按调用携带有效模式 + 不可变 cwd 根：
  - `read-only`：结构化 `FS_SANDBOX_DENIED` 拒绝所有变更
  - `workspace-write`：目标规范化后必须位于可写根（工作区根 + `/tmp`/`os.tmpdir()`）
  - `danger-full-access`：不加围栏
- 拒绝是结构化 `FsError`，**不经 stderr 文本推断**
- **威胁模型**：这是"可信代码检查模型控制的路径"，提供约束**不是安全边界**；TOCTOU（写入前重新规范化缩小）为模型接受

**第二层：bash-sandbox（shell 内核级隔离）**
- 把精确 `['bash','-c',command]` argv 交给平台 runner（Windows 用 pwsh-sandbox）
- **只限制文件影响**，网络不受限；无 runner 时 **fail-closed**（`SANDBOX_UNAVAILABLE`），绝不静默无约束运行
- 拒绝是**结果事实**：stderr 尾部匹配后端方言（EROFS/EACCES/EPERM）→ `sandbox.denied: true`
- 升权（sandbox_permissions）只改策略模式，会话根不变；批准问题在工具层（`dsh-tool-bash`），执行器只报告拒绝、绝不自己协商权限

**面试点**：两层隔离各管一段——**fs 管文件、shell 管命令**；fs 是策略围栏（快、结构化错误），shell 是内核边界（慢、真隔离）。`read-only` 下文件写死，命令写也死，双保险。

---

### 12.7 防死循环与超时（`repeat-tool-reminder` + `tool-call-timeout-policy`）

**repeat-tool-reminder（防工具死循环）**：
- **不是工具**：不出现在工具列表、不否决/不改写调用，只注入提示
- 链键 = `(工具名, 规范化参数)`（深排序 + JSON.stringify，属性顺序不同算相同）
- 默认 `thresholds: [3,5,8]`：连续同参调用 3 次 → 简短提醒；5/8 次 → 详细提醒（列出工具/次数/参数预览，默认 500 字符上限）
- 提醒内容：要求模型停止重复、重读上次结果、换方案或结束——**决策权完全留给模型**（合理重复不阻塞）

**tool-call-timeout-policy（工具超时）**：
- 单个 `tools/execute` 环绕监听器，零配置
- 读取 `ToolDefinition.timeoutMs`（各工具插件自声明，如 `dsh-tool-web` 的 `fetchTimeoutMs`），截止先到 → 结构化 `TOOL_TIMEOUT`
- 超时预算归属权在**工具定义方**（声明处），执行器只管强制执行

**面试点**：防死循环靠"建议性提醒"不是硬阻断；超时预算由工具声明、由框架强制执行——职责分离的样板。

---

### 12.8 token 计量与输出溢出（`dsh-token-meter` + `dsh-spill-local`）

**token-meter（回放感知计量）**：
- 固定启发式：**每 token ≈ 4 字符** + 角色/块/envelope 结构开销
- 从持久日志为每会话推进隔离 fold——compaction 与其他压力插件**共享同一计量**，不重复估算
- `measure()` 一次同步返回深度不可变快照（O(surface)）；`estimateMessage()` 单条计价
- 用提供方真实 usage 锚定：最新成功请求的 envelope 匹配时才复用提供方用量

**spill-local（超大输出溢出到文件）**：
- 工具输出过大时落盘：`<root>/session-<sha256前缀>/<随机>-<safeName>`
- 写入用 `open(path, 'wx', 0600)` 排他 + 仅属主——防符号链接预置
- 模型看到的是**本地路径 + 取回指引**（`read`/`grep` 该文件），而不是被截断

**面试点**：token 预算由全框架共享计量（不是各插件各算各的）；超大工具输出"溢出不截断"——这对你的日志查询工具很重要（超大结果该走 spill 而不是截断）。

---

### 12.9 检查点与持久性（`dsh-session-checkpoint-policy`）

**定位**：事件溯源会话的**语义持久性屏障**。零配置函数插件。

**在三个时机强制检查点**（`session/flush` 即时屏障，等写入完全停稳）：
1. **模型适配器收到请求前**——上一响应已持久，可安全重试
2. **顶层工具正文可能产生外部副作用前**——工具结果有序落盘
3. **每个 `agent/pre-step` 边界**——下一请求前确保历史完整

持久化（jsonl 后端）本身是**有界后台批次**（200ms 窗口），不带此策略时崩溃可能丢批窗口内事件；此策略把关键边界变成即时屏障。

**面试点**："先持久化再行动"——崩溃恢复的语义保证来自检查点策略，不是持久化后端本身。

---

### 12.10 遥测（`dsh-session-telemetry-otel`）——观察 agent 行为

**定位**：OpenTelemetry 后端，把会话事件映射到 `logger.emit()`，经 OTLP/HTTP 导出。

**模式**：
| mode | 行为 |
|---|---|
| `FULL` | 每条投影记录立即送 OTel SDK（含运维记录） |
| `FEEDBACK_ONLY` | 每次 `feedback/record` 回放该点后的权威日志（默认 `DISABLED`，需显式开） |

**身份**：`service.name/version`（来自 dsh-llm 的 `APP_IDENTITY`）+ 匿名 `user.id`（`$DSH_HOME/.anonymous-user-id`，随机 UUID）。

**面试点**：agent 可观测性 = 事件溯源日志 + OTel 导出。这是你将来"监控这个智能体本身"的接入点。

---

### 12.11 UI 与 RPC 层（`dsh-api-gateway` + Typert + `dsh-web`）

**Typert RPC（前端↔后端的通信协议）**：
- Host 侧 `ctx.typertGateway.invoke()`：解析描述符 → **校验具名参数** → 解析对象/Context → 调用 `@Remote` 标记的业务方法 → **校验结果**
- Client 侧 `ctx.remote.$mount()`：为 fiber 安装具体方法，`rpc.call('/api', endpoint, ...)` 发送，返回前再校验
- 取消：Remote 方法可声明末位 `signal: AbortSignal`（descriptor 元数据，不是 wire 参数）
- `/api` FetchHandler 上有 **trusted-host interceptor**（你之前遇到的 settings loopback 限制就来自这层）

**`dsh-web`（联网能力 seam）**：`ctx.web` 注册搜索/抓取提供方（Exa、Perplexity、HTTP fetch）；`dsh-tool-web` 才是模型的 `web_search`/`web_fetch` 工具——**提供方注册能力，工具插件注册名称/描述/schema**（又一个职责分离）。

**面试点**：前后端是**强类型 RPC**（不是自由 HTTP）；你之前遇到的"settings 保存失败"就是 trusted-host fence 在这层的判定。

---

### 12.12 设置 / 凭据 / KV 存储（`settings-file` + `credentials-local` + `storage`）

**settings-file**：
- 一个 YAML/JSON 文档承载全部 namespace；外部编辑**热发布**（watch + debounce 100ms）
- 每次写入是**读-改-写**：先重读文档再渲染，绝不覆盖未观察到的磁盘变更
- 跨进程写锁（`<file>.lock`，`wx` 创建，指数退避 2s 期限）；原子 rename 提交（0600 临时文件）
- **YAML 叶子级 diff**：只写变化的值，保留注释/锚点

**credentials-local（四层来源，明确优先级）**：
| 层 | 来源 | 可写 | 优先级 |
|---|---|---|---|
| env | 继承的进程环境 | 否 | 始终优先 |
| file | `$DSH_HOME/.credentials.yaml` | 是 | 高于 .env |
| project-env | `<cwd>/.env` | 否 | 高于用户 .env |
| user-env | `$DSH_HOME/.env` | 否 | 兜底 |

**storage（非会话 KV 存储）**：`ctx.storage` 注册具名后端（json/sqlite）+ 数据形式分面（当前 `kv`）；中心不执行 IO、不注册工具、不写会话事件。

**面试点**：设置 = 热加载 + 原子写 + 注释保留；凭据 = 明确优先级链（env 永远优先且只读）；KV 存储是给你做"长期记忆后端"的现成底座。

---

### 12.13 用户交互能力（`user-questions` + `commands` + `attachment-local` + `jobs-local`）

**user-questions（问用户）**：`ctx.userQuestions.ask({questions:[...]})` 暂停等人类决定；`AskUserQuestionIntent` 支持 `plan-review` 带标签意图（UI 渲染成"批准计划"而不是通用问题）。

**commands（斜杠命令注册表）**：`ctx.commands.register({name, description, ...})`；`/plan`、`/plan off` 这类命令；每次执行记 `command/run` + `command/done` 事件对；**结果绝不进模型历史**（除非命令生产方显式安排）。

**attachment-local（图片附件）**：
- 存 `<DSH_HOME>/attachments/v1/objects/<sha256前缀>/<sha256>`
- **规范化管线**：EXIF 方向应用 → 删元数据/色彩配置 → 8-bit sRGB → 长边 ≤2048px → ≤4MiB；按低色数分类选 PNG/WebP/JPEG
- 每条消息 ≤20 图、源图总量 ≤200MiB
- 会话日志只存引用+元数据，**绝不包含宿主路径**

**jobs-local（后台任务）**：`ctx.jobs` 内存注册表，`<kind>-N` id；`maxConcurrentJobsPerOwner` 默认 10；达到容量 `start()` 直接失败（错误告诉模型用 `job_kill`）；任务属于 owner 而非工具 fiber（重载不杀任务）。

**面试点**：问用户/命令/附件/后台任务 = 交互层四个可复用积木。你以后要给专家加"上传截图附件"或"后台跑长任务"，直接挂这些。

---

### 12.14 Agent 接口与注册表（`dsh-agent`）——一切 agent 的公共底座

**定位**：Agent 接口、注册表、作用域、`agent/*` 事件词汇。**它不依赖 loop**——所以 loop 可以替换（插件化的关键）。

**关键 API**：
- `ctx.agents.register(agent)`：注册已构造完成的 agent
- **`Agent.ctx`**：agent 作用域上下文（`dsh-scope`，键 = 该 agent）——通过它注册只对该 agent 生效的工具/段/变量/监听器，dispose 时全部撤销
- `assembleContextFor(agent)`：按 agent 的组装上下文
- `installAgentLlmTarget(agentCtx, target)`：快照提供方/模型/推理强度选择 → 应用到路由 + 提示词变量

**面试点**：`Agent` 是"活着的、带作用域的执行单元"；工具/提示/监听器都可以挂在它的作用域上（只对该 agent 可见）——这正是子代理 toolFilter、专家 persona 的底层机制。

---

## 十三、机制全貌图：一次对话里所有插件怎么协作

把第十二节放进总览（对照第〇节的图）：

```
用户输入
  │
  ▼
[12.13] user-questions / commands / attachment（入口交互）
  │
  ▼
[12.1] system-prompt 组装（段 + 变量 + 工具 schema + [12.2] skill 加载）
  │
  ▼
① Agent Loop（[12.14] Agent 作用域上下文）
  │   ├─ [2.1] 会话历史重放（[12.9] checkpoint 保证持久）
  │   ├─ [10.1] AGENTS.md 注入
  │   ├─ [12.8] token-meter 压力计量（喂 compaction）
  │   └─ [12.4] goal / plan-mode（长任务自动推进）
  │
  ▼
② 调模型（[12.10] telemetry 观测）
  │   ├─ [10.2] agent/request-error → [10.3] llm-retry 退避
  │   ├─ [2.2] compaction 溢出压缩
  │   └─ [12.11] 强类型 RPC 给 UI 传事件
  │
  ▼
③ 模型返回工具调用
  │
  ▼
④ 工具流水线
  │   ├─ [5.1/12.5] 审批 seam（ask → user-approval）
  │   ├─ [12.6] fs-sandbox / bash-sandbox 双隔离
  │   ├─ [12.7] timeout-policy（TOOL_TIMEOUT）
  │   ├─ [12.7] repeat-tool-reminder（防死循环）
  │   ├─ [12.8] spill（大输出落盘）
  │   └─ [12.3] workflow / ralph（模型编排子代理）
  │
  ▼
⑤ 子代理（[四] subagent）→ 结果回流
  │
  ▼
⑥ 结果写会话日志（[12.9] checkpoint 屏障 + [12.10] 遥测）
```

**一句话**：dsh 的几十个插件不是零散功能，是**同一棵装配树按请求生命周期分工**——入口（交互）→ 组装（提示词）→ 循环（记忆/计量/目标）→ 模型（重试/压缩）→ 工具（审批/沙箱/超时/防循环）→ 子代理 → 持久化（检查点/遥测）。你自研的位置始终是：**领域工具 + 长期记忆 + 角色预设**，其余全是这棵树里的现成机制。

---

## 八（补）、面试自检扩充

在第八节 7 个问题之外，再自检这 10 个：

8. 系统提示词怎么组装？（有序段 + 变量插值 + 工具 schema，`{{model}}`/`{{cwd}}`；complete 段可完全接管）
9. 技能和 AGENTS.md 的区别？（技能=按需加载的 SOP，带 model/user 四象限调用策略；AGENTS.md=每次都注入的全局指令）
10. 长任务怎么自动推进？（goal 状态机 + goal-round-driver 自动轮次 + 人类让行 + 256 轮上限）
11. 审批一次授权有效多久？（allowed-once，只对本次操作；审计 asked/decided 成对留痕）
12. 沙箱两层各管什么？（fs=进程内路径围栏；shell=内核级文件影响隔离；只限文件，网络不限）
13. 工具死循环怎么防？（repeat-tool-reminder 链键+阈值提醒，建议性不阻断）
14. 超大工具输出怎么处理？（spill 落盘给路径，不截断；token-meter 全框架共享计量）
15. 崩溃恢复怎么保证？（session-checkpoint-policy 在请求前/工具副作用前/pre-step 三处强制 flush 屏障）
16. 前端怎么和后端通信？（Typert 强类型 RPC，参数/结果双向校验，可取消）
17. 凭据优先级？（env > .credentials.yaml > 项目 .env > 用户 .env；env 只读且永远优先）

