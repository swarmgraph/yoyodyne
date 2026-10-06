package cli

// Resolving the provider an agent named into the adapter that runs it.
//
// A backend identifier is not always one this build ships. A project may declare
// a provider of its own — which roles it serves, which tool postures it can hold
// them to, which compiled adapter launches it, which binary that adapter runs,
// and how to read what it says about rate limits, retries, and reset times — and
// an agent may name it. This is the one place that turns the name into a running
// adapter, so a declared dialect actually reads the stream rather than sitting
// in a registry nothing consults.

import (
	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/adapters"
	"github.com/mason-bryant/yoyodyne/internal/backend/claudecode"
	"github.com/mason-bryant/yoyodyne/internal/backend/codex"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// providerDescriptor is what this project says about one backend: the built-in
// description for a backend this build ships, the project's own for one it
// declared, and nothing for a name neither covers. A configuration that will not
// resolve answers nothing rather than guessing, because it has already been
// refused where it was loaded.
func providerDescriptor(cfg config.Config, named domain.Backend) (backend.Descriptor, bool) {
	registry, err := cfg.ProviderRegistry()
	if err != nil {
		return backend.Descriptor{}, false
	}
	return registry.Lookup(named)
}

// providerRegistry is the set of providers this project may name, and nothing
// when the configuration will not resolve one — which is a configuration that
// has already been refused where it was loaded. A caller handed nothing falls
// back to the backends this build ships, which is what every project that
// declares no provider of its own gets anyway.
func providerRegistry(cfg config.Config) *backend.Registry {
	registry, err := cfg.ProviderRegistry()
	if err != nil {
		return nil
	}
	return registry
}

// providerBackend builds the adapter that runs one agent's provider. A backend
// this project does not describe, or one no compiled adapter can launch, still
// yields an adapter: what refuses it is the run pipeline and the conversation,
// which say so in their own terms and before anything is claimed. Building one
// here that says nothing is what keeps this from becoming a second place that
// decides whether work may start.
func providerBackend(cfg config.Config, named domain.Backend, runner execution.ProcessRunner) backend.Backend {
	return providerBackendIn(cfg, named, runner, "")
}

// providerBackendIn is the same adapter pointed at one account's provider home.
// Which provider runs an agent is the project's decision and which login it runs
// under is this machine's, so they are separate arguments rather than one
// resolved value: an invocation that named no account runs where the machine is
// already signed in, which is what a single-account installation has always
// done.
func providerBackendIn(cfg config.Config, named domain.Backend, runner execution.ProcessRunner, configDir string) backend.Backend {
	if descriptor, known := providerDescriptor(cfg, named); known {
		if provider, built := adapters.For(descriptor, named, runner, configDir); built {
			// What a Codex role is given beside its prompt is what this project
			// names, and nothing from the account's own home. A relative path is
			// read from the checkout the harness itself reads, which the
			// components set as the product's repository, never from a worktree
			// a reviewer is inspecting.
			if codexProvider, isCodex := provider.(codex.Backend); isCodex {
				codexProvider.Context = cfg.Codex.Anchored(cfg.Product.Repository)
				return codexProvider
			}
			return provider
		}
	}
	// Nothing here describes the backend, or nothing in this build launches it.
	// The default adapter is still handed back so that this does not become a
	// second place that decides whether work may start; what refuses it is the
	// run pipeline and the conversation, which say so in their own terms and
	// before anything is claimed.
	return claudecode.Backend{Runner: runner, Provider: named, ConfigDir: configDir}
}

// providerRuns reports a backend this build can actually launch: one it ships an
// adapter for, or one the project declared that runs on one of those. It is what
// the surfaces ask before they start a run or open a conversation, so a provider
// that could never be launched is refused with its own reason rather than
// failing partway through an invocation.
func providerRuns(cfg config.Config, named domain.Backend) bool {
	descriptor, known := providerDescriptor(cfg, named)
	return known && descriptor.Runnable()
}
