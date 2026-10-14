package gitworktree

import (
	"os"
	"syscall"
)

// freeMemory reads the pages macOS holds no data in at all, vm.page_free_count,
// in bytes. It leaves out what the kernel would give up from its caches, so it
// is the smallest honest figure for what a new process can have at once. A
// sandbox may refuse the reading, and then nothing is read.
func freeMemory() (uint64, bool) {
	pages, err := syscall.SysctlUint32("vm.page_free_count")
	if err != nil {
		return 0, false
	}
	return uint64(pages) * uint64(os.Getpagesize()), true
}
