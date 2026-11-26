// Package epiccheck makes the check of the whole epic for one naming: it asks the
// one decision, reserves the call in the epic's progress before it is made, and
// keeps the answer the reviewer session gave for each thing the description asks.
// docs/designs/epic-mode-second-part.md states the rule, under piece 10.
//
// The reviewer session itself, what it is given and when it is started are built
// by the next piece; Judge is where they go.
package epiccheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/epicprogress"
	"github.com/mason-bryant/yoyodyne/internal/maystart"
)

// Progress is what is written. It is satisfied by the store of internal/epicprogress.
type Progress interface {
	CallWhileOpen(naming epicprogress.Naming, id, purpose string, lookUp func() (string, epicprogress.Looked, error), send func() (string, error)) (string, error)
	Checked(naming epicprogress.Naming, check epicprogress.Check) (epicprogress.Check, error)
}

// Request is one check, for the session that makes it.
type Request struct {
	ID     string
	Naming epicprogress.Naming
	Commit string
}

// Checker makes checks of the whole epic.
type Checker struct {
	Progress Progress
	// MayStart asks the one decision on what may start (internal/maystart).
	MayStart func(maystart.Start) maystart.Decision
	// LookUp finds what became of a check that was reserved and has no result kept:
	// its recorded answer, that it was never sent, or that nothing can tell.
	LookUp func(id string) (string, epicprogress.Looked, error)
	// Judge makes the call in a reviewer session of its own and answers what the
	// session recorded: {"findings": [...]}, one finding for each thing asked.
	Judge func(ctx context.Context, request Request) (string, error)
}

// recorded is a session's answer, as it is kept with the reservation.
type recorded struct {
	Findings []epicprogress.Finding `json:"findings"`
}

// Check makes the check with the identifier id of the naming, reading commit. Where
// the decision does not let it start, nothing is called or kept, and the decision
// says why. A check already made under id is answered from what was kept, and one
// reserved and not answered is looked up before anything is sent again.
func (c Checker) Check(ctx context.Context, naming epicprogress.Naming, id, commit string) (epicprogress.Check, maystart.Decision, error) {
	decision := c.MayStart(maystart.CheckOfTheEpic(naming))
	if !decision.MayStart() {
		return epicprogress.Check{}, decision, nil
	}
	if c.Judge == nil || c.LookUp == nil {
		return epicprogress.Check{}, decision, errors.New("no reviewer session is given to make the check of the whole epic")
	}
	says, err := c.Progress.CallWhileOpen(naming, id, fmt.Sprintf("the check of the whole epic %s at commit %s", naming.WorkItemID, commit),
		func() (string, epicprogress.Looked, error) { return c.LookUp(id) },
		func() (string, error) { return c.Judge(ctx, Request{ID: id, Naming: naming, Commit: commit}) })
	if err != nil {
		return epicprogress.Check{}, decision, err
	}
	var answer recorded
	decoder := json.NewDecoder(bytes.NewReader([]byte(says)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil {
		return epicprogress.Check{}, decision, fmt.Errorf("read the recorded answer of check %s: %w", id, err)
	}
	check, err := c.Progress.Checked(naming, epicprogress.Check{ID: id, Commit: commit, Findings: answer.Findings})
	return check, decision, err
}
