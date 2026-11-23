package config

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
)

// A project that writes nothing keeps the limits the reviewer's copy was always
// rendered under, and the revision it had before the key existed.
func TestAProjectThatSetsNoReviewLimitKeepsTheDefaults(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if got := resolved.Config.ReviewLimits.DiffLimits(); got != (gitworktree.DiffLimits{}) {
		t.Fatalf("DiffLimits() = %+v, want every limit left to its default", got)
	}
	encoded, err := json.Marshal(resolved.Config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "review_limits") {
		t.Fatalf("the digested form carries review_limits while nothing is set: %s", encoded)
	}
}

func TestTheProjectsReviewLimitsReachTheRendering(t *testing.T) {
	t.Parallel()

	plain := loadProject(t, minimalProjectConfig, nil)
	resolved := loadProject(t, minimalProjectConfig+"review_limits:\n  diff_kib: 128\n  file_kib: 96\n  files: 300\n", nil)
	want := gitworktree.DiffLimits{MaxTotalBytes: 128 << 10, MaxFileBytes: 96 << 10, MaxFiles: 300}
	if got := resolved.Config.ReviewLimits.DiffLimits(); got != want {
		t.Fatalf("DiffLimits() = %+v, want %+v", got, want)
	}
	if resolved.Config.Revision() == plain.Config.Revision() {
		t.Error("setting the review limits did not change the configuration revision")
	}
	for _, key := range []string{"review_limits.diff_kib", "review_limits.file_kib", "review_limits.files"} {
		if origin := resolved.Origins[key]; origin == "" || origin == OriginDefault {
			t.Errorf("origin of %s = %q, want the project's file", key, origin)
		}
	}
	// One limit set leaves the others at their defaults.
	one := loadProject(t, minimalProjectConfig+"review_limits:\n  files: 50\n", nil)
	if got := one.Config.ReviewLimits.DiffLimits(); got != (gitworktree.DiffLimits{MaxFiles: 50}) {
		t.Fatalf("DiffLimits() = %+v, want only the file count set", got)
	}
}

// A value outside its bound is refused as the configuration loads, naming the
// limit and the bound, and so is a value written as zero.
func TestAReviewLimitOutsideItsBoundIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ body, want string }{
		{"diff_kib: 63", "review_limits.diff_kib is 63, and it must be between 64 and 256 KiB"},
		{"diff_kib: 512", "review_limits.diff_kib is 512, and it must be between 64 and 256 KiB, because the 768 KiB the reviewer is shown in all leaves no room for a larger diff beside the rest of the review evidence"},
		{"diff_kib: 0", "review_limits.diff_kib is 0, and it must be between 64 and 256 KiB"},
		{"file_kib: 15", "review_limits.file_kib is 15, and it must be between 16 and 128 KiB"},
		{"file_kib: 129", "review_limits.file_kib is 129, and it must be between 16 and 128 KiB"},
		{"files: 49", "review_limits.files is 49, and it must be between 50 and 400 files"},
		{"files: 401", "review_limits.files is 401, and it must be between 50 and 400 files"},
		{"files: -1", "review_limits.files is -1, and it must be between 50 and 400 files"},
		{"files: 0", "review_limits.files is 0, and it must be between 50 and 400 files"},
	} {
		_, err := loadProjectError(t, minimalProjectConfig+"review_limits:\n  "+test.body+"\n", nil)
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("review_limits %s: error = %v, want one saying %q", test.body, err, test.want)
		}
	}
	for _, body := range []string{"diff_kib: 64", "diff_kib: 256", "file_kib: 16", "file_kib: 128", "files: 50", "files: 400"} {
		if _, err := loadProjectError(t, minimalProjectConfig+"review_limits:\n  "+body+"\n", nil); err != nil {
			t.Errorf("review_limits %s at its bound was refused: %v", body, err)
		}
	}
}

// The three limits are values the development manager may apply, and nothing
// beside them under the section is a run's or anybody else's to set.
func TestTheReviewLimitsAreARolesToApply(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"review_limits", "review_limits.diff_kib", "review_limits.file_kib", "review_limits.files"} {
		if err := RoleMayApply(key); err != nil {
			t.Errorf("RoleMayApply(%q) = %v, want it applicable", key, err)
		}
	}
	for _, key := range []string{"review_limits.diff_kib", "review_limits.file_kib", "review_limits.files"} {
		found := false
		for _, known := range SchemaKeys() {
			found = found || known == key
		}
		if !found {
			t.Errorf("SchemaKeys() does not list %s", key)
		}
	}
}
