// Package epiccheck makes the check of the whole epic for one naming: it asks the
// one decision, gives the project's declared checks' results on the commit it
// reads, reserves the call in the epic's progress before it is made, and keeps the
// answer a reviewer session that did none of the work gave for each thing the
// description asks. docs/designs/epic-mode-second-part.md states the rule, under
// piece 10.
package epiccheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/epicscope"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Progress is what is written. It is satisfied by the store of internal/epicprogress.
type Progress interface {
	CallWhileOpen(naming epicprogress.Naming, id, purpose string, lookUp func() (string, epicprogress.Looked, error), send func() (string, error)) (string, error)
	Checked(naming epicprogress.Naming, check epicprogress.Check) (epicprogress.Check, error)
}

// Request is one check, for the session that makes it: what it is told to do, and
// what it is given.
type Request struct {
	ID     string
	Naming epicprogress.Naming
	Commit string
	System string
	Prompt string
}

// Member is one member of the epic as the session is given it. Change is the
// commit its change landed as, or the approved head handed over, and is empty for
// a member done with no change. Review is what its reviewer decided and said.
// Sessions are every developer and reviewer session of its runs.
type Member struct {
	WorkItemID string
	Title      string
	Change     string
	Review     string
	Sessions   []string
}

// Given is what one check reads. Epic is the operator's record of the naming,
// Entry what is kept of its progress, and Commits the target branch at the one
// commit recorded, or each approved head where changes were handed over. Titles
// names the items the operator took out. Again is a check made again on new
// approved heads after a change handed over was repaired (Entry.Repaired), which
// is not a round of correction.
type Given struct {
	Epic       epicscope.Epic
	Entry      epicprogress.Entry
	Members    []Member
	Commits    []string
	HandedOver bool
	Titles     map[string]string
	Again      bool
}

// Checker makes checks of the whole epic.
type Checker struct {
	Progress Progress
	// MayStart asks the one decision on what may start (internal/maystart). It is
	// never asked about the epic's budget: a check that is owed is made past it.
	MayStart func(maystart.Start) maystart.Decision
	// LookUp finds what became of a check that was reserved and has no result kept:
	// its recorded answer, that it was never sent, or that nothing can tell.
	LookUp func(id string) (string, epicprogress.Looked, error)
	// Judge makes the call in a new reviewer session that is never resumed and may
	// write nothing, and answers what was recorded (Recorded).
	Judge func(ctx context.Context, request Request) (string, error)
	// Commands is the project's configured checks, run whole. Queued is every merge
	// queue candidate that landed as the commit it was checked on, Recorded every
	// run of the scheduled check of the target branch, and RunChecks runs the commands
	// whole on one commit in a checkout of its own, under the limits every check
	// runs under.
	Commands  []string
	Queued    func() ([]Queued, error)
	Recorded  func() ([]runstate.MainCheckRun, error)
	RunChecks func(ctx context.Context, commit string, commands []string) ([]runstate.MainCheckResult, error)
}

// recorded is a session's answer, as it is kept with the reservation, and the
// session that gave it.
type recorded struct {
	Findings []epicprogress.Finding `json:"findings"`
	Session  string                 `json:"session"`
}

// Recorded is what is kept of a session's answer: the findings its final text
// gives, and the session. A text with no findings that can be read is an error,
// so nothing is kept.
func Recorded(finalText, session string) (string, error) {
	var said recorded
	from, to := strings.Index(finalText, "{"), strings.LastIndex(finalText, "}")
	if from < 0 || to < from || json.Unmarshal([]byte(finalText[from:to+1]), &said) != nil || len(said.Findings) == 0 {
		return "", errors.New("the checking session's answer could not be read")
	}
	if strings.TrimSpace(session) == "" {
		return "", errors.New("the checking session is not known")
	}
	encoded, err := json.Marshal(recorded{Findings: said.Findings, Session: session})
	return string(encoded), err
}

// Check makes the check with the identifier id of the naming. Where the decision
// does not let it start, nothing is run, called or kept, and the decision says
// why. A check already made under id is answered from what was kept, and one
// reserved and not answered is looked up before anything is sent again. An answer
// from a session that wrote or reviewed a member, or judged a piece that joined,
// is refused and nothing is kept of it.
func (c Checker) Check(ctx context.Context, naming epicprogress.Naming, id string, given Given) (epicprogress.Check, maystart.Decision, error) {
	decision := c.MayStart(maystart.CheckOfTheEpic(naming))
	if !decision.MayStart() {
		return epicprogress.Check{}, decision, nil
	}
	if c.Judge == nil || c.LookUp == nil {
		return epicprogress.Check{}, decision, errors.New("no reviewer session is given to make the check of the whole epic")
	}
	if len(given.Commits) == 0 || given.Epic.WorkItemID != naming.WorkItemID {
		return epicprogress.Check{}, decision, errors.New("the check of the whole epic needs the epic and the commit it reads")
	}
	var results []Results
	for _, commit := range given.Commits {
		found, err := c.results(ctx, naming, id, commit)
		if err != nil {
			return epicprogress.Check{}, decision, err
		}
		results = append(results, found)
	}
	commit := strings.Join(given.Commits, " ")
	says, err := c.Progress.CallWhileOpen(naming, id, fmt.Sprintf("%s %s at commit %s", epicprogress.CheckFor, naming.WorkItemID, commit),
		func() (string, epicprogress.Looked, error) { return c.LookUp(id) },
		func() (string, error) {
			return c.Judge(ctx, Request{ID: id, Naming: naming, Commit: commit, System: system, Prompt: prompt(given, results)})
		})
	if err != nil {
		return epicprogress.Check{}, decision, err
	}
	var answer recorded
	decoder := json.NewDecoder(bytes.NewReader([]byte(says)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return epicprogress.Check{}, decision, fmt.Errorf("read the recorded answer of check %s: %w", id, err)
	}
	if answer.Session == "" || slices.Contains(Worked(given), answer.Session) {
		return epicprogress.Check{}, decision, fmt.Errorf("the session %q that answered check %s is not one that did none of the epic's work, so its answer is not kept", answer.Session, id)
	}
	check, err := c.Progress.Checked(naming, epicprogress.Check{ID: id, Commit: commit, Findings: answer.Findings, Again: given.Again})
	return check, decision, err
}

// Worked is every session that wrote or reviewed a member, or judged a piece that
// may join: none of them may make the check.
func Worked(given Given) []string {
	var sessions []string
	for _, member := range given.Members {
		sessions = append(sessions, member.Sessions...)
	}
	for _, joined := range given.Entry.Joined {
		sessions = append(sessions, joined.Session)
	}
	for _, piece := range given.Entry.Pieces {
		if piece.Outcome != nil {
			sessions = append(sessions, piece.Outcome.Session)
		}
	}
	return slices.DeleteFunc(sessions, func(session string) bool { return strings.TrimSpace(session) == "" })
}
