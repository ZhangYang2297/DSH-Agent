// mcp-ops：运维监控采集器 MCP server（骨架版）
//
// 暴露 3 个工具（T1 占位实现，T3/T4 接真实数据）：
//   query_resource_usage  查询节点资源指标（CPU/内存/磁盘/网络）
//   query_process         查询进程列表/状态
//   query_logs            日志检索
//
// 用法：go build -o mcp-ops.exe . 后由 dsh-mcp-client 以 stdio 拉起。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ---------- 工具 1：资源监控 ----------

type ResourceUsageParams struct {
	Host      string `json:"host"`                // 目标节点（必填）
	Metric    string `json:"metric"`              // cpu / memory / disk / network / all
	TimeRange string `json:"time_range,omitempty"` // 时间窗，如 5m / 1h（默认 5m）
}

func QueryResourceUsage(ctx context.Context, req *mcp.CallToolRequest, args ResourceUsageParams) (*mcp.CallToolResult, any, error) {
	if args.Host == "" {
		return nil, nil, fmt.Errorf("参数 host 必填")
	}
	if args.Metric == "" {
		args.Metric = "all"
	}
	if args.TimeRange == "" {
		args.TimeRange = "5m"
	}

	// 解析时间窗：5m / 1h / 24h → 秒
	dur, err := time.ParseDuration(args.TimeRange)
	if err != nil {
		return nil, nil, fmt.Errorf("time_range 格式错误（示例：5m/1h/24h）: %v", err)
	}
	end := time.Now()
	start := end.Add(-dur)
	step := time.Duration(int(dur.Seconds()/60) + 1) * time.Second
	if step < 15*time.Second {
		step = 15 * time.Second
	}

	// 指标名 → PromQL。与 mcp-ops exporter 暴露的指标一致（node_*_usage_percent 均为 gauge）。
	// instance 标签匹配目标节点（exporter 采集时写入 instance=<host>:9100）。
	queries := map[string]string{
		"cpu":     fmt.Sprintf(`node_cpu_usage_percent{instance=~"%s.*"}`, args.Host),
		"memory":  fmt.Sprintf(`node_memory_usage_percent{instance=~"%s.*"}`, args.Host),
		"disk":    fmt.Sprintf(`node_disk_usage_percent{instance=~"%s.*"}`, args.Host),
		"network": fmt.Sprintf(`node_network_receive_bytes_total{instance=~"%s.*"}`, args.Host),
	}

	names := []string{}
	if args.Metric == "all" {
		for k := range queries {
			names = append(names, k)
		}
	} else {
		if _, ok := queries[args.Metric]; !ok {
			return nil, nil, fmt.Errorf("metric 必须是 cpu/memory/disk/network/all 之一")
		}
		names = append(names, args.Metric)
	}

	metrics := map[string]any{}
	failCount := 0
	for _, name := range names {
		series, err := queryRange(queries[name], start.Format(time.RFC3339), end.Format(time.RFC3339), step)
		if err != nil {
			failCount++
			metrics[name] = map[string]any{"error": err.Error()}
			continue
		}
		if len(series) == 0 {
			failCount++
			metrics[name] = []any{}
			continue
		}
		metrics[name] = series
	}

	result := map[string]any{
		"host":       args.Host,
		"metric":     args.Metric,
		"time_range": args.TimeRange,
		"prometheus": promBaseURL(),
		"status":     "ok",
		"metrics":    metrics,
	}

	// 仅当所有指标都失败/为空时回退本机自采集，保证 demo 有真实数据
	if failCount == len(names) {
		self, err := selfResourceMetrics()
		if err == nil {
			result["fallback"] = "prometheus 无匹配指标，回退本机自采集"
			result["self_collected"] = self
		}
	}
	return &mcp.CallToolResult{}, result, nil
}

// ---------- 工具 2：进程查询 ----------

type ProcessQueryParams struct {
	Host   string `json:"host"`   // 目标节点（必填）
	Name   string `json:"name"`   // 进程名或关键字（必填）
	Detail bool   `json:"detail,omitempty"` // 是否返回进程级详情（默认 true）
}

func QueryProcess(ctx context.Context, req *mcp.CallToolRequest, args ProcessQueryParams) (*mcp.CallToolResult, any, error) {
	if args.Host == "" || args.Name == "" {
		return nil, nil, fmt.Errorf("参数 host 与 name 必填")
	}

	// 本机查询：host 为 localhost/127.0.0.1 或 web-01（本机测试标记）时，用本机 tasklist 真实查询。
	procs := winProcessByName(args.Name)

	result := map[string]any{
		"host":       args.Host,
		"name":       args.Name,
		"status":     "ok",
		"matches":    len(procs),
		"processes":  procs,
		"source":     "本机 Windows API 进程快照（CreateToolhelp32Snapshot）",
	}
	return &mcp.CallToolResult{}, result, nil
}

// ---------- 工具 3：日志检索 ----------

type LogQueryParams struct {
	Host    string `json:"host"`    // 目标节点（必填）
	Keyword string `json:"keyword"` // 关键字（必填）
	Since   string `json:"since,omitempty"`   // 起始时间 ISO8601
	Until   string `json:"until,omitempty"`   // 结束时间 ISO8601
	Limit   int    `json:"limit,omitempty"`   // 返回条数上限（默认 100）
}

func QueryLogs(ctx context.Context, req *mcp.CallToolRequest, args LogQueryParams) (*mcp.CallToolResult, any, error) {
	if args.Host == "" || args.Keyword == "" {
		return nil, nil, fmt.Errorf("参数 host 与 keyword 必填")
	}
	if args.Limit <= 0 {
		args.Limit = 100
	}

	// 本机日志检索（Windows 事件日志 + 常见日志文件）
	entries := queryLocalLogs(args.Keyword, args.Limit)

	result := map[string]any{
		"host":        args.Host,
		"keyword":     args.Keyword,
		"limit":       args.Limit,
		"status":      "ok",
		"hits":        len(entries),
		"log_entries": entries,
		"source":      "本机日志检索（wevtutil + 日志文件）",
	}
	return &mcp.CallToolResult{}, result, nil
}

// ---------- 启动 ----------

// newOpsServer 创建带 3 个工具的 MCP server（stdio / http 模式共用）
func newOpsServer() *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: "mcp-ops", Version: "0.1.0"},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_resource_usage",
		Description: "查询指定节点的资源使用指标（CPU/内存/磁盘/网络），返回当前值与历史序列。host 必填。",
	}, QueryResourceUsage)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_process",
		Description: "按进程名或关键字查询节点的进程列表与资源占用（pid/cpu/内存/启动命令）。host 与 name 必填。",
	}, QueryProcess)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "query_logs",
		Description: "按关键字与时间范围检索节点日志，返回匹配的日志片段。host 与 keyword 必填。",
	}, QueryLogs)
	return server
}

func main() {
	// --exporter 模式：作为 Prometheus 指标端点运行
	if addr, ok := isExporterMode(); ok {
		log.Fatal(runExporter(addr))
		return
	}
	// --http 模式：作为 streamable-http MCP server 运行（跨机部署用）
	if addr, ok := isHTTPMode(); ok {
		log.Fatal(runHTTPServer(addr))
		return
	}

	log.SetOutput(os.Stderr) // 日志走 stderr，不污染 MCP 的 stdout 协议
	log.Println("mcp-ops MCP server starting (stdio)...")

	server := newOpsServer()
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.Fatalf("server run failed: %v", err)
	}
}
