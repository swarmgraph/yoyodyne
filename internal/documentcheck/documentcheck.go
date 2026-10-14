// Package documentcheck reads one document a role wrote from its conversation
// for what this repository's own document checks would refuse in it, before
// the harness records it.
//
// Those checks run under `make test` in the run that lands the document, so a
// word the coined-words check refuses or a link that leads nowhere used to be
// found there, after the document had been confirmed and a run had been spent
// on it, and handed back to its author a turn or more later. Every one of those
// findings was already decidable from the document and the repository it is
// written into. So the same rules are applied when the document is written, and
// what fails one is refused straight back to the role in the turn that wrote
// it, naming each word or link and the line it is on. They are applied once
// more when the harness confirms a waiting document, which may have been
// written by a build without them (internal/chat, confirmDocument).
//
// It is the same check rather than a second one: the terms and doclink packages
// read the document exactly as they read it once it is filed. The landing run
// still runs the project's checks, which judge the whole repository and are
// the ones that decide whether the document lands.
package documentcheck

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/doclink"
	"github.com/mason-bryant/yoyodyne/internal/terms"
)

// The rules a problem is reported under, named as the role is told them.
const (
	RuleCoinedWords = "coined words"
	RuleLinks       = "links"
)

// Problem is one thing in the document a rule refuses, on the line of the body
// it is written on.
type Problem struct {
	Line   int
	Rule   string
	Reason string
}

// Refusal is a document refused for what the document checks found in it.
type Refusal struct {
	// Path is where the document would have been filed, repository-relative.
	Path     string
	Problems []Problem
}

func (r *Refusal) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "the document for %s fails the checks the run landing it would run, so it was not recorded; correct each line below and write the document again. Lines are counted in the body you wrote:", r.Path)
	for _, problem := range r.Problems {
		fmt.Fprintf(&out, "\n- line %d, %s: %s", problem.Line, problem.Rule, problem.Reason)
	}
	return out.String()
}

// Check reads body as the document filed at documentPath in the repository at
// root, and returns a *Refusal naming every problem found, or nil when there is
// none. Any other error is a check that could not be run.
//
// A repository that keeps no register of coined words is not checked for them,
// because nothing there says which words are coined; its links are still
// resolved.
func Check(root, documentPath, body string) error {
	var problems []Problem
	coined, err := terms.CheckDocument(root, documentPath, body)
	if err != nil && !errors.Is(err, terms.ErrNoRegister) {
		return fmt.Errorf("check %s for coined words: %w", documentPath, err)
	}
	for _, problem := range coined {
		problems = append(problems, Problem{Line: problem.Line, Rule: RuleCoinedWords, Reason: problem.Reason})
	}
	links, err := doclink.CheckDocument(root, documentPath, body)
	if err != nil {
		return fmt.Errorf("check %s for links: %w", documentPath, err)
	}
	for _, problem := range links {
		problems = append(problems, Problem{Line: problem.Line, Rule: RuleLinks, Reason: problem.Reason})
	}
	if len(problems) == 0 {
		return nil
	}
	// In the order the body is written, whichever rule found each.
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Line < problems[j].Line })
	return &Refusal{Path: documentPath, Problems: problems}
}
