package cli

// The factory's health as a manager observes it, through health.observe and
// health.follow, and as the harness hands it to every scheduled pass of the
// development manager and of the factory-flow program manager.

import (
	"context"
	"sort"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backend/adapters"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/health"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// productHealth observes one product. The sources are built afresh for every
// observation, over the same records `yoyo status` reads, so an observation
// reads the configuration and the records as they stand when it is taken and
// never as they stood when a conversation or a session opened.
type productHealth struct {
	parts components
}

func (p productHealth) observer() health.Observer {
	observer := health.Observer{
		Product:          p.parts.config.Product.ID,
		Sources:          standingSources(p.parts.configPath),
		ProviderCommands: providerCommands(p.parts.config),
		RedactValues:     p.parts.redactValues,
	}
	// An interface holding a nil store is not nil, so the keeper is set only
	// where there is one; the observation then says it could not be kept.
	if store, err := runstate.NewHealthObservationStore(p.parts.stateRoot, p.parts.config.Product.ID); err == nil {
		observer.Keeper = store
	}
	return observer
}

func (p productHealth) Observe(ctx context.Context, caller health.Caller, runs []string) health.Observation {
	return p.observer().Observe(ctx, caller, runs)
}

func (p productHealth) Follow(id, cursor string) (health.Part, error) {
	return p.observer().Follow(id, cursor)
}

// Deliver is the observation a scheduled pass is handed.
func (p productHealth) Deliver(ctx context.Context, role domain.AgentRole, agent string) (string, *runstate.SweepHealth) {
	return p.observer().Deliver(ctx, health.Caller{Role: role, Agent: agent})
}

// providerCommands is the command every configured agent's provider launches,
// and its alternate's where it fails over to another provider, as the
// configuration resolves each: the commands a part that launches roles has to
// find on its search path.
func providerCommands(cfg config.Config) []string {
	seen := map[string]bool{}
	var commands []string
	add := func(named domain.Backend) {
		if named == "" {
			return
		}
		descriptor, found := providerDescriptor(cfg, named)
		if !found {
			descriptor = backend.Descriptor{Adapter: named}
		}
		if binary := adapters.Binary(descriptor); binary != "" && !seen[binary] {
			seen[binary] = true
			commands = append(commands, binary)
		}
	}
	for _, endpoint := range agentEndpoints(cfg) {
		add(endpoint.Provider)
		add(endpoint.AlternateProvider)
	}
	sort.Strings(commands)
	return commands
}

// conversationHealthObserver is the health hand a conversation is wired with,
// or nil where there is no state root to keep observations under, which the
// conversation refuses plainly.
func conversationHealthObserver(parts components) chat.HealthObserver {
	if parts.stateRoot == "" {
		return nil
	}
	return productHealth{parts: parts}
}
