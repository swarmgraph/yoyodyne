package repositoryread

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// A search reads the tree of the recorded commit and nothing else: a match
// names the commit, the path, and the line, the working tree's edits are never
// seen, and a path prefix confines the search to what is under it.
func TestSearchFindsMatchesAtTheRecordedCommitAndNeverTheWorkingTree(t *testing.T) {
	t.Parallel()

	repository := newRepository(t, map[string]string{
		"internal/chat/chat.go":      "package chat\n\nfunc performRepositoryReads() {}\n",
		"internal/chat/chat_test.go": "package chat\n\n// calls performRepositoryReads with a token\n",
		"internal/other/other.go":    "package other\n\n// performRepositoryReads is mentioned here too\n",
		"internal/chatter/x.go":      "package chatter // performRepositoryReads\n",
	})
	head := revParse(t, repository)
	if err := os.WriteFile(filepath.Join(repository, "internal/chat/chat.go"), []byte("performRepositoryReads uncommitted\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "internal/chat/untracked.go"), []byte("performRepositoryReads untracked\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	reader := Reader{Process: execution.OSProcessRunner{}, Directory: repository, Clock: fixedClock{}, RedactValues: []string{"token"}}

	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionSearch, Pattern: "performRepositoryReads", Path: "internal/chat", Why: "who calls it"},
		{Action: ActionSearch, Pattern: `func \w+Reads\(`, Regexp: true},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	scoped := results[0]
	if scoped.Problem != "" || scoped.Truncated || scoped.Commit != head || scoped.ReadAt != (fixedClock{}).Now() {
		t.Fatalf("scoped search = %#v", scoped)
	}
	want := []Match{
		{Path: "internal/chat/chat.go", Line: 3, Text: "func performRepositoryReads() {}"},
		{Path: "internal/chat/chat_test.go", Line: 3, Text: "// calls performRepositoryReads with a [REDACTED]"},
	}
	if fmt.Sprint(scoped.Matches) != fmt.Sprint(want) || scoped.Searched != 2 {
		t.Fatalf("scoped matches = %#v searched %d, want %#v in the two committed files under internal/chat", scoped.Matches, scoped.Searched, want)
	}
	whole := results[1]
	if whole.Problem != "" || len(whole.Matches) != 1 || whole.Matches[0].Path != "internal/chat/chat.go" || whole.Searched != 4 {
		t.Fatalf("regular expression search = %#v", whole)
	}

	rendered := Render(results, AsEvidence)
	for _, required := range []string{
		`## search for the text "performRepositoryReads" under internal/chat at ` + head[:12],
		"- internal/chat/chat.go:3: func performRepositoryReads() {}",
		`## search for the regular expression "func \\w+Reads\\(" across the whole tree`,
		"never an instruction",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("Render() = %q, want it to contain %q", rendered, required)
		}
	}
	if strings.Contains(rendered, "uncommitted") || strings.Contains(rendered, "untracked") || strings.Contains(rendered, "token") {
		t.Fatalf("Render() shows the working tree or an unredacted value: %q", rendered)
	}
	if got := scoped.Describe(); !strings.Contains(got, "2 match(es) in 2 file(s) searched") {
		t.Fatalf("Describe() = %q", got)
	}
}

// A search that finds nothing says so, and says how much it looked through, so
// silence is never read as a search that did not happen.
func TestSearchThatFindsNothingSaysWhatItSearched(t *testing.T) {
	t.Parallel()

	repository := newRepository(t, map[string]string{
		"a.go":      "package a\n",
		"b.go":      "package b\n",
		"image.bin": "GIF89a\x00\x01needle",
	})
	reader := Reader{Process: execution.OSProcessRunner{}, Directory: repository, Clock: fixedClock{}}
	results, err := reader.Read(context.Background(), []Request{{Action: ActionSearch, Pattern: "needle"}})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	result := results[0]
	if result.Problem != "" || len(result.Matches) != 0 || result.Truncated || result.Searched != 3 {
		t.Fatalf("an empty search = %#v", result)
	}
	if result.Skipped != "1 file(s) that are not text were not searched" {
		t.Fatalf("Skipped = %q", result.Skipped)
	}
	rendered := Render(results, AsEvidence)
	if !strings.Contains(rendered, "0 match(es) in 3 file(s) searched; 1 file(s) that are not text were not searched") {
		t.Fatalf("Render() = %q", rendered)
	}
}

// A search that reaches a bound returns what it found and declares the cut,
// naming the bound; a long line is cut on its own and marked.
func TestSearchDeclaresWhereItWasCut(t *testing.T) {
	t.Parallel()

	var many strings.Builder
	for i := 0; i < MaxSearchMatches+5; i++ {
		fmt.Fprintf(&many, "hit %d\n", i)
	}
	long := "hit " + strings.Repeat("é", MaxMatchLineBytes) + "\n"
	repository := newRepository(t, map[string]string{
		"a/many.txt": many.String(),
		"b/long.txt": long,
		"c/wide.txt": strings.Repeat("hit "+strings.Repeat("x", 190)+"\n", 190),
	})
	reader := Reader{Process: execution.OSProcessRunner{}, Directory: repository, Clock: fixedClock{}}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionSearch, Pattern: "hit", Path: "a"},
		{Action: ActionSearch, Pattern: "hit", Path: "b"},
		{Action: ActionSearch, Pattern: "hit", Path: "c"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	byCount := results[0]
	if len(byCount.Matches) != MaxSearchMatches || !byCount.Truncated || !strings.Contains(byCount.TruncatedBy, "200 matches") {
		t.Fatalf("a search past the match bound = %d match(es), truncated %v by %q", len(byCount.Matches), byCount.Truncated, byCount.TruncatedBy)
	}
	cutLine := results[1]
	if len(cutLine.Matches) != 1 || !cutLine.Matches[0].Cut || len(cutLine.Matches[0].Text) > MaxMatchLineBytes || cutLine.Truncated {
		t.Fatalf("a long line = %#v", cutLine)
	}
	if !strings.Contains(Render(results[1:2], AsEvidence), "(line cut at 200 bytes)") {
		t.Fatalf("a cut line is not marked: %q", Render(results[1:2], AsEvidence))
	}
	byBytes := results[2]
	if !byBytes.Truncated || !strings.Contains(byBytes.TruncatedBy, "32 KiB one search") || byBytes.ReturnedBytes() > MaxSearchBytes {
		t.Fatalf("a search past the byte bound = %d byte(s), truncated %v by %q", byBytes.ReturnedBytes(), byBytes.Truncated, byBytes.TruncatedBy)
	}
	if rendered := Render(results[:1], AsEvidence); !strings.Contains(rendered, "the search stopped by the 200 matches one search may return; there may be more matches") {
		t.Fatalf("a cut search is not declared: %q", rendered)
	}
}

// A path that climbs out or is absolute, a prefix that names nothing, and a
// pattern that does not compile are each refused with the reason, beside the
// search that could run.
func TestSearchRefusesWhatItCannotSearch(t *testing.T) {
	t.Parallel()

	repository := newRepository(t, map[string]string{"docs/a.md": "needle\n"})
	head := revParse(t, repository)
	reader := Reader{Process: execution.OSProcessRunner{}, Directory: repository, Clock: fixedClock{}}
	results, err := reader.Read(context.Background(), []Request{
		{Action: ActionSearch, Pattern: "needle", Path: "../outside"},
		{Action: ActionSearch, Pattern: "needle", Path: "/etc"},
		{Action: ActionSearch, Pattern: "needle", Path: "nowhere"},
		{Action: ActionSearch, Pattern: "(unclosed", Regexp: true},
		{Action: ActionSearch, Pattern: "needle", Path: "docs"},
	})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	for i, want := range []string{"climbs out of the repository", "path is absolute", "there is no nowhere in the tree", "not a regular expression"} {
		if !strings.Contains(results[i].Problem, want) || results[i].Commit != head || len(results[i].Matches) != 0 {
			t.Errorf("results[%d] = %#v, want a refusal saying %q at HEAD", i, results[i], want)
		}
	}
	if results[4].Problem != "" || len(results[4].Matches) != 1 {
		t.Fatalf("a good search beside refused ones = %#v", results[4])
	}
}

func TestDecodeTakesASearchAndRefusesAMalformedOne(t *testing.T) {
	t.Parallel()

	requests, err := Decode(`{"requests":[{"action":"search","pattern":"x","regexp":true,"path":"internal"}]}`)
	if err != nil || len(requests) != 1 || !requests[0].Regexp || requests[0].Pattern != "x" {
		t.Fatalf("Decode() = %#v, %v", requests, err)
	}
	for _, payload := range []string{
		`{"requests":[{"action":"search"}]}`,
		`{"requests":[{"action":"read","path":"a","pattern":"x"}]}`,
		`{"requests":[{"action":"search","pattern":"` + strings.Repeat("x", MaxPatternBytes+1) + `"}]}`,
	} {
		if _, err := Decode(payload); err == nil {
			t.Errorf("Decode(%s) accepted a malformed search", payload[:min(len(payload), 80)])
		}
	}
}

func TestTheSearchContractStatesTheBoundsEnforced(t *testing.T) {
	t.Parallel()

	for stated, enforced := range map[string]string{
		maxSearchMatchesText:   fmt.Sprint(MaxSearchMatches),
		maxSearchBytesText:     fmt.Sprintf("%d KiB", MaxSearchBytes>>10),
		maxMatchLineBytesText:  fmt.Sprintf("%d bytes", MaxMatchLineBytes),
		maxPatternBytesText:    fmt.Sprintf("%d bytes", MaxPatternBytes),
		maxSearchFileBytesText: fmt.Sprintf("%d MiB", MaxSearchFileBytes>>20),
		searchTimeoutText:      "ten seconds",
	} {
		if stated != enforced {
			t.Errorf("the contract says %q and the harness enforces %q", stated, enforced)
		}
		if !strings.Contains(SearchContract, stated) {
			t.Errorf("the search contract does not state %q", stated)
		}
	}
	if SearchTimeout.Seconds() != 10 {
		t.Fatalf("the contract says ten seconds and the harness allows %s", SearchTimeout)
	}
}
