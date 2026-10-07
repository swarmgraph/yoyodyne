package runstate

import "syscall"

// currentBoot identifies this boot of the machine, and is empty where the
// operating system will not say — a sandbox may refuse the question.
func currentBoot() string {
	boot, err := syscall.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return boot
}
