//go:build darwin

package supervise

import (
	"encoding/binary"
	"fmt"
	"syscall"
	"unsafe"
)

// The process table on macOS, read the way ps(1) reads it but without ps,
// which a sandboxed process may not run: the kern.proc.all sysctl, which fills
// one kinfo_proc per process. Three fields are read, at offsets fixed by the
// 64-bit layout runstate's process lookup reads the same structure at: the
// process id in struct extern_proc, and the parent and group in the struct
// eproc that follows it.
const (
	kinfoProcSize   = 648
	kinfoPID        = 40
	kinfoParent     = 560
	kinfoGroup      = 564
	sysctlKern      = 1
	sysctlKernProc  = 14
	sysctlProcAll   = 0
	sysctlMIBLength = 3
)

// childrenOf lists the processes whose parent is pid.
func childrenOf(pid int) ([]process, error) {
	mib := [sysctlMIBLength]int32{sysctlKern, sysctlKernProc, sysctlProcAll}
	for range 3 {
		var size uintptr
		if _, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), sysctlMIBLength, 0, uintptr(unsafe.Pointer(&size)), 0, 0); errno != 0 {
			return nil, fmt.Errorf("read the size of the process table: %w", errno)
		}
		// Room for the processes started between the two calls.
		size += size / 8
		buffer := make([]byte, size)
		_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL, uintptr(unsafe.Pointer(&mib[0])), sysctlMIBLength,
			uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
		if errno == syscall.ENOMEM {
			continue
		}
		if errno != 0 {
			return nil, fmt.Errorf("read the process table: %w", errno)
		}
		var children []process
		for offset := uintptr(0); offset+kinfoProcSize <= size; offset += kinfoProcSize {
			record := buffer[offset : offset+kinfoProcSize]
			row := process{
				pid:    int(int32(binary.LittleEndian.Uint32(record[kinfoPID:]))),
				parent: int(int32(binary.LittleEndian.Uint32(record[kinfoParent:]))),
				group:  int(int32(binary.LittleEndian.Uint32(record[kinfoGroup:]))),
			}
			if row.parent == pid && row.pid != pid && row.pid > 0 {
				children = append(children, row)
			}
		}
		return children, nil
	}
	return nil, fmt.Errorf("read the process table: it kept growing faster than it could be read")
}
