package chat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/contextbundle"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// standingGoalsLine is in the goals document only, so finding it in a request
// is finding the standing goals carried.
const standingGoalsLine = "- the plain-language goal stands over every role's output."

// assembledBriefing writes a small product into a repository and assembles its
// briefing the way a conversation is opened with one: the goals carrying the
// standing goals, a brief, a design, and two shipped documents, sized so the
// documents are what make it large.
func assembledBriefing(t *testing.T, design, guide, readme int) string {
	t.Helper()
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	filler := func(word string, size int) string {
		line := strings.Repeat(word+" ", 15) + "\n"
		return strings.Repeat(line, size/len(line)+1)
	}
	write("docs/product/brief.md", "---\nid: brief\nkind: brief\n---\n\n# Product brief\n\nThe product turns intent into software.\n")
	write("docs/product/goals/v1-goals.md", "---\nid: v1-goals\nkind: goals\n---\n\n# V1 goals\n\nWhat the first version reaches.\n\n## Goals\n\n- Intent goes in and software comes out.\n\n## Standing goals\n\n"+standingGoalsLine+"\n")
	write("docs/designs/large-design.md", "# Large design\n\n"+filler("design", design))
	write("docs/guide.md", "# Guide\n\n"+filler("guide", guide))
	write("README.md", "# Readme\n\n"+filler("readme", readme))
	bundle, err := contextbundle.AssembleProduct(contextbundle.ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		ShippedDocumentation:    []string{"README.md", "docs/guide.md"},
		RoleDocuments:           []contextbundle.DocumentSet{{Label: "Design", Directory: "docs/designs"}},
		WorkItems:               []beads.WorkItem{{ID: "yoyodyne-ifd.7", Title: "Current work stays in the briefing", Status: "open", Priority: 1}},
		CommandHelp:             "yoyo chat   talk to a role\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	return bundle.Text
}

func briefingFitOptions(t *testing.T, provider *sizedConversationBackend, briefing string) Options {
	t.Helper()
	options := testOptions(t, provider)
	options.Role, options.Agent = domain.RoleDevelopmentManager, string(domain.RoleDevelopmentManager)
	options.Provider, options.Model = domain.BackendCodex, "gpt-6.1-sol"
	options.Briefing.Text = briefing
	return options
}

func TestBriefingTooLargeForCodexIsFittedAndTheTurnCarriesOn(t *testing.T) {
	t.Parallel()
	// Larger than the Codex endpoint's 1,048,576-character input on its own,
	// and well inside the 2.5 MiB the product briefing is allowed.
	briefing := assembledBriefing(t, 300<<10, 600<<10, 200<<10)
	if len(briefing) <= 1<<20 || len(briefing) > contextbundle.MaxProductBytes {
		t.Fatalf("fixture briefing is %d bytes", len(briefing))
	}
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{{SessionID: "first", FinalText: "Answered from the fitted briefing."}}}}
	options := briefingFitOptions(t, provider, briefing)
	session := openTestSession(t, options)
	message := "Decide what the stopped work needs." + strings.Repeat(" evidence", 2<<10)
	reply, err := session.Send(context.Background(), message)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if reply.Text != "Answered from the fitted briefing." || len(provider.requests) != 1 {
		t.Fatalf("reply %q after %d request(s)", reply.Text, len(provider.requests))
	}
	sent := provider.requests[0]
	size, limit := provider.RequestSize(sent)
	if limit != 1<<20 || size > limit-limit/20 {
		t.Fatalf("sent %d bytes against Codex's %d", size, limit)
	}
	if session.state.Turns != 1 {
		t.Fatalf("turn was not recorded: %d turns", session.state.Turns)
	}

	// The role is told, in the briefing it reads, what gave way and how to read
	// it, and the longest least current section went first.
	for _, want := range []string{
		contextbundle.FittedHeading,
		"- docs/guide.md (shipped documentation, ",
		"name that path in a repository read",
		"say that you have not read it",
		"### Shipped documentation: docs/guide.md\n",
		"[Shortened to fit the provider serving this turn: only the first ",
	} {
		if !strings.Contains(sent.Prompt, want) {
			t.Fatalf("fitted request does not say %q", want)
		}
	}
	if strings.Count(sent.Prompt, strings.Repeat("guide ", 15)) >= strings.Count(briefing, strings.Repeat("guide ", 15)) {
		t.Fatal("the longest shipped document was kept whole while the briefing was over")
	}
	if strings.Count(sent.Prompt, strings.Repeat("design ", 15)) != strings.Count(briefing, strings.Repeat("design ", 15)) ||
		strings.Count(sent.Prompt, strings.Repeat("readme ", 15)) != strings.Count(briefing, strings.Repeat("readme ", 15)) {
		t.Fatal("a shorter or more current section gave way although shortening the longest shipped document was enough")
	}

	// What the turn is about stays whole.
	for _, want := range []string{message, standingGoalsLine, "Current work stays in the briefing", "## Recorded product intent"} {
		if !strings.Contains(sent.Prompt, want) {
			t.Fatalf("fitted request lost %q", want[:min(len(want), 60)])
		}
	}

	events, err := options.Store.LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Reason        string                        `json:"reason"`
		BriefingBytes int                           `json:"briefing_bytes"`
		FittedBytes   int                           `json:"briefing_fitted_bytes"`
		Sections      []contextbundle.FittedSection `json:"briefing_sections"`
	}
	for _, event := range events {
		if event.Type == execution.EventSessionCompacted {
			if err := json.Unmarshal(event.Payload, &recorded); err != nil {
				t.Fatal(err)
			}
		}
	}
	if recorded.Reason != "request_size" || recorded.BriefingBytes != len(briefing) || recorded.FittedBytes >= recorded.BriefingBytes ||
		len(recorded.Sections) != 1 || recorded.Sections[0].Path != "docs/guide.md" || recorded.Sections[0].KeptBytes == 0 {
		t.Fatalf("shortening was not recorded: %+v", recorded)
	}
}

func TestBriefingFitNeverDropsInstructionsStandingGoalsOrTheTurn(t *testing.T) {
	t.Parallel()
	briefing := assembledBriefing(t, 60<<10, 60<<10, 60<<10)
	// Only the fixed sections, the goals and a few kilobytes of the rest fit.
	floor := contextbundle.FitProductContext(briefing, 0).Text
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{results: []backendapi.RunResult{{SessionID: "first", FinalText: "Answered."}}}}
	options := briefingFitOptions(t, provider, briefing)
	options.Persona = "Persona instruction that must arrive whole."
	session := openTestSession(t, options)
	session.state.PendingTrackerResults = "A tracker result waiting to be delivered.\n"
	message := "Current operator message."
	probe := backendapi.RunRequest{SystemPrompt: WithRemit(SystemPrompt(session.state.Role, options.Admission, session.artifactFiling(), options.Persona), session.state.Role, options.Remit), Prompt: session.renderMemories() + session.turnPrompt(message, nil)}
	fixed, _ := provider.RequestSize(probe)
	fixed -= len(briefing) - len(floor)
	provider.limit = (fixed + 2<<10) * 20 / 19
	if _, err := session.Send(context.Background(), message); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("%d requests", len(provider.requests))
	}
	sent := provider.requests[0]
	if !strings.Contains(sent.SystemPrompt, "Persona instruction that must arrive whole.") || sent.SystemPrompt != probe.SystemPrompt {
		t.Fatal("role instructions changed to fit")
	}
	for _, want := range []string{message, standingGoalsLine, "A tracker result waiting to be delivered.", "Current work stays in the briefing", "Intent goes in and software comes out."} {
		if !strings.Contains(sent.Prompt, want) {
			t.Fatalf("fitted request lost %q", want)
		}
	}
	for _, path := range []string{"docs/guide.md", "README.md", "docs/designs/large-design.md", "the command help"} {
		if !strings.Contains(sent.Prompt, "- "+path+" (") {
			t.Fatalf("%s did not give way before the protected parts", path)
		}
	}
}

func TestBriefingThatCannotFitFailsOnceNamingTheSizes(t *testing.T) {
	t.Parallel()
	briefing := assembledBriefing(t, 60<<10, 60<<10, 60<<10)
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}, limit: 8 << 10}
	session := openTestSession(t, briefingFitOptions(t, provider, briefing))
	session.ForPass("development-manager-sweep")
	_, err := session.Send(context.Background(), "Decide the docket.")
	var tooLarge *TurnTooLarge
	if !errors.As(err, &tooLarge) || !errors.Is(err, ErrTurnUnassembled) {
		t.Fatalf("error = %v", err)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("the provider was asked %d time(s)", len(provider.requests))
	}
	var adapter *backendapi.RequestTooLarge
	if !errors.As(err, &adapter) {
		t.Fatal("the refusal no longer reads as the adapter's size refusal")
	}
	floor := len(contextbundle.FitProductContext(briefing, 0).Text)
	if tooLarge.LimitBytes != 8<<10 || tooLarge.BriefingBytes != floor || tooLarge.InstructionsBytes == 0 || tooLarge.TurnBytes == 0 {
		t.Fatalf("sizes: %+v", tooLarge)
	}
	for _, want := range []string{"this turn was not sent", "8192", "the role's own instructions are", "the briefing is", "not tried again"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not say %q", err, want)
		}
	}
}

func TestRebuiltConversationDropsOldMessagesBeforeTheBriefingGivesWay(t *testing.T) {
	t.Parallel()
	briefing := assembledBriefing(t, 300<<10, 600<<10, 200<<10)
	provider := &sizedConversationBackend{speakingBackend: &speakingBackend{}}
	session := sizeTestHistory(t, briefingFitOptions(t, provider, briefing), provider.speakingBackend)
	session.state.ProviderSessionID = ""
	provider.results = append(provider.results, backendapi.RunResult{SessionID: "rebuilt", FinalText: "Carried on from the record."})
	before := len(provider.requests)
	reply, err := session.Send(context.Background(), "Current evidence stays.")
	if err != nil || reply.Text != "Carried on from the record." || len(provider.requests) != before+1 {
		t.Fatalf("Send: %q, %v, %d request(s)", reply.Text, err, len(provider.requests)-before)
	}
	sent := provider.requests[before]
	if size, limit := provider.RequestSize(sent); size > limit-limit/20 {
		t.Fatalf("sent %d bytes against %d", size, limit)
	}
	for _, want := range []string{rebuiltContextHeader, "earlier message(s) are not carried here.", contextbundle.FittedHeading, "- docs/guide.md (shipped documentation, ", standingGoalsLine, "Current evidence stays."} {
		if !strings.Contains(sent.Prompt, want) {
			t.Fatalf("rebuilt request does not carry %q", want)
		}
	}
	if strings.Contains(sent.Prompt, "old-answer-9") {
		t.Fatal("an old message was kept while the briefing gave way")
	}
}
