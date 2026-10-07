package modelfailover

// Serving a turn on the alternate provider when the configured provider's
// executable could not be found or started.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"os/exec"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// missingExecutable is the error the shared executable lookup hands back for a
// provider CLI that is not on PATH, as an adapter wraps it.
func missingExecutable() error {
	return errors.Join(execution.ErrProcessNotStarted, &backend.ExecutableError{
		Binary: "claude", Provider: "claude-code", Path: "/usr/bin:/bin",
		Cause: exec.ErrNotFound,
	})
}

func TestAnExecutableThatCannotRunCrossesToTheAlternateProviderAndSaysWhy(t *testing.T) {
	t.Parallel()

	primary := &fakeProvider{errs: []error{missingExecutable()}}
	crossed := &fakeProvider{results: []backend.RunResult{{SessionID: "codex-session-1", FinalText: "decided"}}}
	windows := newTestWindows(t)
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) { return request, nil })
	policy.AlternateProvider = crossed
	policy.UnknownResetPause = 30 * time.Minute

	result, served, err := Serve(context.Background(), primary, backend.RunRequest{Model: "fable", SessionID: "claude-session-1"}, policy)
	if err != nil || result.FinalText != "decided" {
		t.Fatalf("Serve() = %+v, %v; want the alternate provider's answer", result, err)
	}
	if served.Why != runstate.SubstitutedForExecutable || served.Endpoint.Provider != "codex" || served.RefusedEndpoint.Provider != "claude-code" {
		t.Fatalf("served = %+v, want the turn moved to codex for want of an executable", served)
	}
	if len(crossed.requests) != 1 || crossed.requests[0].SessionID != "" {
		t.Fatalf("alternate requests = %#v, want one with no session", crossed.requests)
	}
	entries, err := windows.List()
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v, %v; want the substitution written down", entries, err)
	}
	entry := entries[0]
	if entry.Reason() != runstate.SubstitutedForExecutable || entry.Provider != "claude-code" || entry.ServedByProvider != "codex" || entry.ServedBy != "gpt-5-codex" || entry.Model != "fable" {
		t.Fatalf("entry = %+v, want which provider served and why", entry)
	}
	if !strings.Contains(entry.Waiting, "not found on PATH") || !strings.Contains(entry.Describe(), "could not be found or started") {
		t.Fatalf("entry waiting = %q, describe = %q; want the executable's own account", entry.Waiting, entry.Describe())
	}

	// The next turn inside the interval goes straight to the alternate, so the
	// operator is told once rather than once per turn.
	crossed.results = append(crossed.results, backend.RunResult{FinalText: "again"})
	_, served, err = Serve(context.Background(), primary, backend.RunRequest{Model: "fable"}, policy)
	if err != nil || served.Why != runstate.SubstitutedForExecutable || len(primary.requests) != 1 {
		t.Fatalf("second turn served = %+v, %v, primary asked %d times", served, err, len(primary.requests))
	}
	if entries, _ := windows.List(); len(entries) != 1 {
		t.Fatalf("entries = %d, want the standing substitution said once", len(entries))
	}
}

// An alternate on the configured provider would be started by the same missing
// executable, so it is not asked, and the executable's own error is the answer.
func TestAnExecutableThatCannotRunIsReportedWhereNoOtherProviderIsConfigured(t *testing.T) {
	t.Parallel()

	for _, alternate := range []string{"", "sonnet"} {
		primary := &fakeProvider{errs: []error{missingExecutable(), missingExecutable()}}
		windows := newTestWindows(t)
		_, served, err := Serve(context.Background(), primary, backend.RunRequest{Model: "fable"}, Policy{Alternate: alternate, Windows: windows, Now: fixedNow})
		if !strings.Contains(fmtErr(err), "not found on PATH") || served.Substituted() || len(primary.requests) != 1 {
			t.Fatalf("alternate %q: served = %+v, err = %v, asked %d", alternate, served, err, len(primary.requests))
		}
		if entries, _ := windows.List(); len(entries) != 0 {
			t.Fatalf("alternate %q: entries = %+v, want nothing recorded", alternate, entries)
		}
	}
}

func TestAnAlternateThatCannotRunEitherReturnsBothFailuresAndRecordsNothing(t *testing.T) {
	t.Parallel()

	primary := &fakeProvider{errs: []error{missingExecutable()}}
	crossed := &fakeProvider{errs: []error{errors.Join(execution.ErrProcessNotStarted, &backend.ExecutableError{Binary: "codex", Provider: "codex", Path: "/usr/bin", Cause: exec.ErrNotFound})}}
	windows := newTestWindows(t)
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) { return request, nil })
	policy.AlternateProvider = crossed

	_, _, err := Serve(context.Background(), primary, backend.RunRequest{Model: "fable"}, policy)
	if !strings.Contains(fmtErr(err), `"claude"`) || !strings.Contains(fmtErr(err), `"codex"`) {
		t.Fatalf("err = %v, want both executables named", err)
	}
	if entries, _ := windows.List(); len(entries) != 0 {
		t.Fatalf("entries = %+v, want nothing recorded for a turn nothing served", entries)
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
