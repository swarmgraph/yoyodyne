package review

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

func TestReviewChecksCanonicalAbsenceClaimsAgainstRepositoryEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		path      string
		listing   RepositoryEvidence
		wantError bool
	}{
		{"testdata/fixture.bin", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"testdata/fixture.bin"}}}, true},
		{"./testdata//fixture.bin", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"testdata/fixture.bin"}}}, true},
		{"testdata/a/../fixture.bin", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"testdata/fixture.bin"}}}, true},
		{"testdata", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"testdata/fixture.bin"}}}, true},
		{"testdata/with space.bin ", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"testdata/with space.bin "}}}, true},
		{"missing.txt", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head"}}, false},
		{"missing.txt", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Omitted: 1}}, true},
		{"missing.txt", RepositoryEvidence{}, true},
		{"../missing.txt", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head"}}, true},
		{"a/../../missing.txt", RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head"}}, true},
	} {
		t.Run(test.path, func(t *testing.T) {
			provider := &fakeBackend{finalText: `{"decision":"repair","summary":"missing delivery","findings":[{"severity":"major","message":"supply it","absent":"` + test.path + `"}]}`}
			request := newRequest(nil)
			request.Repository = test.listing
			result, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request)
			if (err != nil) != test.wantError {
				t.Fatalf("Review() = %#v, %v", result, err)
			}
			if test.wantError && result.Decision != "" {
				t.Fatalf("unsupported claim became a verdict: %#v", result)
			}
		})
	}
}

func TestReviewAbsenceIncludesUncommittedAdditionsAndExcludesDeletions(t *testing.T) {
	t.Parallel()
	evidence := RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"removed.txt"}}}
	changes := gitworktree.ChangeDiff{Files: []gitworktree.ChangedFile{{Path: "added.txt", Status: "??"}, {Path: "removed.txt", Status: "D"}}}
	for _, path := range []string{"added.txt", "removed.txt"} {
		err := evidence.refute(Verdict{Findings: []Finding{{Absent: path}}}, changes)
		if (err != nil) != (path == "added.txt") {
			t.Fatalf("refute(%q) = %v", path, err)
		}
	}
	// Removing a tracked path does not prove absence when the new-file
	// evidence supplies that same path again above HEAD.
	changes.UntrackedFiles = []string{"removed.txt"}
	if err := evidence.refute(Verdict{Findings: []Finding{{Absent: "removed.txt"}}}, changes); err == nil {
		t.Fatal("an untracked file at a removed path was accepted as absent")
	}
}

func TestReviewRefusesAbsenceWhenUncommittedAdditionsAreOmittedFromTheListing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		changes gitworktree.ChangeDiff
		want    string
	}{
		{"new file shown in the patch", gitworktree.ChangeDiff{UntrackedFiles: []string{"late/added.txt"}}, "holds it"},
		{"new file omitted from the patch", gitworktree.ChangeDiff{OmittedFiles: []gitworktree.OmittedFile{{Path: "late/added.txt", Reason: gitworktree.OmittedPatchFull, Bytes: 8, Digest: "sha256:content"}}}, "holds it"},
		{"empty file omitted from the patch", gitworktree.ChangeDiff{OmittedFiles: []gitworktree.OmittedFile{{Path: "late/added.txt", Reason: gitworktree.OmittedTooManyFiles, Digest: "sha256:empty"}}}, "holds it"},
		{"irregular file omitted from the patch", gitworktree.ChangeDiff{OmittedFiles: []gitworktree.OmittedFile{{Path: "late/added.txt", Reason: gitworktree.OmittedUnreadable, Undigestable: true}}}, "holds it"},
		{"no other presence evidence", gitworktree.ChangeDiff{}, "no complete change listing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := newRequest(nil)
			request.Repository = RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: "head", Files: []string{"first.txt"}}}
			request.Changes = test.changes
			request.Changes.Files = []gitworktree.ChangedFile{{Path: "first.txt", Status: "M"}}
			request.Changes.FilesOmitted = 1
			provider := &fakeBackend{finalText: `{"decision":"repair","summary":"missing delivery","findings":[{"severity":"major","message":"supply it","absent":"./late//added.txt"}]}`}
			result, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) || result.Decision != "" {
				t.Fatalf("an absence claim became a verdict with an incomplete change listing: %#v, %v", result, err)
			}
		})
	}
}

func TestReviewAbsenceUsesWholeContentAndRemovalEvidence(t *testing.T) {
	t.Parallel()
	evidence := RepositoryEvidence{
		Listing:  gitworktree.CommitListing{Commit: "head", Files: []string{"removed.txt"}},
		Contents: []RepositoryFile{{Path: "empty.txt"}},
	}
	changes := gitworktree.ChangeDiff{DeletedFiles: []gitworktree.DeletedFile{{Path: "removed.txt", Whole: true}, {Path: "reduced.txt"}}}
	for _, path := range []string{"removed.txt", "reduced.txt", "empty.txt", "missing.txt"} {
		err := evidence.refute(Verdict{Findings: []Finding{{Absent: path}}}, changes)
		if (err != nil) != (path == "reduced.txt" || path == "empty.txt") {
			t.Fatalf("refute(%q) = %v", path, err)
		}
	}
}

func TestRepositoryEvidenceQuotesWholeContentAndMakesBoundsExplicit(t *testing.T) {
	t.Parallel()
	evidence := RepositoryEvidence{
		Listing:  gitworktree.CommitListing{Commit: "head", Files: []string{"README.md"}},
		Contents: []RepositoryFile{{Path: "README.md", Size: 3200, Content: strings.Repeat("literal\n", 400)}},
	}
	prompt := renderRepository(evidence)
	if !strings.Contains(prompt, "Whole file, 3200 bytes") || strings.Count(prompt, "literal\n") != 400 {
		t.Fatalf("whole content not supplied: %s", prompt)
	}
	bounded := evidence.bounded(800)
	if len(renderRepository(bounded)) > 800 || !strings.Contains(renderRepository(bounded), "Content not supplied") {
		t.Fatalf("input bound did not make the content loss explicit: %s", renderRepository(bounded))
	}
	if evidence.Contents[0].Unavailable != "" {
		t.Fatal("bounding mutated the original evidence")
	}
}

func TestReviewBoundsRepositoryEvidenceAgainstTheWholeInput(t *testing.T) {
	t.Parallel()
	request := newRequest(nil)
	request.Context = strings.Repeat("c", 350<<10)
	request.Changes.Patch = strings.Repeat("p", 350<<10)
	request.Repository.Listing.Commit = "head"
	for index := 0; index < 6000; index++ {
		request.Repository.Listing.Files = append(request.Repository.Listing.Files, fmt.Sprintf("long/path/to/repository/file-%06d.txt", index))
	}
	provider := &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"all changed code was shown"}`}
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(provider.request.Prompt)+len(provider.request.SystemPrompt) > MaxReviewInputBytes || !strings.Contains(provider.request.Prompt, "listing omitted") {
		t.Fatal("the whole input did not bound repository evidence explicitly")
	}
	provider.finalText = `{"decision":"repair","summary":"missing","findings":[{"severity":"major","message":"add it","absent":"missing.txt"}]}`
	if result, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request); err == nil || result.Decision != "" {
		t.Fatalf("an absence claim was accepted against the bounded listing: %#v, %v", result, err)
	}
}

func TestReviewRendersProjectCodeOnlyWhenSupplied(t *testing.T) {
	t.Parallel()
	provider := &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"ok"}`}
	request := newRequest(nil)
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(provider.request.Prompt, "The project's code") {
		t.Fatal("a review with no project code rendered a project code section")
	}
	request.ProjectCodeCommit = "abc123"
	request.ProjectCode = RepositoryEvidence{
		Listing:  gitworktree.CommitListing{Commit: "abc123", Files: []string{"internal/foo/foo.go"}},
		Contents: []RepositoryFile{{Path: "internal/foo/foo.go", Size: 24, Content: "package foo // marker-xyz"}},
	}
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# The project's code at commit abc123", "companion intent repository", "unread rather than absent", "internal/foo/foo.go", "package foo // marker-xyz"} {
		if !strings.Contains(provider.request.Prompt, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
}

func TestReviewBoundsProjectCodeAndRepositoryTogether(t *testing.T) {
	t.Parallel()
	request := newRequest(nil)
	request.Context = strings.Repeat("c", 100<<10)
	huge := func(commit string) RepositoryEvidence {
		e := RepositoryEvidence{Listing: gitworktree.CommitListing{Commit: commit}}
		for index := 0; index < 6000; index++ {
			e.Listing.Files = append(e.Listing.Files, fmt.Sprintf("long/path/to/repository/file-%06d.txt", index))
		}
		e.Contents = []RepositoryFile{{Path: "big.txt", Size: 600 << 10, Content: strings.Repeat("x", 600<<10)}}
		return e
	}
	request.Repository = huge("head")
	request.ProjectCode = huge("proj")
	request.ProjectCodeCommit = "proj"
	provider := &fakeBackend{finalText: `{"decision":"approve","approves":"implementation","summary":"ok"}`}
	if _, err := (Reviewer{Backend: provider, Model: testReviewModel}).Review(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if total := len(provider.request.Prompt) + len(provider.request.SystemPrompt); total > MaxReviewInputBytes {
		t.Fatalf("input is %d bytes, limit %d", total, MaxReviewInputBytes)
	}
	if !strings.Contains(provider.request.Prompt, "# The project's code at commit proj") || !strings.Contains(provider.request.Prompt, "# Repository at the reviewed commit") {
		t.Fatal("one of the two evidence sections is missing")
	}
}
