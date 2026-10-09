package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/research"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

const (
	// OriginDefault marks a value no layer supplied, which the harness filled
	// in itself.
	OriginDefault = "harness-default"
	// OriginDerived marks a value computed from another configured value.
	OriginDerived = "derived:product.id"
	// OriginDerivedRepairGrant marks a triage repair grant no layer stated,
	// which takes the size of the effective repair budget rather than a number
	// of its own.
	OriginDerivedRepairGrant = "derived:execution.repair_attempts_before_replan"
	// OriginDerivedAccount marks an agent's account no layer stated, which
	// follows the single account the effective mapping declares rather than a
	// value of its own.
	OriginDerivedAccount = "derived:accounts"
	// OriginRoleCapabilities marks an agent's capability set, which no layer
	// states and none may: it is read off the role's bundle in the harness's
	// registry, so what an agent holds is the registry's statement wherever the
	// rest of the agent came from.
	OriginRoleCapabilities = "registry:role-capabilities"
	// OriginInput marks a configuration decoded from a stream rather than read
	// from a project file.
	OriginInput = "configuration input"
)

// Resolved is an effective configuration together with the provenance needed to
// explain it: which layers were applied, and where every effective value came
// from. Inheritance is only debuggable if the answer to "why is this value
// what it is" is recorded rather than reconstructed.
type Resolved struct {
	Config  Config            `json:"config"`
	Path    string            `json:"path,omitempty"`
	Sources []string          `json:"sources"`
	Origins map[string]string `json:"origins"`
	// RoleDefinitions are validated files, not effective authority. Loading one
	// never changes an agent's role, capabilities, or configuration revision.
	RoleDefinitions map[string]RoleDefinition `json:"role_definitions,omitempty"`
}

// OriginKeys lists every recorded key in a stable order.
func (r Resolved) OriginKeys() []string {
	keys := make([]string, 0, len(r.Origins))
	for key := range r.Origins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// layer is one configuration document plus where it came from and how its
// persona paths resolve.
type layer struct {
	origin   string
	document configDocument
	personas personaLoader
}

// Load reads and validates the effective configuration at a path.
func Load(path string) (Config, error) {
	resolved, err := LoadResolved(path)
	return resolved.Config, err
}

// LoadResolved reads a project configuration, overlays it on the bundle it
// extends, validates the result, and reports the provenance of every value.
func LoadResolved(path string) (Resolved, error) {
	return loadResolved(path, true)
}

// RoleHistoryProduct identifies the audit's product and repository without
// reading today's role definitions. A broken or removed definition must not
// hide its history. It returns no agent configuration or authority for a caller
// to execute.
func RoleHistoryProduct(path string) (Product, error) {
	resolved, err := loadResolved(path, false)
	return resolved.Config.Product, err
}

func loadResolved(path string, definitions bool) (Resolved, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve config path %q: %w", path, err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return Resolved{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	document, err := decodeDocument(file)
	if err != nil {
		return Resolved{}, err
	}
	resolved, err := resolveDocument(document, absolute, personaLoaderFor(absolute))
	if err != nil {
		return Resolved{}, err
	}
	resolved.Path = absolute
	if err := resolved.Config.Validate(); err != nil {
		return Resolved{}, err
	}
	if err := resolveIntentRepository(&resolved.Config, absolute); err != nil {
		return Resolved{}, err
	}
	if definitions {
		resolved.RoleDefinitions, err = LoadRoleDefinitions(absolute)
		if err != nil {
			return Resolved{}, err
		}
	}
	return resolved, nil
}

// Decode reads a complete configuration from a stream. Personas declared by the
// stream itself cannot be resolved, because a stream has no project directory
// to resolve them against; inherited built-in personas still load.
func Decode(reader io.Reader) (Config, error) {
	resolved, err := DecodeResolved(reader)
	return resolved.Config, err
}

// DecodeResolved is Decode with the provenance of every effective value.
func DecodeResolved(reader io.Reader) (Resolved, error) {
	document, err := decodeDocument(reader)
	if err != nil {
		return Resolved{}, err
	}
	resolved, err := resolveDocument(document, OriginInput, unavailablePersonaLoader{
		reason: "the configuration was decoded from a stream and has no project " + DirectoryName + " directory",
	})
	if err != nil {
		return Resolved{}, err
	}
	if err := resolved.Config.Validate(); err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}

func resolveDocument(document configDocument, origin string, personas personaLoader) (Resolved, error) {
	// The project layer states its own schema version even when the bundle it
	// extends declares one. An inherited version would let a file written
	// against a different schema load as whatever the bundle happened to say,
	// which is the one thing the version is supposed to prevent.
	if document.Version == nil {
		return Resolved{}, ValidationError{Problems: []string{
			fmt.Sprintf("version must be %d and is required in the project configuration; it is not inherited from an extended bundle", CurrentVersion),
		}}
	}

	layers := make([]layer, 0, 2)
	if document.Extends != nil {
		inherited, err := loadBuiltinBundle(*document.Extends)
		if err != nil {
			return Resolved{}, err
		}
		layers = append(layers, layer{origin: inherited.name, document: inherited.document, personas: inherited.personas})
	}
	layers = append(layers, layer{origin: origin, document: document, personas: personas})
	return resolveLayers(layers)
}

func resolveLayers(layers []layer) (Resolved, error) {
	state := newResolution()
	sources := make([]string, 0, len(layers))
	for _, applied := range layers {
		if err := state.apply(applied); err != nil {
			return Resolved{}, err
		}
		sources = append(sources, applied.origin)
	}
	return state.finish(sources)
}

type resolution struct {
	config  Config
	origins map[string]string
	agents  map[string]*agentResolution
}

type agentResolution struct {
	config  AgentConfig
	origins map[string]string
	persona *personaReference
	remit   *personaReference
}

type personaReference struct {
	version string
	path    string
	loader  personaLoader
}

func newResolution() *resolution {
	return &resolution{
		config: Config{
			// The artifact directories are harness defaults rather than bundle
			// values for the same reason the rest of `product` is absent from the
			// bundle: they describe the project. A project that follows the
			// recommended layout never writes any of them down.
			Product: Product{
				Specifications: DefaultSpecifications,
				Invariants:     DefaultInvariants,
				Designs:        DefaultDesigns,
				Decisions:      DefaultDecisions,
			},
			Execution: Execution{
				MaxConcurrentDevelopers:    1,
				RepairAttemptsBeforeReplan: 2,
				// Two retries is the same shape of bound as the repair one: enough
				// for a target that moved under a run to be caught up with, and far
				// short of a run that keeps re-reviewing a change it can never land.
				IntegrationRetriesBeforeReconciliation: 2,
				// And the same shape again, for the third thing a run absorbs rather
				// than fails on: enough that the provider dropping a connection twice
				// running is survived, and short of a run that keeps asking a provider
				// that is not going to answer it.
				TransientRelaunchesBeforeBlocking:     2,
				WorktreeRoot:                          "auto",
				Remote:                                defaultRemote,
				UsageLimitMaxPause:                    defaultUsageLimitMaxPause,
				UsageLimitInProcessPause:              defaultUsageLimitInProcessPause,
				UsageLimitUnknownResetPause:           defaultUsageLimitUnknownResetPause,
				ServerOverloadPause:                   defaultServerOverloadPause,
				CheckTimeout:                          defaultCheckTimeout,
				CheckStageTimeout:                     defaultCheckStageTimeout,
				LandingCheckTimeout:                   defaultLandingCheckTimeout,
				WorkPoll:                              defaultWorkPoll,
				RedeployDrainLimit:                    defaultRedeployDrainLimit,
				FactoryStallAfter:                     defaultFactoryStallAfter,
				CouldNotRunBeforeStatus:               defaultCouldNotRunBeforeStatus,
				MissingReportsBeforeFreshConversation: DefaultMissingReportsBeforeFreshConversation,
				BlockedRunsBeforeIntakeHold:           defaultBlockedRunsBeforeIntakeHold,
				BrakeCooldown:                         defaultBrakeCooldown,
				BrakeEscalationCycles:                 defaultBrakeEscalationCycles,
				// The declarative path is what a new run executes unless the project
				// says otherwise, so it is a harness default like every other value
				// here rather than the absence of a key. A project that wrote nothing
				// gets it, and a project that rolled back is one that wrote `false`.
				DeclarativeDelivery: true,
			},
			// The repair grant is deliberately absent here. It is derived from
			// the effective repair budget once every layer has been applied, so
			// a default written now would be a number that stopped tracking the
			// budget the moment a layer changed it.
			Triage: Triage{
				StuckMergeAge:   defaultStuckMergeAge,
				ReviewRoundsCap: defaultReviewRoundsCap,
			},
			Exchange: Exchange{MaxRounds: defaultExchangeMaxRounds},
			// The picture's age is measured whether or not a project mentions it, so
			// the threshold it is measured against is always present.
			Conversation: Conversation{RefreshAfterLandings: DefaultRefreshAfterLandings},
			// Publishing and work_items are the approvals with a harness default,
			// because they are the ones added after configurations existed: a file
			// mentioning neither loads rather than failing over a key that did not
			// exist when it was written.
			//
			// The bundle states both, at the same value these defaults hold, so a
			// project that extends it inherits nothing it did not already have and
			// upgrading the executable moves neither. Both are opt-ins, and an
			// opt-in that arrived by inheritance would not be one.
			//
			// Only one of the two is behaviour-preserving, and the difference is
			// worth stating where the default is written rather than only in the
			// guide. Publishing nothing is exactly what a file written before that
			// key got. Asking about every work item is not: before work_items
			// existed the product manager admitted work to the backlog directly and
			// the operator was told afterwards, and `human` refuses that admission
			// as well as the automatic one. That is deliberate — a `human` setting
			// that gated the proposal path alone would be one the product manager
			// could walk around by taking the other door — and it costs the work
			// nothing, because what is refused is proposed instead and the operator
			// decides. A project that wants the old behaviour sets `automatic`.
			Approvals: Approvals{Publishing: domain.ApprovalHuman, WorkItems: domain.ApprovalHuman},
			// One account, named, is what every project has until pooling exists, so
			// it is a harness default rather than something a project states: a file
			// that says nothing about accounts still has runs that say which account
			// they ran under, and naming a second one is what a project writes.
			Accounts: map[string]Account{DefaultAccountAlias: {}},
			// Every part of the product is present at its default whether or not
			// a project mentions it, because the set of parts is the product's
			// shape: what a project writes is which of them are on, and where the
			// one that listens is reached.
			Services: DefaultServices(),
		},
		origins: map[string]string{
			"accounts":                                            OriginDefault,
			"services.slack.enabled":                              OriginDefault,
			"services.dashboard.enabled":                          OriginDefault,
			"services.dashboard.port":                             OriginDefault,
			"services.dashboard.bind":                             OriginDefault,
			"services.dashboard.allowed_hosts":                    OriginDefault,
			"services.dashboard.token":                            OriginDefault,
			"services.scheduler.enabled":                          OriginDefault,
			"services.maintenance.enabled":                        OriginDefault,
			"services.maintenance.every":                          OriginDefault,
			"product.specifications":                              OriginDefault,
			"product.invariants":                                  OriginDefault,
			"product.designs":                                     OriginDefault,
			"product.decisions":                                   OriginDefault,
			"approvals.publishing":                                OriginDefault,
			"approvals.work_items":                                OriginDefault,
			"execution.max_concurrent_developers":                 OriginDefault,
			"execution.repair_attempts_before_replan":             OriginDefault,
			"execution.integration_retries_before_reconciliation": OriginDefault,
			"execution.transient_relaunches_before_blocking":      OriginDefault,
			"execution.worktree_root":                             OriginDefault,
			"execution.remote":                                    OriginDefault,
			"execution.usage_limit_max_pause":                     OriginDefault,
			"execution.usage_limit_in_process_pause":              OriginDefault,
			"execution.usage_limit_unknown_reset_pause":           OriginDefault,
			"execution.server_overload_pause":                     OriginDefault,
			"execution.check_timeout":                             OriginDefault,
			"execution.check_stage_timeout":                       OriginDefault,
			"execution.landing_check_timeout":                     OriginDefault,
			"execution.work_poll":                                 OriginDefault,
			"execution.redeploy_drain_limit":                      OriginDefault,
			"execution.factory_stall_after":                       OriginDefault,
			"execution.could_not_run_before_status":               OriginDefault,
			"execution.missing_reports_before_fresh_conversation": OriginDefault,
			"execution.blocked_runs_before_intake_hold":           OriginDefault,
			"execution.brake_cooldown":                            OriginDefault,
			"execution.brake_escalation_cycles":                   OriginDefault,
			"execution.declarative_delivery":                      OriginDefault,
			"execution.merge_queue":                               OriginDefault,
			"triage.stuck_merge_age":                              OriginDefault,
			"triage.review_rounds_cap":                            OriginDefault,
			"exchange.max_rounds":                                 OriginDefault,
			"conversation.refresh_after_landings":                 OriginDefault,
		},
		agents: map[string]*agentResolution{},
	}
}

func (r *resolution) apply(applied layer) error {
	document := applied.document
	setValue(r.origins, "version", document.Version, &r.config.Version, applied.origin)
	setValue(r.origins, "extends", document.Extends, &r.config.Extends, applied.origin)

	if product := document.Product; product != nil {
		setValue(r.origins, "product.id", product.ID, &r.config.Product.ID, applied.origin)
		setValue(r.origins, "product.repository_id", product.RepositoryID, &r.config.Product.RepositoryID, applied.origin)
		setValue(r.origins, "product.repository", product.Repository, &r.config.Product.Repository, applied.origin)
		setValue(r.origins, "product.specifications", product.Specifications, &r.config.Product.Specifications, applied.origin)
		setValue(r.origins, "product.invariants", product.Invariants, &r.config.Product.Invariants, applied.origin)
		setValue(r.origins, "product.designs", product.Designs, &r.config.Product.Designs, applied.origin)
		setValue(r.origins, "product.decisions", product.Decisions, &r.config.Product.Decisions, applied.origin)
		setValue(r.origins, "product.shipped_documentation", product.ShippedDocumentation, &r.config.Product.ShippedDocumentation, applied.origin)
	}
	if execution := document.Execution; execution != nil {
		setValue(r.origins, "execution.max_concurrent_developers", execution.MaxConcurrentDevelopers, &r.config.Execution.MaxConcurrentDevelopers, applied.origin)
		setValue(r.origins, "execution.repair_attempts_before_replan", execution.RepairAttemptsBeforeReplan, &r.config.Execution.RepairAttemptsBeforeReplan, applied.origin)
		setValue(r.origins, "execution.integration_retries_before_reconciliation", execution.IntegrationRetriesBeforeReconciliation, &r.config.Execution.IntegrationRetriesBeforeReconciliation, applied.origin)
		setValue(r.origins, "execution.transient_relaunches_before_blocking", execution.TransientRelaunchesBeforeBlocking, &r.config.Execution.TransientRelaunchesBeforeBlocking, applied.origin)
		setValue(r.origins, "execution.worktree_root", execution.WorktreeRoot, &r.config.Execution.WorktreeRoot, applied.origin)
		setValue(r.origins, "execution.remote", execution.Remote, &r.config.Execution.Remote, applied.origin)
		setValue(r.origins, "execution.push_remote", execution.PushRemote, &r.config.Execution.PushRemote, applied.origin)
		setValue(r.origins, "execution.usage_limit_max_pause", execution.UsageLimitMaxPause, &r.config.Execution.UsageLimitMaxPause, applied.origin)
		setValue(r.origins, "execution.usage_limit_in_process_pause", execution.UsageLimitInProcessPause, &r.config.Execution.UsageLimitInProcessPause, applied.origin)
		setValue(r.origins, "execution.usage_limit_unknown_reset_pause", execution.UsageLimitUnknownResetPause, &r.config.Execution.UsageLimitUnknownResetPause, applied.origin)
		setValue(r.origins, "execution.server_overload_pause", execution.ServerOverloadPause, &r.config.Execution.ServerOverloadPause, applied.origin)
		setValue(r.origins, "execution.check_timeout", execution.CheckTimeout, &r.config.Execution.CheckTimeout, applied.origin)
		setValue(r.origins, "execution.check_stage_timeout", execution.CheckStageTimeout, &r.config.Execution.CheckStageTimeout, applied.origin)
		setValue(r.origins, "execution.landing_check_timeout", execution.LandingCheckTimeout, &r.config.Execution.LandingCheckTimeout, applied.origin)
		setValue(r.origins, "execution.work_poll", execution.WorkPoll, &r.config.Execution.WorkPoll, applied.origin)
		setValue(r.origins, "execution.redeploy_drain_limit", execution.RedeployDrainLimit, &r.config.Execution.RedeployDrainLimit, applied.origin)
		setValue(r.origins, "execution.factory_stall_after", execution.FactoryStallAfter, &r.config.Execution.FactoryStallAfter, applied.origin)
		setValue(r.origins, "execution.could_not_run_before_status", execution.CouldNotRunBeforeStatus, &r.config.Execution.CouldNotRunBeforeStatus, applied.origin)
		setValue(r.origins, "execution.missing_reports_before_fresh_conversation", execution.MissingReportsBeforeFreshConversation, &r.config.Execution.MissingReportsBeforeFreshConversation, applied.origin)
		setValue(r.origins, "execution.blocked_runs_before_intake_hold", execution.BlockedRunsBeforeIntakeHold, &r.config.Execution.BlockedRunsBeforeIntakeHold, applied.origin)
		setValue(r.origins, "execution.brake_cooldown", execution.BrakeCooldown, &r.config.Execution.BrakeCooldown, applied.origin)
		setValue(r.origins, "execution.brake_escalation_cycles", execution.BrakeEscalationCycles, &r.config.Execution.BrakeEscalationCycles, applied.origin)
		// A supplied slot list replaces the inherited one entirely, and is copied
		// rather than aliased, for the reasons the check list is: which slot
		// prefers what is one statement, and a layer's own slice must not become
		// the resolved configuration's.
		if execution.DeveloperSlots != nil {
			r.config.Execution.DeveloperSlots = make([]domain.DeveloperSlot, 0, len(*execution.DeveloperSlots))
			for _, slot := range *execution.DeveloperSlots {
				r.config.Execution.DeveloperSlots = append(r.config.Execution.DeveloperSlots, slot.Clone())
			}
			r.origins["execution.developer_slots"] = applied.origin
		}
		// And the label-to-model mapping, replaced and copied for the same two
		// reasons: the order is the statement, and a layer's own slice must not
		// become the resolved configuration's.
		if execution.DeveloperModels != nil {
			r.config.Execution.DeveloperModels = append([]DeveloperModelRule(nil), *execution.DeveloperModels...)
			r.origins["execution.developer_models"] = applied.origin
		}
		// The declarative path carries a harness default like the values above it,
		// because it is what a run does rather than something a project opts into.
		// A layer that writes the key — `false` for the rollback to the legacy
		// path, `true` to state the default explicitly — takes the origin with it,
		// so `config show --origins` names the file a rollback came from.
		setValue(r.origins, "execution.declarative_delivery", execution.DeclarativeDelivery, &r.config.Execution.DeclarativeDelivery, applied.origin)
		// The merge queue is off unless a layer turns it on, and the layer that
		// does takes the origin, so `config show --origins` names the file that
		// switched integration over to the queue.
		setValue(r.origins, "execution.merge_queue", execution.MergeQueue, &r.config.Execution.MergeQueue, applied.origin)
	}
	if triage := document.Triage; triage != nil {
		setValue(r.origins, "triage.stuck_merge_age", triage.StuckMergeAge, &r.config.Triage.StuckMergeAge, applied.origin)
		setValue(r.origins, "triage.review_rounds_cap", triage.ReviewRoundsCap, &r.config.Triage.ReviewRoundsCap, applied.origin)
		setValue(r.origins, "triage.repair_grant_attempts", triage.RepairGrantAttempts, &r.config.Triage.RepairGrantAttempts, applied.origin)
	}
	if asks := document.Exchange; asks != nil {
		setValue(r.origins, "exchange.max_rounds", asks.MaxRounds, &r.config.Exchange.MaxRounds, applied.origin)
	}
	if conversation := document.Conversation; conversation != nil {
		setValue(r.origins, "conversation.refresh_after_landings", conversation.RefreshAfterLandings, &r.config.Conversation.RefreshAfterLandings, applied.origin)
	}
	if evidence := document.Research; evidence != nil {
		// Copied rather than aliased, like the check list: a layer's own slice must
		// not become the resolved configuration's, or a later layer would be editing
		// what an earlier document holds.
		if evidence.Sources != nil {
			r.config.Research.Sources = append([]research.Source(nil), (*evidence.Sources)...)
			r.origins["research.sources"] = applied.origin
		}
		setValue(r.origins, "research.max_queries_per_turn", evidence.MaxQueriesPerTurn, &r.config.Research.MaxQueriesPerTurn, applied.origin)
		setValue(r.origins, "research.timeout", evidence.Timeout, &r.config.Research.Timeout, applied.origin)
	}
	if intent := document.Intent; intent != nil {
		setValue(r.origins, "intent.repository", intent.Repository, &r.config.Intent.Repository, applied.origin)
	}
	if approvals := document.Approvals; approvals != nil {
		setValue(r.origins, "approvals.brief", approvals.Brief, &r.config.Approvals.Brief, applied.origin)
		setValue(r.origins, "approvals.goals", approvals.Goals, &r.config.Approvals.Goals, applied.origin)
		setValue(r.origins, "approvals.designs", approvals.Designs, &r.config.Approvals.Designs, applied.origin)
		setValue(r.origins, "approvals.work_items", approvals.WorkItems, &r.config.Approvals.WorkItems, applied.origin)
		// Copied rather than aliased, like the check list: a layer's own slice must
		// not become the resolved configuration's, or a later layer would be editing
		// what an earlier document holds.
		if approvals.WorkItemExemptions != nil {
			r.config.Approvals.WorkItemExemptions = append([]domain.WorkItemClass(nil), (*approvals.WorkItemExemptions)...)
			r.origins["approvals.work_item_exemptions"] = applied.origin
		}
		setValue(r.origins, "approvals.integration", approvals.Integration, &r.config.Approvals.Integration, applied.origin)
		setValue(r.origins, "approvals.publishing", approvals.Publishing, &r.config.Approvals.Publishing, applied.origin)
	}
	if slack := document.Slack; slack != nil {
		setValue(r.origins, "slack.enabled", slack.Enabled, &r.config.Slack.Enabled, applied.origin)
		setValue(r.origins, "slack.channel", slack.Channel, &r.config.Slack.Channel, applied.origin)
		// Avatars are merged entry by entry rather than replaced wholesale, the
		// way agents are and unlike the check list or the operators: each entry is
		// one speaker's decoration and independent of every other, so a layer that
		// changed the developer's picture said nothing about the reviewer's and
		// should not have to restate it to keep it.
		if slack.Avatars != nil {
			if r.config.Slack.Avatars == nil {
				r.config.Slack.Avatars = map[string]string{}
			}
			for speaker, avatar := range *slack.Avatars {
				r.config.Slack.Avatars[speaker] = avatar
				r.origins["slack.avatars."+speaker] = applied.origin
			}
		}
	}
	if services := document.Services; services != nil {
		// Each service overrides field by field, the way execution does: a layer
		// that switches the dashboard on has said nothing about its port.
		if slack := services.Slack; slack != nil {
			setValue(r.origins, "services.slack.enabled", slack.Enabled, &r.config.Services.Slack.Enabled, applied.origin)
		}
		if dashboard := services.Dashboard; dashboard != nil {
			setValue(r.origins, "services.dashboard.enabled", dashboard.Enabled, &r.config.Services.Dashboard.Enabled, applied.origin)
			setValue(r.origins, "services.dashboard.port", dashboard.Port, &r.config.Services.Dashboard.Port, applied.origin)
			if dashboard.Bind != nil {
				r.config.Services.Dashboard.Bind = strings.TrimSpace(*dashboard.Bind)
				r.origins["services.dashboard.bind"] = applied.origin
			}
			// Copied rather than aliased, like the check list: a layer's own slice
			// must not become the resolved configuration's, or a later layer would
			// be editing what an earlier document holds.
			if dashboard.AllowedHosts != nil {
				r.config.Services.Dashboard.AllowedHosts = append(make([]string, 0, len(*dashboard.AllowedHosts)), (*dashboard.AllowedHosts)...)
				r.origins["services.dashboard.allowed_hosts"] = applied.origin
			}
			if dashboard.Token != nil {
				r.config.Services.Dashboard.Token = DashboardTokenSource(strings.TrimSpace(string(*dashboard.Token)))
				r.origins["services.dashboard.token"] = applied.origin
			}
		}
		if scheduler := services.Scheduler; scheduler != nil {
			setValue(r.origins, "services.scheduler.enabled", scheduler.Enabled, &r.config.Services.Scheduler.Enabled, applied.origin)
		}
		if maintenance := services.Maintenance; maintenance != nil {
			setValue(r.origins, "services.maintenance.enabled", maintenance.Enabled, &r.config.Services.Maintenance.Enabled, applied.origin)
			setValue(r.origins, "services.maintenance.every", maintenance.Every, &r.config.Services.Maintenance.Every, applied.origin)
		}
	}
	// A supplied operators mapping replaces the inherited one entirely, for the
	// reason the check list below does and with more riding on it: it says who may
	// act, and a mapping half from one layer and half from another is not the set
	// of humans either layer named.
	if document.Operators != nil {
		operators := make(map[string]Operator, len(*document.Operators))
		for name, operator := range *document.Operators {
			operators[name] = operator.clone()
		}
		r.config.Operators = operators
		r.origins["operators"] = applied.origin
	}
	// A supplied accounts mapping replaces the inherited one entirely, for the
	// reason the operators mapping above does: what accounts exist is one
	// statement, and a set assembled from two layers is not the one either of
	// them wrote.
	if document.Accounts != nil {
		accounts := make(map[string]Account, len(*document.Accounts))
		for alias, account := range *document.Accounts {
			accounts[alias] = account
		}
		r.config.Accounts = accounts
		r.origins["accounts"] = applied.origin
	}
	// A supplied providers mapping replaces the inherited one entirely, for the
	// reason the accounts mapping above does: which providers a project may name
	// is one statement, and a dialect assembled from two layers is one nobody
	// wrote and nobody could review.
	if document.Providers != nil {
		providers := make(map[string]backend.ProviderPlugin, len(*document.Providers))
		for id, plugin := range *document.Providers {
			providers[id] = plugin
		}
		r.config.Providers = providers
		r.origins["providers"] = applied.origin
	}
	// A supplied Codex section replaces the inherited one entirely, for the
	// reason the providers mapping above does.
	if document.Codex != nil {
		r.config.Codex = *document.Codex
		r.origins["codex"] = applied.origin
	}
	if document.ClaudeCode != nil {
		r.config.ClaudeCode = *document.ClaudeCode
		r.origins["claude_code"] = applied.origin
	}
	// A supplied recurring-task mapping replaces the inherited one entirely, for
	// the reason the providers mapping above does, and for one of its own: a task
	// is a role, a cadence, and a prompt read together, so a layer that overrode
	// only the prompt would leave a role woken on a cadence nobody chose for it.
	if document.RecurringTasks != nil {
		tasks := make(map[string]RecurringTask, len(*document.RecurringTasks))
		for name, task := range *document.RecurringTasks {
			tasks[name] = task
		}
		r.config.RecurringTasks = tasks
		r.origins["recurring_tasks"] = applied.origin
	}
	// A supplied check list replaces the inherited one entirely: checks are the
	// gate on integration, and a silently concatenated list is not the gate
	// either layer described.
	if document.Checks != nil {
		r.config.Checks = append([]string(nil), (*document.Checks)...)
		r.origins["checks"] = applied.origin
	}
	if document.LandingChecks != nil {
		r.config.LandingChecks = append([]string(nil), (*document.LandingChecks)...)
		r.origins["landing_checks"] = applied.origin
	}
	if document.PathChecks != nil {
		r.config.PathChecks = append([]PathCheck(nil), (*document.PathChecks)...)
		r.origins["path_checks"] = applied.origin
	}

	for _, name := range sortedAgentNames(document.Agents) {
		if err := r.applyAgent(name, document.Agents[name], applied); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolution) applyAgent(name string, document agentDocument, applied layer) error {
	if document.Disabled != nil && *document.Disabled {
		if document.overridesFields() {
			return fmt.Errorf("agent %q is disabled and also configured; remove one of them", name)
		}
		if _, inherited := r.agents[name]; !inherited {
			return fmt.Errorf("agent %q is disabled but no inherited agent by that name exists", name)
		}
		delete(r.agents, name)
		return nil
	}

	agent, exists := r.agents[name]
	if !exists {
		agent = &agentResolution{origins: map[string]string{}}
		r.agents[name] = agent
	}
	setValue(agent.origins, "role", document.Role, &agent.config.Role, applied.origin)
	setValue(agent.origins, "backend", document.Backend, &agent.config.Backend, applied.origin)
	setValue(agent.origins, "instances", document.Instances, &agent.config.Instances, applied.origin)
	if document.Model != nil {
		agent.config.Model = strings.TrimSpace(*document.Model)
		agent.origins["model"] = applied.origin
	}
	if document.ModelVersion != nil {
		// Stated empty removes an inherited pin rather than leaving it in place, so
		// a layer can put an alias back to floating without having to know which
		// layer underneath pinned it.
		agent.config.ModelVersion = strings.TrimSpace(*document.ModelVersion)
		agent.origins["model_version"] = applied.origin
	}
	if document.Effort != nil {
		agent.config.Effort = strings.TrimSpace(*document.Effort)
		agent.origins["effort"] = applied.origin
	}
	if document.Account != nil {
		agent.config.Account = strings.TrimSpace(*document.Account)
		agent.origins["account"] = applied.origin
	}
	if document.Persona != nil {
		// A persona override replaces the inherited persona completely, so both
		// halves of the reference must come from the overriding layer.
		if document.Persona.Version == nil || document.Persona.Path == nil {
			return fmt.Errorf("agent %q persona override must declare both version and path", name)
		}
		agent.persona = &personaReference{
			version: strings.TrimSpace(*document.Persona.Version),
			path:    strings.TrimSpace(*document.Persona.Path),
			loader:  applied.personas,
		}
		agent.origins["persona"] = applied.origin
	}
	if document.Lane != nil {
		agent.config.Lane = strings.TrimSpace(*document.Lane)
		agent.origins["lane"] = applied.origin
	}
	if document.Remit != nil {
		// A remit is held to the persona's rules, this one among them: an override
		// replaces the inherited remit completely, so both halves come from it.
		if document.Remit.Version == nil || document.Remit.Path == nil {
			return fmt.Errorf("agent %q remit override must declare both version and path", name)
		}
		agent.remit = &personaReference{
			version: strings.TrimSpace(*document.Remit.Version),
			path:    strings.TrimSpace(*document.Remit.Path),
			loader:  applied.personas,
		}
		agent.origins["remit"] = applied.origin
	}
	if document.Triggers != nil {
		triggers := Triggers{Every: document.Triggers.Every}
		if len(document.Triggers.On) > 0 {
			triggers.On = make([]TriggerEvent, 0, len(document.Triggers.On))
			for _, event := range document.Triggers.On {
				triggers.On = append(triggers.On, TriggerEvent(strings.TrimSpace(string(event))))
			}
		}
		agent.config.Triggers = triggers
		agent.origins["triggers"] = applied.origin
	}
	if document.Failover != nil {
		// The block replaces whatever was inherited, so an override that names only
		// half of it leaves the other half at its zero value rather than at some
		// underneath layer's — which is what keeps `enabled: false` a way of
		// switching failover off rather than a way of inheriting somebody else's
		// alternate.
		failover := Failover{}
		if document.Failover.Enabled != nil {
			failover.Enabled = *document.Failover.Enabled
		}
		if document.Failover.Model != nil {
			failover.Model = strings.TrimSpace(*document.Failover.Model)
		}
		if document.Failover.Provider != nil {
			failover.Provider = domain.Backend(strings.TrimSpace(string(*document.Failover.Provider)))
		}
		if document.Failover.Account != nil {
			failover.Account = strings.TrimSpace(*document.Failover.Account)
		}
		if document.Failover.Effort != nil {
			effort := strings.TrimSpace(*document.Failover.Effort)
			failover.Effort = &effort
		}
		agent.config.Failover = failover
		agent.origins["failover"] = applied.origin
	}
	if document.Conversations != nil {
		// Stated empty removes an inherited choice rather than leaving it in place,
		// so a layer can put an agent back to queueing without having to know which
		// layer underneath gave it side threads.
		agent.config.Conversations = ConversationMode(strings.TrimSpace(string(*document.Conversations)))
		agent.origins["conversations"] = applied.origin
	}
	return nil
}

func (r *resolution) finish(sources []string) (Resolved, error) {
	effective := r.config
	origins := make(map[string]string, len(r.origins))
	for key, origin := range r.origins {
		origins[key] = origin
	}

	// What the harness may do on an agent's behalf is the registry's to say, so
	// it is read once here rather than restated by any layer. A registry that
	// will not build is a defect in the table it is built from, and it stops the
	// configuration loading rather than leaving every agent holding nothing.
	holders, err := rolecapability.Default()
	if err != nil {
		return Resolved{}, err
	}

	agents := make(map[string]AgentConfig, len(r.agents))
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		agent := r.agents[name]
		effectiveAgent := agent.config
		if _, supplied := agent.origins["instances"]; !supplied {
			effectiveAgent.Instances = 1
			agent.origins["instances"] = OriginDefault
		}
		// An agent that names no account runs on the project's single one, read
		// after every layer has had its say so it follows the mapping that will
		// actually be in force rather than one some layer underneath declared. A
		// pooled configuration assigns nothing here, deliberately: which account
		// serves a run is the pool's to decide as the run starts, and an assignment
		// written now would be this file answering a question it does not have the
		// evidence for.
		if _, supplied := agent.origins["account"]; !supplied {
			if alias := effective.AccountAlias(); alias != "" {
				effectiveAgent.Account = alias
				agent.origins["account"] = OriginDerivedAccount
			}
		}
		// The capability set follows the role, whichever layer named the role and
		// whatever else the layers said about the agent. A role the registry does
		// not describe is left holding nothing and is reported as an unknown role
		// when the effective configuration is validated, which names the mistake
		// where an operator can fix it.
		if bundle, described := holders.Bundle(effectiveAgent.Role); described {
			effectiveAgent.Capabilities = bundle.Holds
			agent.origins["capabilities"] = OriginRoleCapabilities
		}
		if agent.persona != nil {
			text, source, err := agent.persona.loader.load("persona", agent.persona.path)
			if err != nil {
				return Resolved{}, fmt.Errorf("agent %q: %w", name, err)
			}
			effectiveAgent.Persona = Persona{
				Version: agent.persona.version,
				Path:    agent.persona.path,
				Source:  source,
				Bytes:   len(text),
				Text:    text,
			}
		}
		if agent.remit != nil {
			text, source, err := agent.remit.loader.load("remit", agent.remit.path)
			if err != nil {
				return Resolved{}, fmt.Errorf("agent %q: %w", name, err)
			}
			effectiveAgent.Remit = Persona{
				Version: agent.remit.version,
				Path:    agent.remit.path,
				Source:  source,
				Bytes:   len(text),
				Text:    text,
			}
		}
		agents[name] = effectiveAgent
		for field, origin := range agent.origins {
			origins["agents."+name+"."+field] = origin
		}
	}
	effective.Agents = agents

	// A grant no layer stated is the effective repair budget, read after every
	// layer has had its say so it follows the budget that will actually be spent
	// rather than the one some layer underneath was written against.
	if _, supplied := origins["triage.repair_grant_attempts"]; !supplied {
		effective.Triage.RepairGrantAttempts = derivedRepairGrant(effective.Execution.RepairAttemptsBeforeReplan)
		origins["triage.repair_grant_attempts"] = OriginDerivedRepairGrant
	}

	if effective.Product.RepositoryID == "" && effective.Product.ID != "" {
		effective.Product.RepositoryID = domain.RepositoryID(effective.Product.ID)
		origins["product.repository_id"] = OriginDerived
	}

	return Resolved{Config: effective, Sources: sources, Origins: origins}, nil
}

// derivedRepairGrant sizes an unstated triage grant from the repair budget a
// run starts with. A project that configured no repair attempts at all still
// gets a grant of one rather than a configuration that fails to load: the grant
// is triage's deliberate exception to that budget, so a project saying runs
// repair nothing routinely is not the same as saying triage may grant nothing.
func derivedRepairGrant(repairAttempts int) int {
	if repairAttempts < minimumRepairGrant {
		return minimumRepairGrant
	}
	return repairAttempts
}

// setValue applies one supplied field and records where it came from. A field a
// layer did not supply is left alone, which is what makes a sparse override
// inherit the rest.
func setValue[T any](origins map[string]string, key string, supplied *T, target *T, origin string) {
	if supplied == nil {
		return
	}
	*target = *supplied
	origins[key] = origin
}

func sortedAgentNames(agents map[string]agentDocument) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
