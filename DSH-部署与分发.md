# DeepSeek Harness —— 部署与分发指南

> 回答：代码要不要放项目里？怎么打包给别人用？怎么从网页端变成软件/服务？

---

## 一、先分清"你的代码"和"依赖"

| 内容 | 放哪 | 说明 |
|---|---|---|
| **dsh CLI 本体** | 全局 npm 安装（不需要放项目） | 是运行时依赖，对方也要装 |
| **官方插件**（dsh-* 几十个） | `~/.dsh/profiles/node_modules/` | 由 dsh/plugin 工具管理 |
| **你的配置**（cordis.patch.yml） | 项目目录 ✅ | 这是"你的东西" |
| **你的指令**（AGENTS.md） | 项目目录 ✅ | 角色定义 |
| **你的自定义插件**（自己写的工具） | 项目目录（如 `plugins/`）✅ | 真正的"你的代码" |

**结论**：项目目录只需要放"你自己的东西"（配置 + 指令 + 自定义插件源码）。dsh CLI 和官方插件是依赖，通过 npm 安装，不用放项目里。

---

## 二、四种分发形态（从简单到专业）

### 形态 A：配置包分发（最简单，当前适用）
把"项目目录"（patch + AGENTS.md + 自定义插件）打个压缩包/传到 git。对方使用：
1. 安装 Node ≥ 22.19 + `npm i -g @deepseek-ai/dsh`
2. 解压你的项目 → `cd 项目`
3. `dsh web --patch ./cordis.patch.yml` 启动
4. 配模型 key → 开用

**适合**：技术用户、demo、面试演示。缺点：对方要装 Node 和 dsh。

### 形态 B：Bundle 插件包（官方推荐的分发格式）
把你的智能体打包成一个 **dsh bundle**（普通 npm 包 + 两个声明）：
```
my-ops-assistant/
  package.json        # 声明 "dsh": { "bundle": { "patch": "./cordis.patch.yml" } }
  cordis.patch.yml    # 你的插件树装配
  AGENTS.md
  plugins/            # 你的自定义插件
```
发布到 npm 后，别人一条命令装好：
```
dsh plugin --profile web add my-ops-assistant
```
这是官方插件生态的分发方式，可复用、可版本化、可更新。

### 形态 C：Python SDK 嵌入（最专业，面向服务化）
官方 **Python SDK**（`pip install deepseek-harness-sdk`）：
- **自带 Harness 运行时**——跑它的机器不需要装 Node.js（官方称支持 Linux x64 / arm64、macOS）
- 可以嵌入你的 FastAPI/后端服务，做成 API
- **注意**：当前 PyPI 上是占位包（0.0.0.dev0），真正 SDK 未稳定发布（依赖的 runtime-bin 包缺失），**等官方正式版**再走这条

### 形态 D：桌面应用（从网页端变软件）
dsh 的 web 模式本质是一个本地 HTTP 服务 + 浏览器前端。要变成"软件"：
- **Electron/Tauri 壳**：把 web 界面 + dsh 后端一起打进桌面应用
- 或 **tui profile**：做成终端应用
- 或 **web + 局域网访问**：`dsh web --host 0.0.0.0` 让局域网访问

---

## 三、决策建议（针对你的项目）

| 阶段 | 做法 |
|---|---|
| **现在**（demo/面试） | 形态 A——项目目录打包，`--patch` 启动。已满足 |
| **中期**（做成可交付产品） | 形态 B——做成 dsh bundle 发布。官方推荐，也能讲清"我把智能体封装成了插件包" |
| **长期**（服务化/商业化） | 形态 C——SDK 嵌入自己的后端；或形态 D 打包桌面软件 |

**关键认知**：dsh 的架构决定了"你的产品 = 插件组合 + 配置 + 提示词"。分发时你分发的是"**组合说明书**"（bundle/patch），而不是整个运行时——运行时是公共依赖。

---

## 四、附带收获：四种运行模式

dsh 通过不同插件组合能变成完全不同的产品：
- **Standard**：完整编码 Agent（默认，我们用这个）
- **PTC**：模型写代码编排工具调用（多步工具流）
- **Minimal**：只有 bash + 编辑两个工具（基准测试）
- **Creative**：在内存里试验插件组合（造 Harness 用）

同一份代码，换 profile 就换一种产品——这正是"一切皆插件"的威力，也是面试可以讲的点。
