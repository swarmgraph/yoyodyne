package runstate

import (
	"os"
	"strings"
)

// currentBoot identifies this boot of the machine, and is empty where the
// operating system will not say.
func currentBoot() string {
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(boot))
}
