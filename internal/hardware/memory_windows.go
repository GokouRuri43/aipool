package hardware

import (
	"syscall"
	"unsafe"
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhysical        uint64
	AvailablePhysical    uint64
	TotalPageFile        uint64
	AvailablePageFile    uint64
	TotalVirtual         uint64
	AvailableVirtual     uint64
	AvailableExtendedVir uint64
}

func detectMemoryMB() (uint64, uint64) {
	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	globalMemoryStatusEx := kernel32.NewProc("GlobalMemoryStatusEx")
	result, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0, 0
	}
	return status.TotalPhysical >> 20, status.AvailablePhysical >> 20
}
