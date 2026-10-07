//go:build !(darwin || linux)

package runstate

// currentBoot is empty where this build has no way to identify a boot, so an
// execution's record says the boot is unknown rather than guessing one.
func currentBoot() string { return "" }
