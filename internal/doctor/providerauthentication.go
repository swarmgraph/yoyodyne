package doctor

// Whether this installation is authenticating a provider by a key exported in
// the shell.
//
// It used to work. Every provider invocation inherited the harness's whole
// environment, so an `export ANTHROPIC_API_KEY=...` in a shell profile
// authenticated every one of them. Since yoyodyne-ifd.408 an invocation is built
// from an allowlist and a name that reads as a credential is dropped from it, so
// that key reaches nothing — and an installation relying on it does not degrade,
// it is refused by the provider on its next run.
//
// The refusal itself is already reported: the provider check asks the provider
// whether it is authenticated, in the same built environment a run would use, so
// it answers no. What is missing without this is the reason. An operator who can
// run `claude` in their own shell and is told by `yoyo doctor` that `claude` is
// not authenticated has been handed a contradiction, and the thing that resolves
// it is the variable they exported.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// checkProviderAuthentication reports the provider keys this environment
// carries, and what they now do, which is nothing.
//
// It is a warning rather than a problem, by the one question this package
// separates the two with: would this stop work running. A key exported beside a
// provider that is signed in stops nothing — the login in the provider home is
// what authenticates, and the key is dead weight. A key exported instead of one
// stops everything, and that is already a problem on the provider's own finding;
// naming it again here would count one broken installation twice while saying
// the same thing.
//
// It is not scoped to the providers the agents name. What it states is a fact
// about the environment rather than about the configuration — this key reaches
// no invocation the harness makes — and it is the same fact whichever provider
// reads it, so the three surfaces that say it read one derivation rather than
// each deciding which keys are worth mentioning.
func (d *diagnosis) checkProviderAuthentication(resolved config.Resolved) Finding {
	const check = "provider-authentication"
	present := d.providerKeys()
	if len(present) == 0 {
		return Finding{
			Check:   check,
			Status:  StatusOK,
			Summary: "no provider key is exported here, so every provider authenticates from its own provider home",
			Detail:  "the login held in the provider home is the only authentication an invocation receives",
		}
	}
	return Finding{
		Check:  check,
		Status: StatusWarning,
		Summary: fmt.Sprintf("%s exported here and reaches no invocation the harness makes",
			describeProviderKeys(present)),
		Detail: "every invocation is built from an allowlist and a name that reads as a credential is dropped from it, " +
			"so an installation authenticating a provider this way is refused by that provider rather than degrading; " +
			"provider authentication is the provider's own login, held in its provider home",
		Remedy: providerKeyRemedy(resolved),
	}
}

// providerKeys names the provider keys this environment carries, asked through
// the diagnosis's own seam rather than of the process, so the whole matrix is
// reachable from a test. The names are the one list execution states; nothing
// here spells a second one.
func (d *diagnosis) providerKeys() []string {
	present := make([]string, 0, len(execution.ProviderKeyNames))
	for _, name := range execution.ProviderKeyNames {
		if strings.TrimSpace(d.getenv(name)) != "" {
			present = append(present, name)
		}
	}
	return present
}

// describeProviderKeys names the keys and agrees with itself about how many
// there are.
func describeProviderKeys(present []string) string {
	if len(present) == 1 {
		return present[0] + " is"
	}
	return strings.Join(present, ", ") + " are"
}

// providerKeyRemedy is the login for the provider this project's developer runs
// on, because that is the invocation an exported key was standing in for and
// the first one a run makes. The agents are read in name order so a
// configuration produces the same remedy on every run rather than whichever one
// the map handed back first, and a project with no readable agent gets the
// provider's own login all the same: the remedy is a command either way, which
// is the promise this package makes.
func providerKeyRemedy(resolved config.Resolved) string {
	registry, _ := resolved.Config.ProviderRegistry()
	names := make([]string, 0, len(resolved.Config.Agents))
	for name := range resolved.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	remedy := ""
	for _, name := range names {
		descriptor, known := registry.Lookup(resolved.Config.Agents[name].Backend)
		if !known {
			continue
		}
		login := AccountLoginCommand(descriptor.Adapter, "", descriptor.Binary)
		if resolved.Config.Agents[name].Role == domain.RoleDeveloper {
			return login
		}
		if remedy == "" {
			remedy = login
		}
	}
	if remedy != "" {
		return remedy
	}
	return providerLoginCommand(domain.BackendClaudeCode)
}
