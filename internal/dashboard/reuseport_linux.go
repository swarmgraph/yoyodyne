//go:build linux && !mips && !mipsle && !mips64 && !mips64le

package dashboard

// soReusePort is SO_REUSEPORT on Linux. The syscall package does not name it
// on every architecture (amd64 among them), and its value is 15 on every
// architecture this file builds for; MIPS numbers it differently and shares
// no port (listen_other.go).
const soReusePort = 0xf
