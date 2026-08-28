// collect_windows.go —— Windows 资源采集（syscall，无第三方依赖）
//go:build windows

package main

import (
	"strings"
	"syscall"
	"unsafe"
)

// ---- FILETIME ----
type winFILETIME struct {
	LowDateTime  uint32
	HighDateTime uint32
}

func winToUint64(ft winFILETIME) uint64 {
	return uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime)
}

var (
	modKernel32  = syscall.NewLazyDLL("kernel32.dll")
	modPsapi     = syscall.NewLazyDLL("psapi.dll")
	modNtdll     = syscall.NewLazyDLL("ntdll.dll")
	procGetSystemTimes           = modKernel32.NewProc("GetSystemTimes")
	procGlobalMemoryStatusEx     = modKernel32.NewProc("GlobalMemoryStatusEx")
	procGetDiskFreeSpaceExW      = modKernel32.NewProc("GetDiskFreeSpaceExW")
	procGetLogicalDriveStringsW  = modKernel32.NewProc("GetLogicalDriveStringsW")
	procEnumProcesses            = modPsapi.NewProc("EnumProcesses")
)

func winGetSystemTimes(idle, kern, user *winFILETIME) error {
	r, _, err := procGetSystemTimes.Call(
		uintptr(unsafe.Pointer(idle)),
		uintptr(unsafe.Pointer(kern)),
		uintptr(unsafe.Pointer(user)),
	)
	if r == 0 {
		return err
	}
	return nil
}

// ---- MEMORYSTATUSEX ----
type winMemoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func winGlobalMemoryStatusEx() (winMemoryStatusEx, error) {
	var ms winMemoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r, _, err := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 {
		return ms, err
	}
	return ms, nil
}

// ---- 磁盘：取第一个非空盘符 ----
func winDiskFreeSpace() (total, avail uint64, ok bool) {
	buf := make([]uint16, 256)
	r, _, _ := procGetLogicalDriveStringsW.Call(
		uintptr(uint32(len(buf))),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if r == 0 {
		return 0, 0, false
	}
	// 找到第一个 "C:\" 形式的盘符
	var root []uint16
	for i := 0; i < int(r); {
		if buf[i] == 0 {
			break
		}
		start := i
		for i < int(r) && buf[i] != 0 {
			i++
		}
		root = buf[start:i]
		break
	}
	if len(root) == 0 {
		return 0, 0, false
	}
	rootStr := syscall.UTF16ToString(root)
	var free, totalBytes, totalFree uint64
	r2, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(rootStr))),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&totalBytes)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r2 == 0 {
		return 0, 0, false
	}
	return totalBytes, free, true
}

// ---- 进程数：EnumProcesses ----
func winListProcesses() []uint32 {
	pids := make([]uint32, 1024)
	var needed uint32
	r, _, _ := procEnumProcesses.Call(
		uintptr(unsafe.Pointer(&pids[0])),
		uintptr(len(pids))*4,
		uintptr(unsafe.Pointer(&needed)),
	)
	if r == 0 {
		return nil
	}
	count := int(needed / 4)
	return pids[:count]
}

// ---- 进程详情：CreateToolhelp32Snapshot（纯 syscall，不依赖 tasklist 命令） ----
// 返回 name/pid/路径，含按名称模糊匹配。

type winProcessEntry32 struct {
	Size                uint32
	Usage               uint32
	ProcessID           uint32
	DefaultHeapID       uintptr
	ModuleID            uint32
	Threads             uint32
	ParentProcessID     uint32
	PriClassBase        int32
	Flags               uint32
	ExeFile             [260]uint16
}

var (
	procCreateToolhelp32Snapshot = modKernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW          = modKernel32.NewProc("Process32FirstW")
	procProcess32NextW           = modKernel32.NewProc("Process32NextW")
	procCloseHandle              = modKernel32.NewProc("CloseHandle")
)

const (
	winTH32CS_SNAPPROCESS = 0x2
)

// winProcessByName 用快照 API 枚举全部进程，按 ExeFile 模糊匹配 name，返回进程列表。
func winProcessByName(name string) []map[string]any {
	snap, _, _ := procCreateToolhelp32Snapshot.Call(winTH32CS_SNAPPROCESS, 0)
	if snap == 0 || snap == uintptr(^uintptr(0)) {
		return nil
	}
	defer procCloseHandle.Call(snap)

	var entry winProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	procs := []map[string]any{}
	r, _, _ := procProcess32FirstW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	lower := strings.ToLower(name)
	for r != 0 {
		exe := strings.ToLower(strings.TrimRight(utf16ToString(entry.ExeFile[:]), "\x00"))
		if lower == "" || strings.Contains(exe, lower) {
			procs = append(procs, map[string]any{
				"name": utf16ToString(entry.ExeFile[:]),
				"pid":  entry.ProcessID,
				"ppid": entry.ParentProcessID,
				"threads": entry.Threads,
			})
		}
		r, _, _ = procProcess32NextW.Call(snap, uintptr(unsafe.Pointer(&entry)))
	}
	return procs
}

// utf16ToString 转换 UTF-16 字节切片为 string（到第一个 \x00 为止）。
func utf16ToString(s []uint16) string {
	out := make([]uint16, 0, len(s))
	for _, v := range s {
		if v == 0 {
			break
		}
		out = append(out, v)
	}
	return syscall.UTF16ToString(out)
}
