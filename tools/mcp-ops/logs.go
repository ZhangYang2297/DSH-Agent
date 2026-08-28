// logs.go —— 日志检索（本机实现）
//
// 策略：
//  1. 优先读 Windows 事件日志（Application/System，用 wevtutil）
//  2. 再扫常见日志目录（%TEMP%、C:/Windows/Logs 等）里最近的文本日志
// 均为只读操作，不修改任何文件。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

const timeHour = time.Hour

func timeNow() time.Time { return time.Now() }

var errStop = filepath.SkipAll

// queryLocalLogs 在本机检索日志（纯文件读取，不调用系统命令）。
// 扫描来源：
//   1. Windows 应用/系统日志目录（C:/Windows/Logs、C:/ProgramData 等常见日志位置）
//   2. %TEMP% 下最近修改的 .log/.txt
//   3. 当前工作目录附近的日志文件（本项目运行产生的）
// keyword 必填；maxLines 限制返回条数。
func queryLocalLogs(keyword string, maxLines int) []map[string]any {
	entries := []map[string]any{}
	seen := map[string]bool{}

	roots := []string{os.TempDir()}
	if windir := os.Getenv("WINDIR"); windir != "" {
		roots = append(roots, filepath.Join(windir, "Logs"))
	}
	if progdata := os.Getenv("ProgramData"); progdata != "" {
		roots = append(roots, filepath.Join(progdata, "Microsoft", "Windows", "Diagnosis"))
	}
	// 当前项目目录（mcp-ops 所在目录的上级），方便开发期检索日志
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, filepath.Dir(filepath.Dir(wd)))
	}

	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".log" && ext != ".txt" {
				return nil
			}
			// 只读最近 24 小时内修改的文件，避免扫全盘
			if timeNow().Sub(info.ModTime()) > 24*timeHour {
				return nil
			}
			// 文件 > 10MB 跳过，避免读大文件
			if info.Size() > 10*1024*1024 {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			// 只匹配最后 200 行（新日志在文件尾）
			if len(lines) > 200 {
				lines = lines[len(lines)-200:]
			}
			for _, ln := range lines {
				if strings.Contains(strings.ToLower(ln), strings.ToLower(keyword)) {
					key := path + ln
					if seen[key] {
						continue
					}
					seen[key] = true
					entries = append(entries, map[string]any{
						"source":  path,
						"message": strings.TrimSpace(ln),
					})
					if len(entries) >= maxLines {
						return errStop
					}
				}
			}
			return nil
		})
		if len(entries) >= maxLines {
			break
		}
	}

	return entries
}
