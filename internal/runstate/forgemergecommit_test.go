package runstate

import (
	"strings"
	"testing"
)

// The commit the forge names as a merge, and the record of having asked for
// it, are kept only on a request recorded merged, and the commit must be one.
func TestTheForgeMergeCommitIsKeptOnlyOnAMergedRequest(t *testing.T) {
	t.Parallel()
	request := func(change func(*PullRequest)) PullRequest {
		published := PullRequest{Remote: "origin", Branch: "yoyodyne/item/run", Number: 41, URL: "https://example.test/pull/41",
			HeadCommit: strings.Repeat("a", 40), State: "MERGED", Merged: true}
		change(&published)
		return published
	}
	for name, test := range map[string]struct {
		change func(*PullRequest)
		says   string
	}{
		"a merged request with the forge's commit":  {func(p *PullRequest) { p.ForgeMergeCommit, p.MergeCommitAsked = strings.Repeat("d", 40), true }, ""},
		"a merged request the forge named none for": {func(p *PullRequest) { p.MergeCommitAsked = true }, ""},
		"a commit that is not one":                  {func(p *PullRequest) { p.ForgeMergeCommit = "squash" }, "forge_merge_commit \"squash\" is invalid"},
		"a commit on an unmerged request": {func(p *PullRequest) { p.Merged, p.ForgeMergeCommit = false, strings.Repeat("d", 40) },
			"require a merged request"},
		"asked on an unmerged request": {func(p *PullRequest) { p.Merged, p.MergeCommitAsked = false, true }, "require a merged request"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := request(test.change).Validate()
			if test.says == "" && err != nil || test.says != "" && (err == nil || !strings.Contains(err.Error(), test.says)) {
				t.Fatalf("Validate() = %v, want %q", err, test.says)
			}
		})
	}
}
