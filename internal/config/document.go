package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/research"
)

// configDocument is the on-disk shape of a configuration layer. Every field is
// a pointer so a layer can distinguish "not supplied, inherit it" from
// "supplied, replace it". Unknown keys are rejected by the decoder rather than
// silently ignored, so a typo in an override fails closed instead of leaving
// the inherited value quietly in place.
type configDocument struct {
	Version   *int               `yaml:"version"`
	Extends   *string            `yaml:"extends"`
	Product   *productDocument   `yaml:"product"`
	Execution *executionDocument `yaml:"execution"`
	Triage    *triageDocument    `yaml:"triage"`
	Exchange  *exchangeDocument  `yaml:"exchange"`
	// Conversation is absent from every file written before the picture's age
	// was measured, which leaves those projects at the harness default: the
	// measurement is not something a layer opts into, only something it times.
	Conversation *conversationDocument `yaml:"conversation"`
	Research     *researchDocument     `yaml:"research"`
	Intent       *intentDocument       `yaml:"intent"`
	Approvals    *approvalsDocument    `yaml:"approvals"`
	Checks       *[]string             `yaml:"checks"`
	// LandingChecks replaces an inherited list entirely rather than merging into
	// it, for the reason Checks does: what runs after a landing is one
	// statement, and a list half from a bundle and half from a project is not the
	// list either layer wrote.
	LandingChecks *[]string `yaml:"landing_checks"`
	// PathChecks replaces an inherited list entirely, for the same reason.
	PathChecks *[]PathCheck             `yaml:"path_checks"`
	Agents     map[string]agentDocument `yaml:"agents"`
	// Operators replaces an inherited mapping entirely rather than merging into
	// it, for the reason the check list does and the allow-list it absorbed did:
	// who may act is a decision, and a mapping silently assembled from two layers
	// is not the mapping either layer wrote. It decodes straight into the
	// effective type because there is no per-field override to distinguish.
	Operators *map[string]Operator `yaml:"operators"`
	// Accounts replaces an inherited mapping entirely rather than merging into
	// it, for the reason the operators mapping does: what accounts exist is one
	// statement, and a mapping half from a bundle and half from a project is not
	// the set of accounts either layer named. It decodes straight into the
	// effective type because there is no per-field override to distinguish.
	Accounts *map[string]Account `yaml:"accounts"`
	// Providers replaces an inherited mapping entirely rather than merging into
	// it, for the reason the accounts mapping does: which providers a project may
	// name is one statement, and a provider assembled half from a bundle and half
	// from a project is a dialect nobody wrote. It decodes straight into the
	// effective type because there is no per-field override to distinguish.
	Providers *map[string]backend.ProviderPlugin `yaml:"providers"`
	// Codex replaces an inherited section entirely, for the reason the providers
	// mapping does: what a role is given beside its prompt is one statement.
	Codex      *backend.NamedContext `yaml:"codex"`
	ClaudeCode *backend.NamedContext `yaml:"claude_code"`
	Slack      *slackDocument        `yaml:"slack"`
	// Services is the parts of the product and whether each runs. Each entry
	// overrides field by field, the way execution does: a layer that switches
	// the dashboard on has said nothing about its port, and should not have to
	// restate the port to keep it. The set of services is the struct's rather
	// than a mapping, so a name that is not one of them is refused by the
	// decoder like any other unknown key — and named as a service that does not
	// exist, by unknownServices, rather than as a Go field.
	Services *servicesDocument `yaml:"services"`
	// RecurringTasks replaces an inherited mapping entirely rather than merging
	// into it, for the reason the accounts mapping does: what the harness does on
	// a cadence is one statement, and a schedule half from a bundle and half from
	// a project is one nobody wrote. It decodes straight into the effective type
	// because there is no per-field override to distinguish — and because every
	// field of a task is required reading together: a cadence inherited under a
	// prompt somebody replaced is the one shape this must not be able to make.
	RecurringTasks *map[string]RecurringTask `yaml:"recurring_tasks"`
}

type productDocument struct {
	ID             *domain.ProductID    `yaml:"id"`
	RepositoryID   *domain.RepositoryID `yaml:"repository_id"`
	Repository     *string              `yaml:"repository"`
	Specifications *string              `yaml:"specifications"`
	Invariants     *string              `yaml:"invariants"`
	Designs        *string              `yaml:"designs"`
	Decisions      *string              `yaml:"decisions"`
	// ShippedDocumentation is replaced as a whole list rather than concatenated,
	// for the reason checks is: a silently merged list is not the description
	// either layer wrote down.
	ShippedDocumentation *[]string `yaml:"shipped_documentation"`
}

type executionDocument struct {
	MaxConcurrentDevelopers                *int      `yaml:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan             *int      `yaml:"repair_attempts_before_replan"`
	IntegrationRetriesBeforeReconciliation *int      `yaml:"integration_retries_before_reconciliation"`
	TransientRelaunchesBeforeBlocking      *int      `yaml:"transient_relaunches_before_blocking"`
	WorktreeRoot                           *string   `yaml:"worktree_root"`
	Remote                                 *string   `yaml:"remote"`
	PushRemote                             *string   `yaml:"push_remote"`
	UsageLimitMaxPause                     *Duration `yaml:"usage_limit_max_pause"`
	UsageLimitInProcessPause               *Duration `yaml:"usage_limit_in_process_pause"`
	UsageLimitUnknownResetPause            *Duration `yaml:"usage_limit_unknown_reset_pause"`
	ServerOverloadPause                    *Duration `yaml:"server_overload_pause"`
	CheckTimeout                           *Duration `yaml:"check_timeout"`
	CheckStageTimeout                      *Duration `yaml:"check_stage_timeout"`
	LandingCheckTimeout                    *Duration `yaml:"landing_check_timeout"`
	WorkPoll                               *Duration `yaml:"work_poll"`
	RedeployDrainLimit                     *Duration `yaml:"redeploy_drain_limit"`
	FactoryStallAfter                      *Duration `yaml:"factory_stall_after"`
	CouldNotRunBeforeStatus                *int      `yaml:"could_not_run_before_status"`
	MissingReportsBeforeFreshConversation  *int      `yaml:"missing_reports_before_fresh_conversation"`
	BlockedRunsBeforeIntakeHold            *int      `yaml:"blocked_runs_before_intake_hold"`
	BrakeCooldown                          *Duration `yaml:"brake_cooldown"`
	BrakeEscalationCycles                  *int      `yaml:"brake_escalation_cycles"`
	// DeveloperSlots is what each developer slot prefers, one entry per slot in
	// slot order. A supplied list replaces an inherited one wholesale rather than
	// merging with it, the way the check list does: which slot prefers what is one
	// statement, and a list half from a bundle and half from a project is a
	// preference nobody wrote down. Absent leaves every slot preferring nothing,
	// which is what every file written before slots could prefer anything means.
	DeveloperSlots *[]domain.DeveloperSlot `yaml:"developer_slots"`
	// DeveloperModels is the label-to-model mapping a run's developer invocations
	// are resolved against. A supplied list replaces an inherited one wholesale
	// for the reason the slot list does: the order is what decides which of an
	// item's labels wins, and an order half from a bundle and half from a project
	// is one nobody wrote down. Absent leaves every run on the developer's
	// configured model, which is what every file written before this means.
	DeveloperModels *[]DeveloperModelRule `yaml:"developer_models"`
	// DeclarativeDelivery is absent from every file written before it existed and
	// from every file whose project is content with the default. A layer that does
	// not supply it leaves the harness default in force, which is the declarative
	// path; a layer that writes `false` is a project rolling back to the legacy
	// one.
	DeclarativeDelivery *bool `yaml:"declarative_delivery"`
	// MergeQueue is absent from every file written before the merge queue
	// existed, which leaves it off: a layer that does not supply it integrates
	// approved work the way runs always have.
	MergeQueue *bool `yaml:"merge_queue"`
}

type triageDocument struct {
	StuckMergeAge   *Duration `yaml:"stuck_merge_age"`
	ReviewRoundsCap *int      `yaml:"review_rounds_cap"`
	// RepairGrantAttempts is absent from most files. A layer that does not
	// supply it leaves the grant to follow execution.repair_attempts_before_replan,
	// which is a derivation rather than an inherited value: it tracks whatever
	// the effective repair budget turns out to be rather than whatever it was
	// when some layer underneath was written.
	RepairGrantAttempts *int `yaml:"repair_grant_attempts"`
}

type exchangeDocument struct {
	MaxRounds *int `yaml:"max_rounds"`
}

type conversationDocument struct {
	RefreshAfterLandings *int `yaml:"refresh_after_landings"`
}

// researchDocument is absent from every file written before research existed,
// which is what leaves the capability off for them: a layer supplying no sources
// permits none, and that is the state a project stays in until its operator
// names one.
type researchDocument struct {
	// Sources replaces an inherited list wholesale rather than adding to it, the
	// way the check list and the work-item exemptions do: what the harness may
	// reach outside this machine is one statement, and a list half from a bundle
	// and half from a project is a reach nobody decided.
	Sources           *[]research.Source `yaml:"sources"`
	MaxQueriesPerTurn *int               `yaml:"max_queries_per_turn"`
	Timeout           *Duration          `yaml:"timeout"`
}

type approvalsDocument struct {
	Brief   *domain.ApprovalMode `yaml:"brief"`
	Goals   *domain.ApprovalMode `yaml:"goals"`
	Designs *domain.ApprovalMode `yaml:"designs"`
	// WorkItems is absent from files written before per-item approval became a
	// policy rather than the only behavior. A layer that does not supply it
	// leaves the harness default in place, which is the per-item gate such a
	// file was written for.
	WorkItems *domain.ApprovalMode `yaml:"work_items"`
	// WorkItemExemptions replaces an inherited list wholesale rather than adding
	// to it, the way the check list does and unlike the Slack avatars: the list is
	// one statement about how far a project's gate comes down, and half of one
	// operator's answer joined to half of another's is a policy nobody decided.
	WorkItemExemptions *[]domain.WorkItemClass `yaml:"work_item_exemptions"`
	Integration        *domain.ApprovalMode    `yaml:"integration"`
	Publishing         *domain.ApprovalMode    `yaml:"publishing"`
}

// slackDocument deliberately has no operators key. A file that still carries one
// is refused by the decoder, which names the key and the line it is on, because
// who may steer the harness now lives in the top-level operators mapping — and
// an allow-list left under `slack` would be an authority decision nothing reads.
type slackDocument struct {
	Enabled *bool              `yaml:"enabled"`
	Channel *string            `yaml:"channel"`
	Avatars *map[string]string `yaml:"avatars"`
}

type servicesDocument struct {
	Slack       *serviceDocument            `yaml:"slack"`
	Dashboard   *dashboardServiceDocument   `yaml:"dashboard"`
	Scheduler   *serviceDocument            `yaml:"scheduler"`
	Maintenance *maintenanceServiceDocument `yaml:"maintenance"`
}

type maintenanceServiceDocument struct {
	Enabled *bool     `yaml:"enabled"`
	Every   *Duration `yaml:"every"`
}

type serviceDocument struct {
	Enabled *bool `yaml:"enabled"`
}

type dashboardServiceDocument struct {
	Enabled *bool   `yaml:"enabled"`
	Port    *int    `yaml:"port"`
	Bind    *string `yaml:"bind"`
	// AllowedHosts replaces an inherited list wholesale rather than adding to
	// it, the way the check list does: which hosts may reach the page is one
	// statement, and half of one layer's answer joined to half of another's is
	// a set nobody decided.
	AllowedHosts *[]string             `yaml:"allowed_hosts"`
	Token        *DashboardTokenSource `yaml:"token"`
}

type agentDocument struct {
	Role    *domain.AgentRole `yaml:"role"`
	Backend *domain.Backend   `yaml:"backend"`
	Model   *string           `yaml:"model"`
	// ModelVersion is the exact version this agent's turns ask for. It overrides
	// on its own rather than with the model, because the two are separate answers:
	// a layer pinning a version over an inherited family alias is saying which
	// version of that same family to ask for, and a layer that had to restate the
	// alias to say it would be re-choosing the family by accident. Stating it
	// empty removes an inherited pin, which is how an alias is put back to
	// floating.
	ModelVersion *string `yaml:"model_version"`
	// Effort overrides on its own, like the model version: it is one value, and
	// stating it empty removes an inherited level so the provider resolves its
	// own again.
	Effort *string `yaml:"effort"`
	// Account is absent from most files. A layer that does not supply it leaves
	// the agent assigned to the project's single account, which is a derivation
	// rather than an inherited value: it follows whatever account the effective
	// mapping turns out to declare rather than whatever some layer underneath was
	// written against.
	Account   *string `yaml:"account"`
	Instances *int    `yaml:"instances"`
	// Persona replaces an inherited persona completely rather than merging into
	// it, because half of one persona and half of another is guidance nobody
	// wrote.
	Persona *personaDocument `yaml:"persona"`
	// Failover replaces an inherited failover block completely, for the reason
	// the persona does: the enablement and the alternate are one answer, and a
	// layer that switched failover on over an alternate some layer underneath
	// named would be serving this agent's turns from a model nobody chose for it.
	Failover *failoverDocument `yaml:"failover"`
	// Conversations is whether this agent queues a question its main thread
	// cannot take yet or holds it on a side thread. It overrides on its own, like
	// the model version and unlike the failover block, because it is one value
	// rather than an answer in two halves. Stating it empty removes an inherited
	// choice and puts the agent back to queueing.
	Conversations *ConversationMode `yaml:"conversations"`
	// Lane, Remit, and Triggers are a program manager instance's, and are refused
	// on an agent of any other role when the effective configuration is validated.
	// The lane overrides on its own, as one value; the remit replaces an inherited
	// one completely, for the reason the persona does; and the triggers replace
	// the inherited block whole, for the reason the failover block does — the
	// cadence and the events are one answer about when a pass is taken.
	Lane     *string          `yaml:"lane"`
	Remit    *personaDocument `yaml:"remit"`
	Triggers *Triggers        `yaml:"triggers"`
	// Disabled removes an inherited agent. It is explicit so a project never
	// loses an agent by accidentally omitting it.
	Disabled *bool `yaml:"disabled"`
}

// overridesFields reports whether an agent entry supplies anything besides
// disabled, which is how a contradictory "remove it and also configure it"
// entry is detected.
func (d agentDocument) overridesFields() bool {
	return d.Role != nil || d.Backend != nil || d.Model != nil || d.ModelVersion != nil || d.Effort != nil || d.Account != nil ||
		d.Instances != nil || d.Persona != nil || d.Failover != nil || d.Conversations != nil ||
		d.Lane != nil || d.Remit != nil || d.Triggers != nil
}

type personaDocument struct {
	Version *string `yaml:"version"`
	Path    *string `yaml:"path"`
}

type failoverDocument struct {
	Effort  *string `yaml:"effort"`
	Enabled *bool   `yaml:"enabled"`
	Model   *string `yaml:"model"`
	// Provider and Account say where the alternate is served. Both are optional
	// and both default to this agent's own, so a block that names only a model is
	// the same failover it always was — one alternate model on the provider the
	// agent already runs on.
	Provider *domain.Backend `yaml:"provider"`
	Account  *string         `yaml:"account"`
}

func decodeDocument(reader io.Reader) (configDocument, error) {
	// The source is held rather than streamed so a file that fails the strict
	// decode can be read a second time to tell an unknown key from a retired one.
	source, err := io.ReadAll(reader)
	if err != nil {
		return configDocument{}, fmt.Errorf("decode config: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)

	var document configDocument
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return configDocument{}, errors.New("decode config: configuration is empty")
		}
		if refusal := misplacedStateRoot(source); refusal != "" {
			return configDocument{}, errors.New(refusal)
		}
		if migration := retiredSlackOperators(source); migration != "" {
			return configDocument{}, errors.New(migration)
		}
		if refusal := unknownServices(source); refusal != "" {
			return configDocument{}, errors.New(refusal)
		}
		return configDocument{}, fmt.Errorf("decode config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return configDocument{}, errors.New("decode config: multiple YAML documents are not supported")
		}
		return configDocument{}, fmt.Errorf("decode config: %w", err)
	}
	return document, nil
}

// retiredSlackOperators reports the migration a file still carrying the old
// allow-list needs, and the empty string for a file that failed to decode for
// any other reason. The decoder's own answer for the key is "field operators not
// found", which is true and useless: somebody who wrote that key wrote a
// decision about who may steer the harness, so the refusal says where that
// decision lives now rather than leaving them to find it.
//
// It runs only after the strict decode has already failed, and it is lenient
// about everything else in the file, because the only question left is whether
// the reason is worth a better answer.
func retiredSlackOperators(source []byte) string {
	var retired struct {
		Slack *struct {
			Operators *[]string `yaml:"operators"`
		} `yaml:"slack"`
	}
	if err := yaml.Unmarshal(source, &retired); err != nil {
		return ""
	}
	if retired.Slack == nil || retired.Slack.Operators == nil {
		return ""
	}
	return fmt.Sprintf(`decode config: slack.operators has moved to the top-level operators mapping, `+
		`where each human binds their identifier namespaces and the grants attach to the human rather than to any one of them. `+
		`The Slack allow-list is derived from it now — the humans granted %q who bound a slack_member_id — so delete the key and write, beside "slack":

operators:
  <your-name>:
    slack_member_id: %s
    grants:
      - %s
`, GrantDirectWork, exampleSlackMemberID, GrantDirectWork)
}

// MachineFileName is the machine's own settings file, `~/.yoyodyne/machine.yaml`.
// The state root is set there and nowhere else.
const MachineFileName = home.MachineFileName

// misplacedStateRoot reports the refusal for a project file that sets the state
// root, and the empty string for a file that failed to decode for any other
// reason. Where the harness keeps its state is a fact about one machine, and a
// project file is committed and read on every machine that checks it out, so the
// key is refused by name and the refusal says where it belongs.
//
// It runs only after the strict decode has already failed, and it is lenient
// about everything else in the file, for the reason retiredSlackOperators is.
func misplacedStateRoot(source []byte) string {
	var lenient map[string]any
	if err := yaml.Unmarshal(source, &lenient); err != nil {
		return ""
	}
	key := ""
	execution, _ := lenient["execution"].(map[string]any)
	if _, set := lenient["state_root"]; set {
		key = "state_root"
	} else if _, set := execution["state_root"]; set {
		key = "execution.state_root"
	} else {
		return ""
	}
	return fmt.Sprintf("decode config: %s is not a project setting: where the harness keeps its state describes one machine, "+
		"and a project configuration is committed and read on every machine that checks it out. "+
		"Delete it here and set state_root in ~/%s/%s, "+
		"or export %s for one shell", key, home.DirectoryName, MachineFileName, home.StateHomeVariable)
}

// exampleSlackMemberID is the shape of a member id, for a refusal that shows the
// entry to write rather than describing it.
const exampleSlackMemberID = "U0123456789"

// unknownServices reports the refusal for a file whose services section names
// something that is not a service, and the empty string for a file that failed
// to decode for any other reason. The decoder's own answer is "field x not
// found in type config.servicesDocument", which is true and names a Go type
// rather than the four things the section may hold; somebody who wrote a fifth
// was either mistyping one of them or expecting a part the product does not
// have, and either way the four are what they need to see.
//
// It runs only after the strict decode has already failed, and it is lenient
// about everything else in the file, for the reason retiredSlackOperators is.
func unknownServices(source []byte) string {
	var lenient struct {
		Services map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(source, &lenient); err != nil || lenient.Services == nil {
		return ""
	}
	var unknown []string
	for name := range lenient.Services {
		if !knownService(name) {
			unknown = append(unknown, fmt.Sprintf("%q", name))
		}
	}
	if len(unknown) == 0 {
		return ""
	}
	sort.Strings(unknown)
	return fmt.Sprintf("decode config: services names %s, which is not a service the product has; the services are %s",
		strings.Join(unknown, " and "), describeServiceNames())
}
