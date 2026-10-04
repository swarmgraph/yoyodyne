package ownership

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

// PersonOnlyReason is the closed set of physical acts a role cannot perform.
// Ordinary repairs and delegated decisions have no entry in this registry.
type PersonOnlyReason string

const (
	PersonCredential        PersonOnlyReason = "credential"
	PersonRepositorySetting PersonOnlyReason = "repository-setting"
	PersonProtectedFile     PersonOnlyReason = "protected-file"
)

var personOnlyReasons = map[PersonOnlyReason]func(string) bool{
	PersonCredential:        func(target string) bool { return target != "" },
	PersonRepositorySetting: func(target string) bool { return target != "" },
	PersonProtectedFile:     personOnlyFile,
}

// PersonOnlyRemedy names the act, what it changes, and the exact step the person
// must take. The resolver validates it against trusted code, never prose or a
// configuration entry that claims somebody needs the operator.
type PersonOnlyRemedy struct {
	Reason PersonOnlyReason `json:"reason"`
	Target string           `json:"target"`
	Step   string           `json:"step"`
}

func (r PersonOnlyRemedy) Validate() error {
	var problems []error
	check, allowed := personOnlyReasons[r.Reason]
	if !allowed {
		problems = append(problems, fmt.Errorf("person-only reason %q is not permitted; use credential, repository-setting, or protected-file", r.Reason))
	} else if !check(strings.TrimSpace(r.Target)) {
		problems = append(problems, fmt.Errorf("target %q is not a person-only %s remedy", r.Target, r.Reason))
	}
	for _, field := range []struct{ name, value string }{{"target", r.Target}, {"step", r.Step}} {
		if strings.TrimSpace(field.value) == "" || len(field.value) > 4096 || strings.ContainsAny(field.value, "\r\n") {
			problems = append(problems, fmt.Errorf("person-only %s must be a nonempty line of at most 4096 bytes", field.name))
		}
	}
	return errors.Join(problems...)
}

func personOnlyFile(target string) bool {
	clean := path.Clean(strings.ReplaceAll(target, "\\", "/"))
	// These files are beyond any grant. A conformance test checks this registry
	// against the protected-path gate without importing its higher-level packages.
	switch clean {
	case ".claude/settings.json", ".claude/settings.local.json":
		return true
	}
	return strings.HasPrefix(strings.ToLower(clean), ".yoyodyne/roles/")
}
