package terms

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// register is a fixture register carrying the rows given, under the heading the
// parser reads and after a table it must not read. The decoy matters: the real
// document lists the words that were replaced rather than registered, and a
// parser that took every table in the file would permit exactly those.
func register(rows ...string) string {
	return registerReplacing([]string{"| `tranche` | stage |"}, rows...)
}

// registerReplacing is register with the replaced table's rows given too, for
// the tests about what a replaced row excuses.
func registerReplacing(replaced []string, rows ...string) string {
	return strings.Join(append(append(append([]string{
		"# Terms",
		"",
		ReplacedHeading,
		"",
		"| Term | Write instead | Still written in |",
		"| --- | --- | --- |",
	}, replaced...), []string{
		"",
		RegisterHeading,
		"",
		"| Term | In plain words | Where it is used |",
		"| --- | --- | --- |",
	}...), rows...), "\n") + "\n"
}

// root writes a fixture repository: the register, and each named document under
// the homes.
func root(t *testing.T, registerBody string, documents map[string]string) string {
	t.Helper()
	directory := t.TempDir()
	write := func(relative, body string) {
		full := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", relative, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", relative, err)
		}
	}
	write(RegisterPath, registerBody)
	for relative, body := range documents {
		write(relative, body)
	}
	return directory
}

func TestCheckReportsACoinageNoEntryDefines(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": "# One\n\nThe backend boundary is the seam it re-enters through.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 {
		t.Fatalf("Check() reported %d problems, want 1: %v", len(problems), problems)
	}
	if problems[0].Path != "docs/designs/one.md" || problems[0].Line != 3 || problems[0].Term != "seam" {
		t.Errorf("Check() reported %+v, want seam at docs/designs/one.md:3", problems[0])
	}
	// The failure has to carry the ordinary wording, because a reader told only
	// that a word is wrong has been given the finding and not the fix.
	if !strings.Contains(problems[0].Reason, "name the boundary instead") {
		t.Errorf("Check() reason = %q, want the plain wording in it", problems[0].Reason)
	}
}

func TestCheckPermitsACoinageTheRegisterDefines(t *testing.T) {
	t.Parallel()

	directory := root(t, register("| `sink` | the process that posts to Slack | `yoyo slack` |"), map[string]string{
		"docs/designs/one.md": "# One\n\nThe sink is one separate, long-running process.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing: the term is registered", problems)
	}
}

// Removing the entry is what forbids the term again, and it is a change to the
// register rather than to this package. That is the property that makes the
// register the authority: the same document, checked twice, differs only by a
// row somebody wrote.
func TestRemovingAnEntryForbidsTheTermAgain(t *testing.T) {
	t.Parallel()

	document := map[string]string{"docs/designs/one.md": "# One\n\nThe sink posts.\n"}
	permitted := root(t, register("| `sink` | the process that posts to Slack | `yoyo slack` |"), document)
	forbidden := root(t, register(), document)

	allowed, err := Check(permitted)
	if err != nil {
		t.Fatalf("Check(permitted) error = %v", err)
	}
	refused, err := Check(forbidden)
	if err != nil {
		t.Fatalf("Check(forbidden) error = %v", err)
	}
	if len(allowed) != 0 || len(refused) != 1 {
		t.Fatalf("Check() allowed %v and refused %v, want nothing then one problem", allowed, refused)
	}
}

func TestCheckReadsNeitherFrontmatterNorFencedBlocks(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": strings.Join([]string{
			"---",
			"id: one",
			"revisions:",
			"  - reason: one re-arm per publication is permitted",
			"---",
			"",
			"# One",
			"",
			"```",
			"grep tranche docs",
			"```",
			"",
			"Nothing here is coined.",
			"",
		}, "\n"),
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing: a revision's recorded reason and a code block are not prose", problems)
	}
}

// A document whose first line is a horizontal rule rather than frontmatter is
// still read whole. The alternative is a check that silently reads nothing.
func TestCheckReadsADocumentThatOpensAFrontmatterBlockAndNeverClosesIt(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": "---\n\n# One\n\nA wedged required check.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "wedged" {
		t.Fatalf("Check() reported %v, want the one coinage in the body", problems)
	}
}

func TestCheckMatchesTheStemAndNotTheWordOnly(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/product/one.md": "# One\n\nIt tolerates additive schema changes instead of starving on them.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "starving" {
		t.Fatalf("Check() reported %v, want starving", problems)
	}
}

// A term held to a whole word does not report the ordinary word its stem begins.
// `seamless` is not `seam`, and offering `seam`'s wording for it would tell an
// author to name a boundary in a sentence that has none.
func TestCheckDoesNotReportAnOrdinaryWordAStemBegins(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": "# One\n\nResuming is seamless, and it happens seamlessly.\n\nThe seam is here.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Line != 5 {
		t.Fatalf("Check() reported %v, want only the whole word on line 5", problems)
	}
}

// The hyphenated spelling of a term the register writes with a space. This one
// is not hypothetical: `minute-zero` is written in an active invariant, and it
// escaped both the ifd.206 sweep's `minute zero` regex and the first version of
// this check, which looked for the register's spelling and nothing else.
func TestCheckReportsAHyphenatedSpellingOfASpacedTerm(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/decisions/one.md": "# One\n\nEvery run begins with a minute-zero execution probe.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "minute zero" || problems[0].Line != 3 {
		t.Fatalf("Check() reported %v, want minute zero on line 3", problems)
	}
	// Named as the register spells it, because what the reader needs is the
	// wording to write rather than the respelling they used.
	if !strings.Contains(problems[0].Reason, "before development begins") {
		t.Errorf("Check() reason = %q, want the plain wording in it", problems[0].Reason)
	}
}

// The other miss the re-run found: `soak` in the wording that reached
// `configurable-workflows.md`, which the sweep's own table records as having no
// governed occurrence at all.
func TestCheckReportsTheTermTheSweepRecordedAsAbsent(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": "# One\n\nKeep an opt-in parity soak alongside the old path.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "soak" || problems[0].Line != 3 {
		t.Fatalf("Check() reported %v, want soak on line 3", problems)
	}
}

// Every spacing of a term's parts is the term: hyphenated, doubly spaced, and
// closed up into one word.
func TestCheckReportsEverySpacingOfATermsParts(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": strings.Join([]string{
			"# One", "",
			"A dropped merge request is re-armed once.", "",
			"A dropped merge request is re armed once.", "",
			"A dropped merge request is rearmed once.", "",
		}, "\n"),
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 3 {
		t.Fatalf("Check() reported %d problems, want 3: %v", len(problems), problems)
	}
	for index, line := range []int{3, 5, 7} {
		if problems[index].Term != "re-arm" || problems[index].Line != line {
			t.Errorf("Check() reported %+v, want re-arm on line %d", problems[index], line)
		}
	}
}

// A line wrap between a term's parts is a spacing of them like any other. These
// documents are wrapped at eighty columns, so this is the likeliest way a term
// of two words is written without either word looking coined on its own line.
func TestCheckReportsATermBrokenByALineWrap(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/product/one.md": "# One\n\nThe refusal names the condition before development begins, at minute\nzero, rather than at review.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "minute zero" || problems[0].Line != 3 {
		t.Fatalf("Check() reported %v, want minute zero reported where it starts, on line 3", problems)
	}
}

// What a term cannot wrap across, it is not matched across. A paragraph that
// ends in one part and a paragraph that begins with the next are two sentences
// about different things, and so are the lines on either side of a code block.
func TestCheckDoesNotMatchAcrossAParagraphOrAFence(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": strings.Join([]string{
			"# One", "",
			"The probe is run at the appointed minute", "",
			"Zero runs were refused.", "",
			"It is run at the appointed minute",
			"```",
			"grep zero docs",
			"```",
			"Zero of them were refused.", "",
		}, "\n"),
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing: neither pair is one phrase", problems)
	}
}

// A variant of a registered term is the registered term, so registering one
// permits every spelling of it rather than the one the register wrote.
func TestCheckPermitsAVariantOfARegisteredTerm(t *testing.T) {
	t.Parallel()

	directory := root(t, register("| `minute zero` | before development begins | the invariant |"), map[string]string{
		"docs/decisions/one.md": "# One\n\nEvery run begins with a minute-zero execution probe.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing: the term is registered", problems)
	}
}

// A term the register writes as one word is looked for as one word. `hand back`
// is ordinary English in sentences that have nothing to do with `handback`, and
// a check that reported those is one people learn to argue with.
func TestCheckDoesNotSplitATermTheRegisterWritesAsOneWord(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": "# One\n\nThe reviewer may hand back the work, or take it.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing: `hand back` here is two ordinary words", problems)
	}
}

// One term written twice on a line is one problem, because what a reader is sent
// to is the line.
func TestCheckReportsOneProblemPerTermPerLine(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md": "# One\n\nA wedged check beside a wedged release.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Term != "wedged" {
		t.Fatalf("Check() reported %v, want the one line named once", problems)
	}
}

func TestCheckReportsARegisterEntryThatDefinesNothing(t *testing.T) {
	t.Parallel()

	directory := root(t, register(
		"| `sink` |  | `yoyo slack` |",
		"| `docket` | the list of stopped runs |  |",
		"| `sink` | the process that posts to Slack | `yoyo slack` |",
	), nil)
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 3 {
		t.Fatalf("Check() reported %d problems, want 3: %v", len(problems), problems)
	}
	for _, want := range []string{"no plain-word definition", "without saying where it is used", "registered twice"} {
		found := false
		for _, problem := range problems {
			if strings.Contains(problem.Reason, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("Check() reported %v, want one naming %q", problems, want)
		}
	}
}

// A word this package has never heard of is registrable, so registering a new
// coinage is a row in a document rather than a change to the checker.
func TestRegisterCarriesATermTheVocabularyDoesNotKnow(t *testing.T) {
	t.Parallel()

	directory := root(t, register("| `doorbell` | the thing that says a run wants you | `yoyo status` |"), nil)
	entries, err := Register(directory)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Term != "doorbell" || entries[0].Used != "`yoyo status`" {
		t.Fatalf("Register() = %+v, want the one entry read whole", entries)
	}
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing", problems)
	}
}

// An unreadable register fails the check rather than emptying it. Every term is
// undefined against a register that is not there, so the alternative reports the
// documents as full of undefined coinage and sends the reader to the wrong file.
func TestCheckFailsWhenTheRegisterIsMissing(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if _, err := Check(directory); err == nil {
		t.Fatal("Check() error = nil, want the missing register named")
	}
}

func TestDocumentsWalksTheHomesAndNothingElse(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"docs/designs/one.md":            "# One\n",
		"docs/decisions/invariants/a.md": "# A\n",
		"docs/product/goals/v1.md":       "# V1\n",
		"docs/conversation.md":           "# Not a home\n",
		"docs/designs/notes.txt":         "not markdown\n",
	})
	documents, err := Documents(directory)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	want := []string{"docs/decisions/invariants/a.md", "docs/designs/one.md", "docs/product/goals/v1.md"}
	if len(documents) != len(want) {
		t.Fatalf("Documents() = %v, want %v", documents, want)
	}
	for index, document := range documents {
		if document != want[index] {
			t.Errorf("Documents()[%d] = %s, want %s", index, document, want[index])
		}
	}
}

func TestGuideFilesWalksTheGuidesAndNotTheHomesOrTheRecords(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"README.md":                   "# Readme\n",
		"docs/operations.md":          "# Operations\n",
		"docs/configuration/runs.md":  "# Runs\n",
		"docs/designs/one.md":         "# One\n",
		"docs/diagnoses/a.md":         "# A\n",
		"docs/releases/v1.md":         "# V1\n",
		"docs/experiments/e/notes.md": "# E\n",
		"docs/slack/notes.txt":        "not markdown\n",
	})
	guides, err := GuideFiles(directory)
	if err != nil {
		t.Fatalf("GuideFiles() error = %v", err)
	}
	want := []string{"README.md", "docs/configuration/runs.md", "docs/operations.md"}
	if strings.Join(guides, ",") != strings.Join(want, ",") {
		t.Fatalf("GuideFiles() = %v, want %v", guides, want)
	}
}

// The guides are held to the register for a term marked Guides — `re-arm`,
// which the guides use because `yoyo triage rearm` is what the command is
// called — and for no other term, since nobody has swept them for the rest.
// So the row is what the guide prose leans on: with it the guide passes, and
// without it the guide fails in every spelling the verb is written.
func TestGuidesAreHeldToTheRegisterForATermMarkedGuides(t *testing.T) {
	t.Parallel()

	guide := map[string]string{
		"docs/operations.md": "# Operations\n\nRun `yoyo triage rearm` once; a second re-arm is refused.\n\nThe cadence is set elsewhere.\n",
	}
	permitted := root(t, register("| `re-arm` | repeat the merge request a forge dropped | `yoyo triage rearm` |"), guide)
	forbidden := root(t, register(), guide)

	allowed, err := Check(permitted)
	if err != nil {
		t.Fatalf("Check(permitted) error = %v", err)
	}
	if len(allowed) != 0 {
		t.Fatalf("Check() reported %v, want nothing: re-arm is registered and cadence is not read in a guide", allowed)
	}
	refused, err := Check(forbidden)
	if err != nil {
		t.Fatalf("Check(forbidden) error = %v", err)
	}
	if len(refused) != 1 || refused[0].Path != "docs/operations.md" || refused[0].Line != 3 || refused[0].Term != "re-arm" {
		t.Fatalf("Check() reported %v, want re-arm once at docs/operations.md:3", refused)
	}
}

// The three words retired for reaching the operator with nothing saying what
// they meant are refused in the guides in every form they were written in —
// `environmental refusal`, `environmental cause`, and `refused environmentally`
// are the same coinage as `environmental stop` — and an idle boundary, which is
// ordinary English, is not mistaken for the idle bound.
func TestGuidesRefuseTheRetiredRunStopWordsInEveryForm(t *testing.T) {
	t.Parallel()

	guide := map[string]string{
		"docs/work.md": "# Work\n\nAn environmental stop.\n\nAn environmental refusal.\n\nAn environmental cause.\n\nRefused environmentally.\n\nThe idle bound ran out.\n\nA stall continuation.\n\nTaken over at idle boundaries.\n",
	}
	problems, err := Check(root(t, register(), guide))
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	want := map[int]string{3: "environmental stop", 5: "environmental stop", 7: "environmental stop", 9: "environmental stop", 11: "idle bound", 13: "stall continuation"}
	if len(problems) != len(want) {
		t.Fatalf("Check() reported %v, want one problem on each of lines %v", problems, want)
	}
	for _, problem := range problems {
		if problem.Path != "docs/work.md" || want[problem.Line] != problem.Term {
			t.Errorf("Check() reported %+v, want %q on line %d", problem, want[problem.Line], problem.Line)
		}
	}
}

// A home a project has not created is intent not yet written rather than a
// defect, which is the judgement the goals check already makes about an absent
// artifact home.
func TestDocumentsToleratesAHomeThatIsNotThere(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{"docs/designs/one.md": "# One\n"})
	documents, err := Documents(directory)
	if err != nil {
		t.Fatalf("Documents() error = %v", err)
	}
	if len(documents) != 1 {
		t.Fatalf("Documents() = %v, want the one document in the one home that exists", documents)
	}
}

// A source string is read as a document is: the help text below is a raw
// literal, and the term is reported on the physical line it is written on
// rather than the line the literal opens on.
func TestCheckReportsACoinageInASourceString(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"internal/cli/one.go": strings.Join([]string{
			"package cli", "",
			"const usage = `usage: yoyo one", "",
			"Naming no item prints the list with whose move each one is.",
			"`", "",
		}, "\n"),
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Path != "internal/cli/one.go" || problems[0].Line != 5 || problems[0].Term != "whose-move" {
		t.Fatalf("Check() reported %v, want whose-move at internal/cli/one.go:5", problems)
	}
	if !strings.Contains(problems[0].Reason, "waiting on you") {
		t.Errorf("Check() reason = %q, want the plain wording in it", problems[0].Reason)
	}
}

// An interpreted literal is one physical line whatever it escapes, so a term
// on either side of a `\n` in one is reported on the line the literal starts —
// and wrapped across the escape it is still one term.
func TestCheckReportsACoinageInAnInterpretedStringOnItsOwnLine(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"internal/notify/one.go": strings.Join([]string{
			"package notify", "",
			"func line() string {",
			"\treturn \"the pause is in\\nforce; nothing runs\\nuntil it lifts\"",
			"}", "",
		}, "\n"),
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Line != 4 || problems[0].Term != "in force" {
		t.Fatalf("Check() reported %v, want in force at line 4", problems)
	}
}

// What the source scan does not read: a comment, which is written for whoever
// reads the code; a test, which names the wording it refuses as often as the
// wording it wants; and a package outside the surfaces.
func TestCheckReadsOnlyTheStringsOfTheSurfaces(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"internal/cli/one.go":        "package cli\n\n// A wedged check is one that never finishes.\nconst usage = \"plain\"\n",
		"internal/cli/one_test.go":   "package cli\n\nconst refused = \"a wedged check\"\n",
		"internal/orchestrator/a.go": "package orchestrator\n\nconst said = \"a wedged check\"\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing: none of these is a string an operator is shown", problems)
	}
}

// The provider and configuration packages are read, since their refusals are
// what configuration loading and `yoyo doctor` print; a struct tag in them is
// not, because it names a key rather than saying anything.
func TestCheckReadsTheProviderAndConfigurationStringsButNotTheirKeys(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"internal/backend/one.go": "package backend\n\ntype Plugin struct {\n\tWedges []string `yaml:\"wedges\"`\n}\n",
		"internal/config/one.go":  "package config\n\nconst refused = \"the check is wedged\"\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 1 || problems[0].Path != "internal/config/one.go" || problems[0].Term != "wedged" {
		t.Fatalf("Check() reported %v, want only wedged in internal/config/one.go", problems)
	}
}

// The dashboard's script is read whole, comments and all.
func TestCheckReadsTheDashboardAssetsWhole(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"internal/dashboard/assets/dashboard.js": "// each says whose move it is\nvar label = \"whose move: \" + whose;\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 2 || problems[0].Line != 1 || problems[1].Line != 2 {
		t.Fatalf("Check() reported %v, want the comment and the string both", problems)
	}
}

// `in forced-colours mode` is not `in force`: the term is held to a whole word.
func TestCheckDoesNotReportInForcedAsInForce(t *testing.T) {
	t.Parallel()

	directory := root(t, register(), map[string]string{
		"internal/dashboard/assets/dashboard.css": "/* the page reads the same in forced-colours mode */\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Check() reported %v, want nothing", problems)
	}
}

// A replaced row can name the governed documents that still carry its term
// while their owner amends them. The term is excused there and nowhere else:
// not in another document, and not in a source string.
func TestAReplacedRowExcusesOnlyTheDocumentsItNames(t *testing.T) {
	t.Parallel()

	directory := root(t, registerReplacing([]string{
		"| `in force` | active | `docs/decisions/invariants/README.md` |",
	}), map[string]string{
		"docs/decisions/invariants/README.md": "# Invariants\n\nA reviewer leaves it in force and proposes the amendment.\n",
		"docs/designs/one.md":                 "# One\n\nThe directive is in force.\n",
		"internal/cli/one.go":                 "package cli\n\nconst said = \"no directives are in force\"\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("Check() reported %d problems, want 2: %v", len(problems), problems)
	}
	if problems[0].Path != "docs/designs/one.md" || problems[1].Path != "internal/cli/one.go" {
		t.Errorf("Check() reported %v, want the unexcused document and the source string, and not the excused document", problems)
	}
}

// The excuse ends with the amendment: a row naming a document that no longer
// carries the term is reported, at the row, so the document comes off it.
func TestAReplacedRowExcusingADocumentThatNoLongerCarriesTheTermIsReported(t *testing.T) {
	t.Parallel()

	registerBody := registerReplacing([]string{
		"| `in force` | active | `docs/decisions/invariants/README.md`, `docs/designs/gone.md` |",
	})
	directory := root(t, registerBody, map[string]string{
		"docs/decisions/invariants/README.md": "# Invariants\n\nA reviewer leaves it as it stands and proposes the amendment.\n",
	})
	problems, err := Check(directory)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(problems) != 2 {
		t.Fatalf("Check() reported %d problems, want one per document the row names: %v", len(problems), problems)
	}
	for _, problem := range problems {
		if problem.Path != RegisterPath || problem.Term != "in force" || !strings.Contains(problem.Reason, "take the document off the row") {
			t.Errorf("Check() reported %+v, want the row itself refused", problem)
		}
	}
	if problems[0].Line != problems[1].Line || problems[0].Line != strings.Count(registerBody[:strings.Index(registerBody, "| `in force`")], "\n")+1 {
		t.Errorf("Check() reported lines %d and %d, want both at the row", problems[0].Line, problems[1].Line)
	}
}

// Every vocabulary entry has to carry the two things a failure is made of: the
// term the register is searched for, and the wording the reader is offered.
func TestVocabularyIsComplete(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	for _, coinage := range Vocabulary {
		switch {
		case coinage.Term == "" || coinage.Match == "" || coinage.PlainWords == "":
			t.Errorf("vocabulary entry %+v is missing a field", coinage)
		case seen[coinage.Term]:
			t.Errorf("vocabulary lists %q twice", coinage.Term)
		}
		seen[coinage.Term] = true
	}
}
