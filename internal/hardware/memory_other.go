//go:build !windows && !linux

package hardware

func detectMemoryMB() (uint64, uint64) { return 0, 0 }
