package main

import (
	"context"
	"os"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/cli"
	"github.com/mason-bryant/yoyodyne/internal/shutdown"
)

// version is what the linker stamps into a release build and what "make build"
// fills with a git description. It stays a plain constant initializer because
// that is what "go build -ldflags -X" can overwrite. A binary from
// "go install" has no stamp, so buildinfo resolves the rest.
var version = "dev"

// A stop signal cancels the command and, where the command does not stop on the
// cancellation, ends the process anyway. See the shutdown package for why the
// cancellation on its own turned out not to be enough.
func main() {
	ctx, stop := shutdown.Answering(context.Background(), os.Stderr)
	cli.KeepTrackerCalls(os.Args[1:])
	code := cli.RunContext(ctx, os.Args[1:], os.Stdout, os.Stderr, buildinfo.Resolve(version))
	stop()
	os.Exit(code)
}
