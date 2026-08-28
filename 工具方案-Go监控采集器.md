# 运维专家领域工具方案 —— Go 监控采集器 + Prometheus + MCP

> 日期：2026-08-25
> 状态：方案确认可行，待落地
> 关联：`PLAN.md` M3（运维诊断专家）工具集细化

---

## 1. 背景：为什么要设计领域工具

当前主管→运维专家的链路已经打通，但专家用的还是 dsh 的**通用工具**（fs/pwsh/skill）。真实运维场景需要的是**领域专用工具**：

| 场景 | 现在（通用工具） | 需要（领域工具） |
|---|---|---|
| 查服务器 CPU/内存 | 专家自己拼 `pwsh` 命令碰运气 | `query_resource_usage(host, metric)` 直接返回结构化数据 |
| 查进程 | 专家猜命令 | `query_process(host, name)` 精确返回进程列表 |
| 查日志 | 专家翻文件 | `query_logs(host, keyword, time_range)` 按时间/关键字检索 |

**核心问题**：通用工具靠模型"临场发挥"，结果不稳定；领域工具是**确定性能力封装**，schema 强约束、输出结构化、可审计。

---

## 2. 技术选型：为什么是 Go + Prometheus + MCP

### 2.1 架构形态

```
┌──────────────────────────────────────────────────────┐
│ dsh（DeepSeek Harness）                               │
│  主管 agent（glm/doubao）                              │
│    └─ subagent 工具：delegate_diagnosis               │
│         └─ 运维诊断专家（子代理）                       │
│              └─ 工具集（toolFilter 限定）              │
│                   ├─ fs / pwsh（只读）                │
│                   └─ mcp__ops__*（MCP 工具）⬅ 新增    │
└──────────────┬───────────────────────────────────────┘
               │ dsh-mcp-client 插件（stdio）
               ▼
┌──────────────────────────────────────────────────────┐
│ Go 监控采集器（MCP server，本项目自研）                │
│   ├─ query_resource_usage：查询节点资源指标            │
│   ├─ query_process：查询进程列表/状态                  │
│   └─ query_logs：日志检索                             │
│        │                                              │
│        ▼                                              │
│   Prometheus（采集存储）                               │
│     ├─ node_exporter（节点指标：CPU/内存/磁盘/网络）    │
│     ├─ 进程 exporter（进程指标，可自研 Go exporter）    │
│     └─ Loki / 自研日志采集器（日志，可选）              │
└──────────────────────────────────────────────────────┘
```

### 2.2 为什么可行（关键事实，已从源码确认）

| 疑问 | 答案 |
|---|---|
| dsh 支持外部 MCP server 吗？ | ✅ **`dsh-mcp-client` 插件原生支持**，stdio / streamable-http 两种传输 |
| 会有 Codex 的 namespace 问题吗？ | ✅ **不会**。dsh 把 MCP 工具注册为 `mcp__<server>__<tool>` 名称的**普通 function 工具**，走 native Function Calling，Ark 兼容 |
| MCP 工具能限定给专家吗？ | ⚠️ 注册是全局的，但可用 subagent `toolFilter` 控制主管/专家各自可见性 |
| Go 写 MCP server 成熟吗？ | ✅ 官方 `modelcontextprotocol/go-sdk`（stdio + streamable-http 都支持）或社区 `mark3labs/mcp-go` |
| **跨机部署（dsh 在 A、监控目标在 B）可行吗？** | ✅ **可行**。采集器部署在 B 用 `--http` 起 streamable-http MCP server，dsh 在 A 用 `transport: streamable-http` 连远程 url |

### 2.3 双模式运行（同机 / 跨机）

`mcp-ops.exe` 支持三种启动方式，覆盖同机和跨机：

| 模式 | 命令 | 用途 | 部署位置 |
|---|---|---|---|
| **stdio（默认）** | `mcp-ops.exe` | dsh 同机拉起，进程内管道 | 与 dsh 同机 |
| **streamable-http** | `mcp-ops.exe --http [addr]` | 跨机 MCP server，dsh 远程连接 | **被监控服务器 B** |
| **exporter** | `mcp-ops.exe --exporter [addr]` | Prometheus 抓取 /metrics | **被监控服务器 B**（与 --http 可并存） |

**同机部署**（当前 demo 形态）：
```
dsh（A 机）──stdio──▶ mcp-ops.exe ──PromQL──▶ Prometheus ──scrape──▶ mcp-ops.exe(:9100) ──▶ 本机指标
```

**跨机部署**（你问的场景，已实测 HTTP 模式可用）：
```
┌─ A 机：运维助手 ──────────────────┐
│ dsh（主管+专家）                    │
│   └─ dsh-mcp-client                │
│        transport: streamable-http  │
│        url: http://B:8000/mcp      │
└──────────────┬────────────────────┘
               │ MCP over HTTP（跨网络）
               ▼
┌─ B 机：被监控服务器 ───────────────┐
│ mcp-ops.exe --http 0.0.0.0:8000    │  ← streamable-http MCP server
│   ├─ query_resource_usage ──PromQL─▶ Prometheus(B 机或独立监控机)
│   │                                  └─scrape─▶ mcp-ops --exporter(B)
│   ├─ query_process（B 机进程）       │
│   └─ query_logs（B 机日志）          │
└────────────────────────────────────┘
```

**跨机接入 dsh 的配置**（`cordis.patch.yml`，与 stdio 版本的唯一差别是 transport/url）：
```yaml
- insert:
    - id: mcp-ops
      name: '@deepseek-ai/dsh-mcp-client'
      config:
        serverName: ops
        transport: streamable-http
        url: http://B_HOST:8000/mcp
        headers:
          Authorization: !!js '`Bearer ${process.env.OPS_MCP_TOKEN}`'  # 可选鉴权
        reconnect:
          enabled: true
          initialDelayMs: 500
          maxDelayMs: 30000
          maxAttempts: 10
```

**跨机注意事项**：
- `--http` 默认监听 `0.0.0.0:8000`（所有网卡），生产环境务必加鉴权（`headers` 或防火墙）
- MCP 端点路径为 `/mcp`（go-sdk StreamableHTTPHandler 默认路由）
- 日志/进程查询默认查**采集器所在机**（B 机）；若要多目标服务器，B 机作为"监控网关"内部路由（二期可加 target 参数）
- exporter 与 --http 是两个独立端口，可同时运行（B 机跑两个实例或后续合一个进程）



### 2.3 为什么值得用 Go

- **单二进制部署**：跨平台无依赖，适合监控采集器（放被监控机器上）
- **并发天然优势**：Prometheus 抓取 + 日志采集的并发模型 Go 最合适
- **MCP SDK 生态成熟**：官方 Go SDK 支持 stdio 和 streamable-http
- **面试价值高**：展示"工程能力"——用 Go 写一个真实的 MCP 工具服务

---

## 3. 三个工具的设计

### 3.1 `query_resource_usage`（资源监控）

**用途**：查询节点 CPU/内存/磁盘/网络指标。

```yaml
参数:
  host: string        # 目标节点（必填）
  metric: string      # cpu / memory / disk / network / all（默认 all）
  time_range: string  # 最近时间窗，如 5m / 1h / 24h（默认 5m）
  step: string        # 采样步长，如 15s / 1m（默认自动）
返回（结构化）:
  host, timestamp, metric 名, 当前值, 历史序列（缩点后）, 阈值告警（可选）
```

**实现**：Go 服务收到调用 → 查 Prometheus HTTP API（`/api/v1/query_range`）→ PromQL 计算 → 返回结构化 JSON。

### 3.2 `query_process`（进程查询）

**用途**：按名称/关键词/主机查进程列表与资源占用。

```yaml
参数:
  host: string       # 目标节点（必填）
  name: string       # 进程名或关键字（必填）
  detail: boolean    # 是否返回进程级详情（pid/启动命令/CPU/内存，默认 true）
返回:
  host, 匹配进程数, 进程列表[{pid, name, user, cpu_pct, mem_pct, cmd, started_at}]
```

**实现**：Go 服务通过 Prometheus 进程指标（自研 exporter 暴露），或直连被监控机（SSH/agent 模式，二期）。

### 3.3 `query_logs`（日志检索）

**用途**：按时间范围/关键字检索节点日志。

```yaml
参数:
  host: string        # 目标节点（必填）
  keyword: string     # 关键字（必填）
  time_from / time_to # 时间范围（默认最近 1h）
  limit: number       # 返回条数上限（默认 100）
  grep: string        # 可选，进一步过滤
返回:
  host, 命中总数, 日志片段[{timestamp, level, source, message}]
```

**实现**：一期 Go 直接读本地日志文件（`/var/log`、Windows Event Log）+ grep；二期接 Loki 或 ELK。

---

## 4. 实现步骤（里程碑）

### T1：环境与骨架
- [ ] 安装 Go（当前未安装，需装 1.22+）
- [ ] 用 `modelcontextprotocol/go-sdk` 建最小 MCP server 骨架（stdio，暴露 3 个占位工具）
- [ ] 独立验证：Python mcp 客户端 / dsh 之外手动 connect，确认工具列表正确

### T2：dsh 接入
- [x] `cordis.patch.yml` 加 `dsh-mcp-client` 实例（stdio + command=go 编译出的 exe）
- [x] 确认主管工具列表出现 `mcp__ops__query_resource_usage` 等
- [x] 测试主管直接调用 MCP 工具成功（真实 Ark）
- [x] **HTTP 模式验证**：`mcp-ops --http` 起 streamable-http server，Python 客户端远程连接成功调用（跨机能力就绪）

### T3：Prometheus 采集
- [ ] 装 Prometheus + node_exporter（Windows 本机即可）
- [ ] Go 服务实现 `query_resource_usage`：调 Prometheus API 返回真实指标
- [ ] 端到端：主管问"CPU 多少" → 专家调 MCP 工具 → 返回真实数据

### T4：进程与日志
- [ ] `query_process`：Prometheus 进程指标 or 本机采集
- [ ] `query_logs`：本机日志读取 + 检索
- [ ] 三个工具全部真实可用

### T5：专家挂载与收尾
- [ ] 运维专家 toolFilter 限定：只暴露 `mcp__ops__*` + 只读 fs/pwsh
- [ ] 主管 toolFilter 排除 `mcp__ops__*`（主管不直接调，路由给专家）
- [ ] AGENTS.md / persona 更新（数据来源纪律已就位）
- [ ] 更新 PLAN.md、写测试

---

## 5. 风险与决策点

| 风险/决策 | 说明 | 应对 |
|---|---|---|
| Go 未安装 | 当前机器无 Go | 装 Go 1.22+（winget 或官网安装包）——✅ 已装 1.26.7 |
| Prometheus 生态在 Windows | node_exporter 支持 Windows，但部分指标受限 | **改用自研 exporter**（`mcp-ops --exporter`，纯 syscall）——✅ 已实现 |
| 沙箱拦截系统命令 | Go 进程内调 tasklist/wevtutil 被 WorkBuddy 安全策略拦截 | 改纯 Windows API（CreateToolhelp32Snapshot / 文件读取）——✅ 已解决 |
| MCP stdio 传输在 Windows 下 spawn exe | Go 编译的 exe 用 stdio 最稳（单文件） | 首选 stdio；**跨机用 streamable-http**（`--http`，已实测可用） |
| 工具全局注册 | `mcp__ops__*` 会全局可见 | 靠 subagent toolFilter 收敛到专家 |
| 日志采集方式 | 本地读 vs Loki | 一期本机读文件够 demo；二期接 Loki |
| **跨机部署** | dsh 在 A、监控目标在 B | 采集器部署 B 用 `--http` 起 MCP server，dsh 用 streamable-http 连（已实测）；B 机 HTTP 需鉴权 |

---

## 6. 面试价值定位

这套东西是面试能讲出深度的核心素材：

1. **Go + MCP**：写了一个真实的外部 MCP 工具服务，接入 agent 框架
2. **Prometheus 集成**：不是 toy——真的接采集、写 PromQL、拿真实指标
3. **工具设计**：领域工具 schema 强约束 vs 通用工具的差别，有实际判断依据
4. **dsh 插件生态**：会用 `dsh-mcp-client` 接外部能力，理解工具 namespace 机制
5. **职责边界**：主管路由、专家执行、工具落地——三层各司其职

> 一句话：这是项目里"第一个完全自己写的可运行代码"，从"配置框架"进入"开发能力"的转折点。
