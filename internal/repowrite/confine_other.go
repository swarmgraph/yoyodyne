//go:build !(darwin || linux)

package repowrite

import (
	"io/fs"
	"os"
	"runtime"
)

// The design names Linux and macOS as the platforms a confined write is
// supported on, and on any other every write is refused with the platform
// named: there is no fallback to an ordinary rename or write, because neither
// keeps the guarantee. Nothing below openAnchor is ever reached here.

type anchor struct {
	path string
	id   fileID
}

type heldDir struct {
	anchor *anchor
	rel    string
	id     fileID
}

type temporary struct{ file *os.File }

type appendWriter struct {
	dir    *heldDir
	file   *os.File
	name   string
	anchor *anchor
}

func openAnchor(path string) (*anchor, error) {
	return nil, &RefusalError{Root: path, Path: ".", Reason: ReasonPlatform, Detail: runtime.GOOS}
}

const unreachable = "repowrite: a confined write was reached on an unsupported platform"

func (a *anchor) duplicate() (*anchor, error)                           { panic(unreachable) }
func (a *anchor) close() error                                          { return nil }
func (a *anchor) hold(string) (*heldDir, error)                         { panic(unreachable) }
func (a *anchor) makeDirectories(string, fs.FileMode) (*heldDir, error) { panic(unreachable) }
func (h *heldDir) close() error                                         { return nil }
func (h *heldDir) makeDirectory(string, fs.FileMode, bool) (*heldDir, error) {
	panic(unreachable)
}
func (h *heldDir) createTemporary(fs.FileMode) (*temporary, error) { panic(unreachable) }
func (h *heldDir) openExisting(string, int, fs.FileMode, bool) (*os.File, error) {
	panic(unreachable)
}
func (h *heldDir) removeEntry(string, entryKind) error   { panic(unreachable) }
func (h *heldDir) shorten(string, int64) error           { panic(unreachable) }
func (h *heldDir) checkMovable(string, bool) error       { panic(unreachable) }
func (h *heldDir) symlink(string, string) error          { panic(unreachable) }
func (h *heldDir) entryExists(string) (bool, error)      { panic(unreachable) }
func (t *temporary) discard() error                      { panic(unreachable) }
func (t *temporary) publish(string, bool, *fileID) error { panic(unreachable) }
func (w *appendWriter) Write([]byte) (int, error)        { panic(unreachable) }
func (w *appendWriter) Sync() error                      { panic(unreachable) }
func (w *appendWriter) Close() error                     { panic(unreachable) }
func moveEntry(*heldDir, string, *heldDir, string) error { panic(unreachable) }
func syncDirectory(*heldDir) error                       { panic(unreachable) }
func sameID(fs.FileInfo, fileID) bool                    { return false }
