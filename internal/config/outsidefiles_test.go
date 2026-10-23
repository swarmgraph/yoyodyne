package config

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
)

// A project that lists no host fetches nothing, which is every project written
// before pinned outside files existed.
func TestAProjectThatListsNoHostFetchesNoOutsideFile(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if policy := cfg.OutsideFiles.Policy(); policy.Enabled() || policy.Limit() != outsidefile.DefaultMaxBytes {
		t.Fatalf("a file listing no host has outside-file policy %#v", policy)
	}
}

func TestListedHostsAndTheSizeLimitReachTheCapability(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig + "outside_files:\n  hosts: [cdn.jsdelivr.net]\n  max_bytes: 1048576\n"))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	policy := cfg.OutsideFiles.Policy()
	if !policy.Enabled() || policy.Hosts[0] != "cdn.jsdelivr.net" || policy.Limit() != 1<<20 {
		t.Fatalf("Policy() = %#v", policy)
	}
}

func TestOutsideFileSettingsThatNameNoHostOrPassTheCeilingAreRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ input, want string }{
		{"outside_files:\n  hosts: [https://cdn.jsdelivr.net]\n", "no scheme or path"},
		{"outside_files:\n  hosts: [cdn.jsdelivr.net/npm]\n", "no scheme or path"},
		{"outside_files:\n  hosts: [CDN.jsdelivr.net]\n", "lowercase host name"},
		{"outside_files:\n  hosts: [cdn.jsdelivr.net, cdn.jsdelivr.net]\n", "twice"},
		{"outside_files:\n  max_bytes: -1\n", "cannot be negative"},
		{"outside_files:\n  max_bytes: 104857600\n", "ceiling no project may raise"},
	} {
		_, err := Decode(strings.NewReader(validBootstrapConfig + test.input))
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("Decode(%q) error = %v, want one saying %q", test.input, err, test.want)
		}
	}
}
