// network_windows.go —— Windows 网络接口字节计数（GetIfTable，固定结构，安全）
//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	modIphlpapi   = syscall.NewLazyDLL("iphlpapi.dll")
	procGetIfTable = modIphlpapi.NewProc("GetIfTable")
)

// MIB_IFROW 官方固定结构（dwInOctets/dwOutOctets 在固定偏移，安全解析）
type mibIfRow struct {
	WszName        [256]uint16 // MAX_INTERFACE_NAME_LEN
	DwIndex        uint32
	DwType         uint32
	DwMtu          uint32
	DwSpeed        uint32
	DwPhysAddrLen  uint32
	BPhysAddr      [8]byte
	DwAdminStatus  uint32
	DwOperStatus   uint32
	DwLastChange   uint32
	DwInOctets     uint32
	DwInUcastPkts  uint32
	DwInNUcastPkts uint32
	DwInDiscards   uint32
	DwInErrors     uint32
	DwInUnknownPro uint32
	DwOutOctets    uint32
	DwOutUcastPkts uint32
	DwOutNUcastPkt uint32
	DwOutDiscards  uint32
	DwOutErrors    uint32
	DwOutQLen      uint32
	DwDescrLen     uint32
	BDescr         [256]byte
}

const (
	maxIfTableSize = 128 // 最多 128 个接口
)

// winNetBytes 返回 (总接收字节, 总发送字节, 错误)
func winNetBytes() (uint64, uint64, error) {
	tableSize := uint32(unsafe.Sizeof(mibIfRow{})*maxIfTableSize + 4)
	buf := make([]byte, tableSize)
	size := uint32(len(buf))

	r, _, err := procGetIfTable.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
		0, // bOrder=FALSE
	)
	if r != 0 {
		return 0, 0, err
	}

	numEntries := *(*uint32)(unsafe.Pointer(&buf[0]))
	var inTotal, outTotal uint64
	rowSize := unsafe.Sizeof(mibIfRow{})
	for i := 0; i < int(numEntries); i++ {
		row := (*mibIfRow)(unsafe.Pointer(&buf[4+int(i)*int(rowSize)]))
		inTotal += uint64(row.DwInOctets)
		outTotal += uint64(row.DwOutOctets)
	}
	return inTotal, outTotal, nil
}
