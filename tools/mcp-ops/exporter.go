// exporter.go —— 可选 Prometheus exporter 模式 + streamable-http MCP 模式
//
// mcp-ops 支持三种运行模式：
//   1. 默认：MCP server（stdio），由 dsh-mcp-client 拉起（同机部署）
//   2. --exporter [addr]：作为 Prometheus 指标端点（/metrics），默认 127.0.0.1:9100
//   3. --http [addr]：作为 streamable-http MCP server（跨机部署），默认 0.0.0.0:8000
//
// 同机部署：dsh 用 stdio 拉起；跨机部署：dsh 用 streamable-http 连远程。
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// runExporter 启动 /metrics 端点。addr 为空时用 127.0.0.1:9100。
func runExporter(addr string) error {
	if addr == "" {
		addr = "127.0.0.1:9100"
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.Write([]byte(collectText()))
	})
	// 健康检查
	mux.HandleFunc("/-/healthy", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok\n"))
	})

	log.Printf("mcp-ops exporter listening on http://%s/metrics", addr)
	return http.ListenAndServe(addr, mux)
}

// 判断是否以 exporter 模式启动
func isExporterMode() (string, bool) {
	for _, a := range os.Args[1:] {
		if a == "--exporter" {
			addr := "127.0.0.1:9100"
			for i, x := range os.Args[1:] {
				if x == "--exporter" && i+1 < len(os.Args)-1 {
					next := os.Args[i+2]
					if !strings.HasPrefix(next, "--") {
						addr = next
					}
				}
			}
			return addr, true
		}
	}
	return "", false
}

// 判断是否以 --http 模式启动（streamable-http MCP server，跨机部署用）
func isHTTPMode() (string, bool) {
	for _, a := range os.Args[1:] {
		if a == "--http" {
			addr := "0.0.0.0:8000" // 默认监听所有网卡，供远程 dsh 连接
			for i, x := range os.Args[1:] {
				if x == "--http" && i+1 < len(os.Args)-1 {
					next := os.Args[i+2]
					if !strings.HasPrefix(next, "--") {
						addr = next
					}
				}
			}
			return addr, true
		}
	}
	return "", false
}

// runHTTPServer 启动 streamable-http MCP server。
// dsh-mcp-client 用 transport=streamable-http + url 连接这里。
func runHTTPServer(addr string) error {
	server := newOpsServer()
	handler := mcp.NewStreamableHTTPHandler(func(req *http.Request) *mcp.Server {
		return server
	}, nil)
	log.Printf("mcp-ops MCP server (streamable-http) listening on http://%s/mcp", addr)
	return http.ListenAndServe(addr, handler)
}

// selfQuery 直接用本机采集（exporter 模式可用，便于 MCP 也走真实数据）
func selfResourceMetrics() (map[string]any, error) {
	s, err := collectResources()
	if err != nil {
		return nil, err
	}
	m := map[string]any{
		"cpu_percent":      round2(s.CPUPct),
		"mem_total_bytes":  s.MemTotal,
		"mem_avail_bytes":  s.MemAvail,
		"mem_usage_percent": round2(100.0 * float64(s.MemTotal-s.MemAvail) / float64(s.MemTotal)),
		"disk_total_bytes": s.DiskTotal,
		"disk_avail_bytes": s.DiskAvail,
		"disk_usage_percent": round2(100.0 * float64(s.DiskTotal-s.DiskAvail) / float64(s.DiskTotal)),
		"process_count":    s.ProcCount,
		"collected_at":     time.Now().Format(time.RFC3339),
	}
	return m, nil
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

var _ = fmt.Sprintf // 保留 fmt 导入
