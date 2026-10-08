//go:build darwin

package runstate

import (
	"syscall"
	"time"
	"unsafe"
)

// What macOS's process table says of one process, read the way ps(1) reads it:
// the kern.proc.pid sysctl, which fills a kinfo_proc. Only two fields are read,
// at offsets fixed by the 64-bit layout of struct extern_proc — the start time,
// a timeval that opens the structure, and the run state, a byte after the
// pointers and the flags.
const (
	kinfoProcSize      = 648
	kinfoStartSeconds  = 0
	kinfoStartMicros   = 8
	kinfoState         = 36
	processStateExited = 5 // SZOMB
	sysctlKern         = 1
	sysctlKernProc     = 14
	sysctlKernProcPID  = 1
	sysctlMIBLength    = 4
)

func describeProcess(pid int) (exited bool, started time.Time, known bool) {
	mib := [sysctlMIBLength]int32{sysctlKern, sysctlKernProc, sysctlKernProcPID, int32(pid)}
	buffer := make([]byte, kinfoProcSize)
	size := uintptr(len(buffer))
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), sysctlMIBLength,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&size)), 0, 0)
	if errno != 0 || size < kinfoState+1 {
		return false, time.Time{}, false
	}
	seconds := *(*int64)(unsafe.Pointer(&buffer[kinfoStartSeconds]))
	micros := *(*int32)(unsafe.Pointer(&buffer[kinfoStartMicros]))
	if seconds > 0 {
		started = time.Unix(seconds, int64(micros)*int64(time.Microsecond))
	}
	return buffer[kinfoState] == processStateExited, started, true
}
