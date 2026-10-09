package publish

// What a forge's own merge queue can be relied on for. The design is
// docs/designs/integration-through-a-merge-queue.md, "Forge-native queue": the
// harness may hand a change to the forge's queue only where the forge both
// tells the harness, and itself enforces before landing, everything the
// harness's own queue gates on. This file is the forge's half of that question
// — what each adapter can say — and internal/queuemode is the half that
// decides from it. Nothing here enqueues, merges, or moves anything.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// QueueRequirement is one thing a forge's merge queue has to establish and
// enforce before a change can be handed to it instead of the harness's queue.
type QueueRequirement string

const (
	QueueEntryIdentity       QueueRequirement = "entry-and-group-identity"
	QueueTargetBase          QueueRequirement = "target-base"
	QueueContributingHeads   QueueRequirement = "contributing-heads"
	QueueCandidateCommit     QueueRequirement = "candidate-commit"
	QueueRequiredChecks      QueueRequirement = "required-checks"
	QueueIndependentApproval QueueRequirement = "independent-approval"
	QueueTimeoutPolicy       QueueRequirement = "timeout-policy"
	QueueLandingIdentity     QueueRequirement = "landing-identity"
)

// QueueRequirements is every requirement, in the order the design lists them.
// A forge's queue that falls short of any one of them is not used.
func QueueRequirements() []QueueRequirement {
	return []QueueRequirement{
		QueueEntryIdentity, QueueTargetBase, QueueContributingHeads, QueueCandidateCommit,
		QueueRequiredChecks, QueueIndependentApproval, QueueTimeoutPolicy, QueueLandingIdentity,
	}
}

// Says is the requirement in ordinary words, as an explanation names it.
func (r QueueRequirement) Says() string {
	switch r {
	case QueueEntryIdentity:
		return "which queue entry a change is and which group of changes it was tested with"
	case QueueTargetBase:
		return "the exact target commit each group was built on"
	case QueueContributingHeads:
		return "the changes in each group, in order"
	case QueueCandidateCommit:
		return "the combined commit each group tests and lands"
	case QueueRequiredChecks:
		return "that the project's configured checks ran on that combined commit"
	case QueueIndependentApproval:
		return "that an independent reviewer approved that combined commit"
	case QueueTimeoutPolicy:
		return "how long the queue waits for checks before it drops a group"
	case QueueLandingIdentity:
		return "which commit landed each change"
	}
	return string(r)
}

// EvidenceBinding says which commit a piece of check or review evidence is
// about. Only evidence about the combined commit a queue lands says anything
// about what landed; evidence about a pull request's own head does not.
type EvidenceBinding string

const (
	BoundToCandidate       EvidenceBinding = "candidate"
	BoundToPullRequestHead EvidenceBinding = "pull-request-head"
)

// QueueRequirementEvidence is what an adapter found about one requirement.
// Established is that the adapter can read it for every entry; Enforced is
// that the forge refuses to land a change without it. Missing says, in
// ordinary words, why either is false.
type QueueRequirementEvidence struct {
	Requirement QueueRequirement
	Established bool
	Enforced    bool
	// BoundTo is which commit check and review evidence is about, and is only
	// read for those two requirements.
	BoundTo EvidenceBinding
	// Configuration identifies the check configuration the forge enforces, and
	// is only read for the required checks: checks run from another
	// configuration are not the project's checks.
	Configuration string
	Missing       string
}

// QueueCapabilities is what one forge adapter observed about one target
// branch: how it is protected, whether the forge has its own merge queue for
// it, and how much of the harness's gate that queue establishes.
type QueueCapabilities struct {
	// Forge names the adapter, such as "github".
	Forge        string
	TargetBranch string
	ObservedAt   time.Time
	Protected    bool
	// ProtectedBy names which of the forge's mechanisms protects the branch.
	ProtectedBy string
	// QueueAvailable is the forge having its own merge queue for the branch.
	QueueAvailable bool
	// QueueRequired is the branch's protection landing changes only through
	// the forge's queue, so nothing — the harness's own pull requests included
	// — lands any other way.
	QueueRequired bool
	// TimeoutMinutes is how long the forge's queue actually waits for checks,
	// as the forge reported it, and zero where it did not.
	TimeoutMinutes int
	// Requirements is what the adapter found about each requirement. A
	// requirement it does not list is one it established nothing about.
	Requirements []QueueRequirementEvidence
}

// QueueCapabilityReader is a forge adapter that can say what its merge queue
// establishes for a target branch. A question the forge did not answer is an
// error, never a reading: "no queue" and "not protected" are both answers a
// caller would act on.
type QueueCapabilityReader interface {
	QueueCapabilities(ctx context.Context, branch string) (QueueCapabilities, error)
}

var _ QueueCapabilityReader = GitHub{}

// mergeQueueConfigurationQuery asks for the forge's merge queue on one branch
// and its timeout. The forge answers a null queue for a branch it has none on;
// the branch is always named, because without it the forge answers about the
// default branch instead.
const mergeQueueConfigurationQuery = `query($owner: String!, $name: String!, $branch: String!) {
  repository(owner: $owner, name: $name) { mergeQueue(branch: $branch) { id configuration { checkResponseTimeout } } }
}`

// QueueCapabilities reads what GitHub's merge queue establishes for a branch.
//
// GitHub tells the harness, for each queue entry, its identity, the base it
// was built on, the pull request heads in order, and the commit that merged
// it, and it builds every group on the base it names. It does not establish
// the rest of the gate, whatever a repository configures:
//
//   - it names no group: which entries were tested together, and the combined
//     commit it built for them, arrive only in the merge_group event sent to
//     webhooks, which the harness does not receive;
//   - it requires checks by name, and nothing binds those names to the checks
//     a project configures in Yoyodyne or to their configuration;
//   - its required reviews approve a pull request's head, never the combined
//     commit the queue lands.
//
// So a GitHub queue never qualifies today, and a branch it protects is one the
// harness's own queue cannot land into either (internal/queuemode says why).
// The timeout is read from the queue's own configuration rather than assumed.
func (g GitHub) QueueCapabilities(ctx context.Context, branch string) (QueueCapabilities, error) {
	if err := validateArgument("branch", branch); err != nil {
		return QueueCapabilities{}, err
	}
	protection, err := g.Protection(ctx, branch)
	if err != nil {
		return QueueCapabilities{}, err
	}
	stdout, err := g.graphql(ctx, fmt.Sprintf("ask the forge whether %s has a merge queue", branch),
		"-f", "query="+mergeQueueConfigurationQuery,
		"-F", "owner={owner}",
		"-F", "name={repo}",
		"-f", "branch="+branch)
	if err != nil {
		return QueueCapabilities{}, err
	}
	var answered struct {
		Data struct {
			Repository *struct {
				MergeQueue *struct {
					ID            string `json:"id"`
					Configuration *struct {
						CheckResponseTimeout *int `json:"checkResponseTimeout"`
					} `json:"configuration"`
				} `json:"mergeQueue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &answered); err != nil {
		return QueueCapabilities{}, fmt.Errorf("decode whether %s has a merge queue: %w", branch, err)
	}
	if answered.Data.Repository == nil {
		return QueueCapabilities{}, fmt.Errorf("the forge's answer about the merge queue for %s names no repository", branch)
	}
	capabilities := QueueCapabilities{
		Forge: "github", TargetBranch: branch, ObservedAt: time.Now().UTC(),
		Protected: protection.Protected, ProtectedBy: protection.By,
	}
	queue := answered.Data.Repository.MergeQueue
	if queue == nil {
		return capabilities, nil
	}
	// A branch with a merge queue lands every change through it: the forge
	// creates one only where the branch's rules require it.
	capabilities.QueueAvailable, capabilities.QueueRequired = true, true
	timeout := QueueRequirementEvidence{Requirement: QueueTimeoutPolicy,
		Missing: fmt.Sprintf("the forge's merge queue for %s did not say how long it waits for checks", branch)}
	if queue.Configuration != nil && queue.Configuration.CheckResponseTimeout != nil && *queue.Configuration.CheckResponseTimeout > 0 {
		capabilities.TimeoutMinutes = *queue.Configuration.CheckResponseTimeout
		timeout = QueueRequirementEvidence{Requirement: QueueTimeoutPolicy, Established: true, Enforced: true}
	}
	capabilities.Requirements = []QueueRequirementEvidence{
		{Requirement: QueueEntryIdentity, Enforced: true,
			Missing: "GitHub names each queue entry but not which entries it tested together"},
		{Requirement: QueueTargetBase, Established: true, Enforced: true},
		{Requirement: QueueContributingHeads, Established: true, Enforced: true},
		{Requirement: QueueCandidateCommit, Enforced: true,
			Missing: "GitHub names the combined commit it builds only to webhooks, which the harness does not receive"},
		{Requirement: QueueRequiredChecks, Enforced: true, BoundTo: BoundToCandidate,
			Missing: "GitHub requires checks by name, and nothing binds them to the project's configured checks"},
		{Requirement: QueueIndependentApproval, BoundTo: BoundToPullRequestHead,
			Missing: "GitHub's required reviews approve the pull request's head, not the combined commit it lands"},
		timeout,
		{Requirement: QueueLandingIdentity, Established: true, Enforced: true},
	}
	return capabilities, nil
}
