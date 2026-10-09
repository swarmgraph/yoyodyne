package backend

import (
	"context"
	"strings"
)

// LaunchSettingsChecker is an adapter that can establish, before a developer's
// session is started, that the installed provider applies the safety
// settings the adapter launches a developer with: the sandbox that confines its
// shell, the guard in front of that shell, and whatever keeps the operator's
// personal configuration out.
//
// It exists for a provider that reads those settings from something it may
// silently decline to apply. Claude Code takes them in one settings payload,
// and its own help says settings that fail validation are ignored without a
// word in non-interactive mode, so a later CLI that rejects any part of the
// payload would start a developer with no sandbox and no guard and nothing
// failing. An adapter whose safety rests on flags the CLI refuses outright when
// it does not know them has nothing to establish and does not implement this.
//
// The directory is where the developer would be started, because what is in
// force there includes the repository's own checked-in settings. The check
// makes no provider call and spends nothing.
//
// An error is a check that could not be made at all — an executable that would
// not start, or did not answer in time — which says nothing about the settings
// either way. A check that was made and found something missing is not an
// error: it is a LaunchSettingsCheck naming what did not take.
type LaunchSettingsChecker interface {
	CheckLaunchSettings(ctx context.Context, directory string) (LaunchSettingsCheck, error)
}

// LaunchSettingsCheck is what one check found: which version of the provider
// was asked, and each setting or flag it did not apply, in words a
// person reads.
type LaunchSettingsCheck struct {
	// Version is the provider's own answer to being asked its version.
	Version string `json:"version"`
	// NotInForce is each setting or flag the adapter relies on that the
	// installed provider did not apply or no longer names, one plain phrase
	// each. Empty is a provider that applied everything.
	NotInForce []string `json:"not_in_force,omitempty"`
}

// InForce reports a check that found everything the adapter relies on applied.
func (c LaunchSettingsCheck) InForce() bool {
	return len(c.NotInForce) == 0
}

// Says is what did not take, in one line.
func (c LaunchSettingsCheck) Says() string {
	return strings.Join(c.NotInForce, "; ")
}
