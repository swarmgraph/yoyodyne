// Command hookoutsidetests tries to set a confined-write test hook from a
// program that is not a test binary. hooks_test.go builds and runs it, and
// expects it to panic before the hook is set.
package main

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

func main() {
	repowrite.SetTestHook(func(repowrite.HookEvent) {})
	fmt.Println("the hook was set")
}
