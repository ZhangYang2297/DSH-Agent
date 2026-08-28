# 配置文件说明（CONFIG.md）

本仓库运行所需的配置文件与环境变量清单。逐文件说明「每个配置的作用」以及「clone 后哪些地方必须/按需修改」。

标注图例：

- `【必须改】`：clone 后不改成自己的值就跑不起来
- `【按需改】`：默认能跑，但多数人会按自己环境调整
- `【一般不动】`：框架约定值，除非你知道在做什么否则别动

---

## 0. 一句话跑起来（改什么）

```bash
# 1) 必须：换成你自己的火山方舟 API Key（见 §6）
export ARK_API_KEY="ark-xxxxxxxx"

# 2) 启动（脚本会自动设置 OPS_MCP_EXE，见 §3）
bash start_dsh.sh
# 打开 http://127.0.0.1:3080
```

真正「必须改」的只有 **`ARK_API_KEY` 这一个环境变量**。其余配置都有合理默认值，能直接跑；模型 ID、专家人设、跨机部署地址属于「按需改」。

---

## 1. `cordis.patch.yml` —— dsh 主配置（核心，必须了解）

dsh 用 Cordis 的「patch 分层配置」。本文件是**覆盖层**：在默认 profile 之上追加/覆盖配置。

- `- id: xxx`：定位已存在的插件实例并**覆盖**它的 `config`（未提及的字段保留）
- `- insert: `：向插件栈**新增**一个实例（本项目用它注册专家子代理和 MCP 客户端）

### 1.1 模型 provider：火山方舟 Ark  【一般不动 / 按账户改模型】

```yaml
- id: llm-pi-ai
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      ark:
        displayName: 火山方舟 Ark
        apiKeyEnv: ARK_API_KEY      # 【必须改关注点】这里只是"环境变量名"，Key 本身在 §6 用 export 设置
        api: openai-responses       # 【一般不动】走 OpenAI Responses 协议兼容层
        baseURL: https://ark.cn-beijing.volces.com/api/v3   # 【一般不动】Ark 接入点
        models:
          - id: doubao-seed-2-0-lite-260428   # 【按需改】换成你 Ark 账户里能用的模型 ID
            name: Doubao Seed 2.0 Lite
            contextWindow: 131072
            maxTokens: 8192
          - id: glm-5-2-260617               # 【按需改】备选模型，同上
            name: GLM-5.2
            contextWindow: 131072
            maxTokens: 8192
```

> 说明：`apiKeyEnv` 指定的是「从哪个环境变量读 Key」，不要在这里写明文 Key。模型 `id` 必须是你 Ark 控制台里真实开通的模型 ID，否则会报 402/404。

### 1.2 默认模型：主管默认用哪个  【按需改】

```yaml
- id: agent-default-model
  name: '@deepseek-ai/dsh-agent-default-model'
  config:
    provider: ark
    model: doubao-seed-2-0-lite-260428   # 【按需改】主管（supervisor）默认调用的模型，须与 1.1 中的某个 id 对应
```

### 1.3 运维诊断专家：subagent 实例  【按需改】

```yaml
- insert:
    - id: tool-ops-expert
      name: '@deepseek-ai/dsh-tool-subagent'
      config:
        provider: spawn
        toolName: delegate_diagnosis   # 主管调专家时使用的工具名（AGENTS.md 里引用的就是它）
        persona: |-
          ...（专家人设全文）...
```

> 作用：注册一个「运维诊断专家」子代理，对复杂排障做根因分析。
> 需要修改：把 `persona` 改成你想要的专家定位（职责边界、安全红线、输出格式）。`toolName` 一般不动，动了要同步改 AGENTS.md。

### 1.4 运维监控 MCP 客户端  【一般不动 / 跨机部署才改】

```yaml
- insert:
    - id: mcp-ops
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: ops
        transport: stdio
        command: !!js process.env.OPS_MCP_EXE   # 从环境变量读 Go 采集器路径（见 §6、§3）
        args: []
        reconnect:
          enabled: true
          initialDelayMs: 500
          maxDelayMs: 30000
          maxAttempts: 10
```

> 作用：把 Go 写的 `mcp-ops` 监控工具注册成 `mcp__ops__*` 普通函数工具，主管和专家都能直接调。
> `!!js process.env.OPS_MCP_EXE` 是 Cordis 的表达式写法，运行时读取环境变量 `OPS_MCP_EXE` 得到可执行文件路径——这就是「避免硬编码绝对路径、clone 后直接能跑」的关键。
> 跨机部署（监控跑在别的机器）时，需要把 `transport` 改成 `streamable-http` 并改 `url`（见 §5）。

---

## 2. `AGENTS.md` —— 主管角色与意图路由  【按需改】

纯文本人设文件，定义「对话主管」怎么理解需求、什么时候自己查、什么时候派专家。

- 第 12–28 行：主管能直接调的 `mcp__ops__*` 工具清单与「单点查询自己答」的路由规则
- 第 30–43 行：什么情况才调 `delegate_diagnosis`（复杂排障/根因/建议）
- 第 51–62 行：派专家时的「上下文自包含 + 证据来源检查 + 危险操作红线」纪律

> 需要修改：按你想要的运维风格改路由策略、工具描述、汇报格式。`delegate_diagnosis` 这个工具名要和 `cordis.patch.yml` 1.3 的 `toolName` 保持一致。

---

## 3. `start_dsh.sh` —— 一键启动脚本  【一般不动】

做的事：

1. 把 `OPS_MCP_EXE` 设成 `./tools/mcp-ops/mcp-ops.exe` 的绝对路径（供 cordis.patch.yml 的 `!!js process.env.OPS_MCP_EXE` 读取）
2. 检查 `ARK_API_KEY` 是否设置，没设就警告
3. `exec dsh web --patch ./cordis.patch.yml --no-open --port 3080` 启动 web

> 用法：`bash start_dsh.sh [--port 3080]`。
> 若不用这个脚本，必须自己 `export OPS_MCP_EXE=.../mcp-ops.exe`，否则 MCP 客户端启动失败、工具不可用。

---

## 4. `tools/prometheus/prometheus.yml` —— Prometheus 抓取配置  【按需改 / 跨机部署必改】

```yaml
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: 'mcp-ops'
    static_configs:
      - targets: ['127.0.0.1:9100']   # 【跨机部署必改】改成 exporter 实际地址，如 ['10.0.0.5:9100']
        labels:
          instance: 'web-01:9100'     # 【按需改】节点名，query_resource_usage 的 host 参数要匹配这个前缀
```

> 作用：Prometheus 每 15s 抓一次 `mcp-ops` 自研 exporter（`:9100/metrics`）的指标，`query_resource_usage` 再经 PromQL 查这里。
> `instance` 标签是 `host` 参数的匹配键：你查 `host=web-01` 时，必须这里标签是 `web-01` 开头才能匹配到。

---

## 5. `tools/mcp-ops/` —— Go 监控采集器（配置点都在这几处）

源码级配置点（改完需 `cd tools/mcp-ops && go build -o mcp-ops.exe .` 重新编译）：

| 文件 | 配置点 | 作用 / 何时改 |
|---|---|---|
| `main.go` | `:9100` exporter 监听地址 | `--exporter` 模式下 Prometheus 抓的端口；跨机时改成 `0.0.0.0:9100` 暴露 |
| `main.go` | `--http` 模式 | 把 MCP 从 stdio 改成 streamable-http，供远端 dsh 连；对应 §1.4 的 `transport: streamable-http` + `url` |
| `promql.go` | PromQL 查询语句 | 想加/改指标视图时调整 |
| `collect_windows.go` / `network_windows.go` | 纯 Windows API 采集 | 默认采集本机资源/进程/网络；非 Windows 目标需另写采集实现 |

> 三种运行形态：
> - **本机 stdio（默认）**：`start_dsh.sh` 直接拉起，`command: !!js process.env.OPS_MCP_EXE`，无需 Prometheus 也能跑简单查询（进程/日志走 Go 直采）。
> - **本机 + Prometheus**：先 `bash tools/prometheus/start_prometheus.sh`（需先 `download_prometheus.sh` 下载二进制），再 `start_dsh.sh`，`query_resource_usage` 走真实指标。
> - **跨机 http**：监控机跑 `mcp-ops.exe --http`，dsh 侧把 §1.4 改成 `transport: streamable-http` + `url: http://监控机IP:PORT/mcp`，并改 §4 的 `targets` 指向监控机 exporter。

---

## 6. 环境变量清单

| 变量 | 必须？ | 作用 | 在哪用 |
|---|---|---|---|
| `ARK_API_KEY` | **必须** | 火山方舟 API Key | cordis.patch.yml `apiKeyEnv`，模型请求鉴权 |
| `OPS_MCP_EXE` | 必须（用脚本则自动） | `mcp-ops.exe` 绝对路径 | cordis.patch.yml `!!js process.env.OPS_MCP_EXE` |
| `DSH_HOME` | 可选 | dsh 会话/配置根目录 | 不设为默认 `~/.dsh`；多项目隔离可设成项目内 `.dsh_home` |

设置示例：

```bash
export ARK_API_KEY="ark-你的key"
# OPS_MCP_EXE 由 start_dsh.sh 自动设；手动跑则：
export OPS_MCP_EXE="$(pwd)/tools/mcp-ops/mcp-ops.exe"
```

---

## 7. `.gitignore` 说明  【一般不动】

已忽略：`*.exe`（二进制不入库，clone 后需 `go build` 或走 `download_*` 脚本）、Prometheus 二进制与数据目录、`.env`、`~/.dsh` 会话数据、以及本仓库的**内部开发文档**（`DSH-*`、`PLAN.md`、`工具方案-*`、`开发进度*`）。

> 也就是说 clone 后：源码 + 配置 + README/CONFIG 都在；`mcp-ops.exe` 需本地 `go build` 生成（或自行编译），`start_dsh.sh` 会检查并提示。

---

## 8. 修改优先级速查

| 场景 | 你至少要改 |
|---|---|
| 本机快速跑通 | 只设 `ARK_API_KEY` → `bash start_dsh.sh` |
| 换模型 | §1.1 的 `models[].id` + §1.2 的 `model` |
| 调专家风格 | §1.3 的 `persona` + §2 `AGENTS.md` |
| 看真实指标 | §4 起 Prometheus（先 `download_prometheus.sh` 再 `start_prometheus.sh`） |
| 跨机监控 | §5 的 `--http` + §1.4 改 http + §4 改 `targets` |
