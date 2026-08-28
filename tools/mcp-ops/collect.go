// collect.go —— 本机资源指标采集（Windows / Linux 通用），暴露 Prometheus 文本格式
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// resourceSnapshot 一次采集的本机资源快照
type resourceSnapshot struct {
	CPUPct     float64
	MemTotal   uint64 // bytes
	MemAvail   uint64 // bytes
	DiskTotal  uint64 // bytes
	DiskAvail  uint64 // bytes
	ProcCount  int
	NetInBytes uint64 // 累计接收字节（counter）
	NetOutBytes uint64 // 累计发送字节（counter）
}

// collectResources 采集本机资源（跨平台：Windows 用 GetSystemTimes/GlobalMemoryStatusEx，Linux 用 /proc）
func collectResources() (resourceSnapshot, error) {
	switch runtime.GOOS {
	case "windows":
		return collectWindows()
	default:
		return collectLinux()
	}
}

// ---------- Windows 实现 ----------

func collectWindows() (resourceSnapshot, error) {
	var s resourceSnapshot

	// CPU：GetSystemTimes
	var idle, kern, user winFILETIME
	if err := winGetSystemTimes(&idle, &kern, &user); err != nil {
		return s, fmt.Errorf("GetSystemTimes: %w", err)
	}
	prevIdle := winToUint64(idle)
	prevKern := winToUint64(kern)
	prevUser := winToUint64(user)
	time.Sleep(200 * time.Millisecond)
	_ = winGetSystemTimes(&idle, &kern, &user)
	curIdle := winToUint64(idle)
	curKern := winToUint64(kern)
	curUser := winToUint64(user)

	idleDelta := curIdle - prevIdle
	kernDelta := curKern - prevKern
	userDelta := curUser - prevUser
	totalDelta := kernDelta + userDelta
	if totalDelta > 0 {
		s.CPUPct = 100.0 * float64(totalDelta-idleDelta) / float64(totalDelta)
	}

	// 内存：GlobalMemoryStatusEx
	ms, err := winGlobalMemoryStatusEx()
	if err == nil {
		s.MemTotal = ms.TotalPhys
		s.MemAvail = ms.AvailPhys
	}

	// 磁盘：取系统盘（或第一个有数据的盘）
	if total, avail, ok := winDiskFreeSpace(); ok {
		s.DiskTotal = total
		s.DiskAvail = avail
	}

	s.ProcCount = len(winListProcesses())

	// 网络字节计数（counter 指标）
	if inB, outB, err := winNetBytes(); err == nil {
		s.NetInBytes = inB
		s.NetOutBytes = outB
	}
	return s, nil
}

// ---------- Linux 实现（骨架） ----------

func collectLinux() (resourceSnapshot, error) {
	var s resourceSnapshot

	// CPU：/proc/stat
	if data, err := os.ReadFile("/proc/stat"); err == nil {
		var idle, total uint64
		lines := strings.Split(string(data), "\n")
		if len(lines) > 0 {
			fields := strings.Fields(lines[0])
			// cpu user nice system idle iowait irq softirq steal
			if len(fields) >= 8 {
				for i := 1; i < len(fields); i++ {
					var v uint64
					fmt.Sscanf(fields[i], "%d", &v)
					total += v
					if i == 4 { // idle
						idle = v
					}
				}
				// 简化：单次采样（真实实现应两次采样算 delta）
				_ = idle
				_ = total
			}
		}
	}
	return s, nil
}

// collectText 输出 Prometheus 文本格式指标
func collectText() string {
	s, err := collectResources()
	if err != nil {
		return fmt.Sprintf("# error: %v\n", err)
	}
	var b strings.Builder
	b.WriteString("# HELP node_cpu_usage_percent CPU 使用率（%）\n")
	b.WriteString("# TYPE node_cpu_usage_percent gauge\n")
	fmt.Fprintf(&b, "node_cpu_usage_percent %.2f\n", s.CPUPct)

	b.WriteString("# HELP node_memory_total_bytes 内存总量\n")
	b.WriteString("# TYPE node_memory_total_bytes gauge\n")
	fmt.Fprintf(&b, "node_memory_total_bytes %d\n", s.MemTotal)

	b.WriteString("# HELP node_memory_avail_bytes 可用内存\n")
	b.WriteString("# TYPE node_memory_avail_bytes gauge\n")
	fmt.Fprintf(&b, "node_memory_avail_bytes %d\n", s.MemAvail)

	b.WriteString("# HELP node_memory_usage_percent 内存使用率（%）\n")
	b.WriteString("# TYPE node_memory_usage_percent gauge\n")
	if s.MemTotal > 0 {
		fmt.Fprintf(&b, "node_memory_usage_percent %.2f\n", 100.0*float64(s.MemTotal-s.MemAvail)/float64(s.MemTotal))
	}

	b.WriteString("# HELP node_disk_total_bytes 磁盘总量\n")
	b.WriteString("# TYPE node_disk_total_bytes gauge\n")
	fmt.Fprintf(&b, "node_disk_total_bytes %d\n", s.DiskTotal)

	b.WriteString("# HELP node_disk_avail_bytes 磁盘可用\n")
	b.WriteString("# TYPE node_disk_avail_bytes gauge\n")
	fmt.Fprintf(&b, "node_disk_avail_bytes %d\n", s.DiskAvail)

	b.WriteString("# HELP node_disk_usage_percent 磁盘使用率（%）\n")
	b.WriteString("# TYPE node_disk_usage_percent gauge\n")
	if s.DiskTotal > 0 {
		fmt.Fprintf(&b, "node_disk_usage_percent %.2f\n", 100.0*float64(s.DiskTotal-s.DiskAvail)/float64(s.DiskTotal))
	}

	b.WriteString("# HELP node_process_count 进程数\n")
	b.WriteString("# TYPE node_process_count gauge\n")
	fmt.Fprintf(&b, "node_process_count %d\n", s.ProcCount)

	b.WriteString("# HELP node_network_receive_bytes_total 网络累计接收字节\n")
	b.WriteString("# TYPE node_network_receive_bytes_total counter\n")
	fmt.Fprintf(&b, "node_network_receive_bytes_total %d\n", s.NetInBytes)

	b.WriteString("# HELP node_network_transmit_bytes_total 网络累计发送字节\n")
	b.WriteString("# TYPE node_network_transmit_bytes_total counter\n")
	fmt.Fprintf(&b, "node_network_transmit_bytes_total %d\n", s.NetOutBytes)

	return b.String()
}
