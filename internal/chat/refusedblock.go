package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/toolcatalog"
)

// The kinds of block a reply can lose alone, named the way the operator and the
// role read them. A tracker block is not among them: an unreadable one still ends
// the message (see TrackerError).
const (
	blockProposal       = "proposal"
	blockConcern        = "concern"
	blockResearch       = "research"
	blockEvaluation     = "evaluation"
	blockRepositoryRead = "repository read"
	blockLogRead        = "log read"
	blockRunRead        = "run read"
	blockHealthRead     = "health read"
	blockRecovery       = "recovery"
	blockAsk            = "question to another role"
	blockMemory         = "memory"
	blockRestart        = "restart request"
	blockPostMortem     = "post-mortem"
	blockPassRequest    = "pass request"
	blockHandStep       = "hand step"
	blockDocument       = "document"
)

// refusedBlock is one block of a reply that was not carried out while the rest
// of the reply was: the harness could not read it, or what it asked failed a
// check made before anything is recorded. Err is the typed error the whole
// message used to end with.
type refusedBlock struct {
	// Kind is the plain-words name of the block, one of the constants above.
	Kind string
	// Block is the fence name the tool catalog knows the block by.
	Block string
	Err   error
}

// fenceName is a fence's block name: the fence without its backticks.
func fenceName(fence string) string { return strings.TrimPrefix(fence, "```") }

// blockRefusal is the refusal for a role that holds no authority for the kind of
// block it sent. authorize returns it for a readable block of the kind, and
// refusedBlockAuthority for an unreadable one, so the two are refused in the
// same words.
func blockRefusal(authority Authority, kind string) *AuthorityError {
	refused := func(what, reason string) *AuthorityError {
		return &AuthorityError{Role: authority.Role, Refused: what, Reason: reason}
	}
	switch kind {
	case blockProposal:
		return refused("work items to be proposed", "proposing work the operator approves belongs to the Lead Product Manager, and this role says what it found in prose instead")
	case blockConcern:
		return refused("a concern to be put to the operator", "stopping work over a goal belongs to the Lead Product Manager, and this role raises what it found in prose instead")
	case blockResearch:
		return refused("evidence to be gathered from outside the repository", "research is the Lead Product Manager's, and this role reasons over the evidence it was given")
	case blockEvaluation:
		return refused("an evaluation to be recorded", "judging an idea the operator brought is the Lead Product Manager's, and this role says what it thinks in prose instead")
	case blockRepositoryRead:
		return refused("a repository path to be read", "reading the repository by path is the management roles' — the Lead Product Manager, the architect, and the development manager — and this role reasons over the evidence it was given")
	case blockLogRead:
		return refused("an operational record to be read", "this role holds no log.read grant")
	case blockRunRead:
		return refused("a run to be inspected", "this role holds neither the run.inspect nor the run.verify grant")
	case blockHealthRead:
		return refused("the factory's health to be observed", "this role holds neither the health.observe nor the health.follow grant")
	case blockRecovery:
		return refused("a recovery to be carried out on a run", "this role holds no run.recover grant; carrying out recovery decisions is the development manager's")
	case blockAsk:
		return refused("a question put to another role", "the ask channel is between the roles that hold judgement about the product, and this role says what it needs in prose instead")
	case blockMemory:
		return refused("a memory to be recorded", "a memory of one's own is kept by the management roles — the Lead Product Manager, the architect, and the development manager — and this role's judgement is exercised inside runs, which remember nothing between invocations")
	case blockRestart:
		return refused("a part of the product to be restarted", "asking the supervisor to restart a part is the program manager's, and this role says what it found in prose instead")
	case blockPostMortem:
		return refused("stopped runs to be counted against their causes", "the post-mortem of stopped runs is the program manager's, and this role says what it found in prose instead")
	case blockPassRequest:
		return refused("a program manager instance to be asked for a pass", "asking a program manager for a pass is the operator's and the Lead Product Manager's, and this role asks the Lead Product Manager for one instead")
	case blockHandStep:
		return refused("a hand step to be recorded", "writing down a hand step the operator took outside the harness is the program manager's, and this role says what it noticed in prose instead")
	case blockDocument:
		return refused("a document to be written", "this role holds no grant to write a document")
	}
	return refused("a block of a kind the harness does not know", "")
}

// refusedBlockAuthority is the authority check for a block that could not be
// read: nil where the role may send blocks of the kind at all, and the refusal
// of the whole reply where it may not. Which blocks the role may send is the
// same question authorize asks of a readable one.
func (s *Session) refusedBlockAuthority(authority Authority, kind string) error {
	var held bool
	switch kind {
	case blockProposal:
		held = authority.Proposals
	case blockConcern:
		held = authority.Concerns
	case blockResearch:
		held = authority.Research
	case blockEvaluation:
		held = authority.Evaluations
	case blockRepositoryRead:
		held = authority.RepositoryReads
	case blockLogRead:
		held = authority.LogReads
	case blockRunRead:
		held = authority.RunInspects || authority.RunVerifies
	case blockHealthRead:
		held = authority.HealthObserves || authority.HealthFollows
	case blockRecovery:
		held = authority.RunRecovers
	case blockAsk:
		held = authority.Asks
	case blockMemory:
		held = authority.Memory
	case blockRestart:
		held = authority.RestartRequests
	case blockPostMortem:
		held = authority.StopCauses
	case blockPassRequest:
		held = authority.PassRequests
	case blockHandStep:
		held = authority.Interventions
	case blockDocument:
		for _, tool := range s.grantedTools() {
			if tool.Tool != nil && (tool.Tool.ID == capability.ArtifactProductMutate || tool.Tool.ID == capability.ArtifactDesignMutate) {
				held = true
			}
		}
	default:
		held = true
	}
	if held {
		return nil
	}
	return blockRefusal(authority, kind)
}

// refuseBlock audits one block as a refused tool call, under the first tool the
// catalog registers for the block, without recording any of its untrusted text.
// Only a failure to write the audit comes back: the refusal itself is expected,
// and a failed audit ends the message as it does everywhere else.
func (s *Session) refuseBlock(block string) error {
	id := capability.Capability("")
	for _, registered := range toolcatalog.Registry().Actions() {
		if registered.Tool.Block == block {
			id = registered.Tool.ID
			break
		}
	}
	// A hand step is filed under the report grant but has a block of its own.
	if id == "" && block == fenceName(interventionFence) {
		id = capability.ReportFile
	}
	if id == "" {
		return nil
	}
	_, err := auditTool(context.Background(), s, id, map[string]any{"block": block}, 1, func() (struct{}, error) { return struct{}{}, errors.New("block refused before invocation") }, nil)
	if err != nil && strings.Contains(err.Error(), "audit failed") {
		return err
	}
	return nil
}

// settleRefusedBlocks makes the loss of each refused block visible: the audit,
// the conversation event, the reply's sentence, and the words the role is given
// back, which it returns for the caller to deliver with the round's results.
func (s *Session) settleRefusedBlocks(reply *Reply, refused []refusedBlock) (string, error) {
	var owed strings.Builder
	for _, block := range refused {
		if err := s.refuseBlock(block.Block); err != nil {
			return owed.String(), err
		}
		problem := block.Err.Error()
		if err := s.emit(execution.EventReplyBlockRefused, map[string]any{
			"turn":    s.state.Turns,
			"role":    string(s.state.Role),
			"block":   block.Kind,
			"problem": singleLine(problem, maxTrackerFailureBytes),
		}); err != nil {
			return owed.String(), fmt.Errorf("record the refused %s block: %w", block.Kind, err)
		}
		reply.RefusedBlocks = append(reply.RefusedBlocks,
			fmt.Sprintf("the %s's %s block was refused and nothing in it was carried out: %s", RoleTitle(s.state.Role), block.Kind, singleLine(problem, runstate.MaxTrackerRefusalProblemBytes)))
		owed.WriteString(renderRefusedBlock(block))
	}
	return owed.String(), nil
}

// renderRefusedBlock is what the role is told about a block of its reply that
// was refused. The refusal is the harness's own words, bounded as a refused
// tracker block's are, because what is wrong with the block is what the role
// needs to send it again.
func renderRefusedBlock(block refusedBlock) string {
	return fmt.Sprintf("# A block in your last reply was refused\n\n"+
		"Your %s block (%s) was refused: %s\n\n"+
		"Nothing in that block happened, so do not describe it as done. Everything else in your reply was carried out. "+
		"If you still want what that block asked for, send the block again, corrected.\n\n",
		block.Kind, block.Block, singleLine(block.Err.Error(), runstate.MaxTrackerRefusalProblemBytes))
}
