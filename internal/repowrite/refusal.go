package repowrite

import (
	"errors"
	"fmt"
	"io/fs"
	"sync/atomic"
	"testing"
)

// Reason is why a confined write was refused. The list is closed and is the
// one the repository-confined-writes design gives, so a refusal names the same
// cause whichever writer met it.
type Reason string

const (
	ReasonLinkLeavesRoot  Reason = "a symbolic link leads outside the root"
	ReasonAbsoluteLink    Reason = "a symbolic link is given as an absolute path"
	ReasonFinalLink       Reason = "the last part of the target is a symbolic link"
	ReasonOtherFilesystem Reason = "the path crosses onto another filesystem"
	ReasonMoved           Reason = "a held directory is no longer beneath the root"
	ReasonLinked          Reason = "the file has more than one link"
	ReasonChanged         Reason = "the target changed before it was removed or replaced"
	ReasonPlatform        Reason = "this platform is not supported"
	ReasonFilesystem      Reason = "this filesystem is not supported"
)

// RefusalError is a confined write that was not made, or was made and then
// undone. It names the operation, the root, the path relative to the root, and
// one reason from the closed list above.
type RefusalError struct {
	Operation string
	Root      string
	Path      string
	Reason    Reason
	// Detail is what was found, where there is more to say than the reason:
	// the link's text, the filesystem's name, the directory that moved.
	Detail string
}

func (e *RefusalError) Error() string {
	message := fmt.Sprintf("refused to %s %s under %s: %s", e.Operation, e.Path, e.Root, e.Reason)
	if e.Detail != "" {
		message += " (" + e.Detail + ")"
	}
	return message
}

// refusal is a refusal whose operation, root and path the caller fills in.
func refusal(reason Reason, detail string) error {
	return &RefusalError{Reason: reason, Detail: detail}
}

func isRefusal(err error) bool {
	var refused *RefusalError
	return errors.As(err, &refused)
}

// pathError names the root-relative path an operating-system error was met at.
func pathError(op, name string, err error) error {
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// movedFrom is the refusal for a held directory that a lookup from the root
// no longer finds, carrying what the lookup met instead. A refusal for the
// platform or the filesystem is passed on as it is, because it is not about
// the directory at all.
func movedFrom(rel string, err error) error {
	var refused *RefusalError
	if errors.As(err, &refused) {
		if refused.Reason == ReasonPlatform || refused.Reason == ReasonFilesystem {
			return err
		}
		return refusal(ReasonMoved, fmt.Sprintf("%s cannot be found from the root any more: %s, %s", rel, refused.Reason, refused.Detail))
	}
	return refusal(ReasonMoved, fmt.Sprintf("%s cannot be found from the root any more: %v", rel, err))
}

// Point is a moment between the steps of a confined change at which a test can
// change the filesystem, so each race in the design's table happens at exactly
// the same place every time it runs.
type Point string

const (
	// PointBeforePublish is after a new file's content is written and synced
	// and before its link count is checked for publication.
	PointBeforePublish Point = "before publish"
	// PointBeforeChange is after the check that comes before a change and
	// before the change itself.
	PointBeforeChange Point = "before change"
	// PointAfterChange is after a change and before the check that follows it.
	PointAfterChange Point = "after change"
	// PointBetweenBatches is after one append batch and its checks, before the
	// next batch is checked.
	PointBetweenBatches Point = "between batches"
	// PointBeforeRemove is after a removal's target is checked and before it
	// is moved aside to be removed.
	PointBeforeRemove Point = "before remove"
)

// HookEvent says where a confined change is when a test hook runs.
type HookEvent struct {
	Point Point
	// Directory is the absolute path of the held directory as it was when it
	// was looked up, which is where a test moves it from.
	Directory string
	// Name is the entry in Directory the change is about, if there is one.
	Name string
}

var raceHook atomic.Pointer[func(HookEvent)]

// SetTestHook runs hook at every Point of every confined change until the
// returned function is called. It is for tests alone and panics in any binary
// that is not a test, so no production path can change what a write does. A
// test that sets it must not run in parallel with another confined write.
func SetTestHook(hook func(HookEvent)) (restore func()) {
	if !testing.Testing() {
		panic("repowrite.SetTestHook is only for tests")
	}
	previous := raceHook.Swap(&hook)
	return func() { raceHook.Store(previous) }
}

func fire(point Point, directory, name string) {
	if hook := raceHook.Load(); hook != nil && *hook != nil {
		(*hook)(HookEvent{Point: point, Directory: directory, Name: name})
	}
}
