package orchestrator

import (
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// invocationRecorder is where the developer invocation about to be made keeps
// what it is sent: the run's own recorder, at the commit its worktree is at. It
// is nil for a pipeline wired with no record of invocations, which sends
// exactly what it always did and keeps nothing.
func (a *activeRun) invocationRecorder() backend.InvocationRecorder {
	if a.pipeline.Invocations == nil {
		return nil
	}
	return a.pipeline.Invocations.Recorder(a.state.RunID, domain.RoleDeveloper, a.worktreeCommit(), a.pipeline.clock().Now)
}

// worktreeCommit is the commit the run's worktree is at as far as the harness
// has recorded it.
func (a *activeRun) worktreeCommit() string {
	if a.worktree.HarnessCommit != "" {
		return a.worktree.HarnessCommit
	}
	return a.worktree.BaseCommit
}

// developerPromptParts names the pieces of a developer prompt the harness
// builds from sources it can point to, so that what many invocations share is
// kept once: the contract the harness wrote, the persona the project
// configures, the architectural invariants, and the work item as it was read.
// Whatever is left of the prompt, such as findings a repair is handed, a
// refusal of the developer's own earlier proposal, or the install steps, is
// named as what this invocation adds.
//
// The prompt is built by the prompt builders and stays exactly what they made:
// each piece is looked for in it, in the order the builders place them, and a
// piece not found is simply not named, so the parts always join to the prompt.
func (a *activeRun) developerPromptParts(prompt string) []backend.SentPart {
	p := a.pipeline
	developer := p.developer()
	checks := p.Config.Checks
	invariants := deliveredInvariantSection(a.deliveredInvariants().Text())
	personaSource := backend.OriginRepositoryFile(developer.Persona.Path, "")
	if developer.Persona.Version != "" {
		personaSource += ", persona version " + developer.Persona.Version
	}
	known := []backend.SentPart{
		{Name: "developer contract", Origin: backend.OriginHarnessBuild(), Text: developerContract(a.scratch, checks, p.reviewDiffLimits())},
		{Name: "persona", Origin: personaSource, Text: personaSection(developer.Persona.Text)},
		{Name: "architectural invariants", Origin: backend.OriginRepositoryFile("docs/decisions/invariants", a.worktreeCommit()), Text: invariants},
		{Name: "work item and its notes", Origin: backend.OriginTrackerItem(a.item.ID, a.state.StartedAt), Text: liftSection(a.state) + a.context},
	}
	return splitByKnownParts(prompt, known, backend.SentPart{Name: "what this invocation adds (findings, refusals, install steps)", Origin: backend.OriginHarnessBuild()})
}

// splitByKnownParts cuts text into the known parts found in it, in order, and
// the pieces between them as rest. A part that is empty or is not found after
// the previous one is left out, and the pieces always join to text.
func splitByKnownParts(text string, known []backend.SentPart, rest backend.SentPart) []backend.SentPart {
	var parts []backend.SentPart
	cursor := 0
	add := func(part backend.SentPart, from, to int) {
		if to > from {
			part.Text = text[from:to]
			parts = append(parts, part)
		}
	}
	for _, part := range known {
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		at := strings.Index(text[cursor:], part.Text)
		if at < 0 {
			continue
		}
		add(rest, cursor, cursor+at)
		add(part, cursor+at, cursor+at+len(part.Text))
		cursor += at + len(part.Text)
	}
	add(rest, cursor, len(text))
	return parts
}

// developerPromptPartsIfRecorded names the prompt's parts only where something
// will record them.
func (a *activeRun) developerPromptPartsIfRecorded(prompt string) []backend.SentPart {
	if a.pipeline.Invocations == nil {
		return nil
	}
	return a.developerPromptParts(prompt)
}
