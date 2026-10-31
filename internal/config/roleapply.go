package config

import (
	"fmt"
	"strings"
)

// The values a role may apply to the project's configuration, held in Go so no
// configuration can widen the list (docs/designs/portable-agent-configuration.md,
// section 6). A value may be applied by a role when it changes how work is done
// and never who may do it or what reaches the machine; every key not listed
// here stays the operator's, and a role's write to it is refused before
// anything is prepared.
//
// Each entry is a key path in SchemaKeys' form, with "*" standing for a name
// the file chooses; a key is applicable when it is an entry or lies under one.
var roleApplicableKeys = []string{
	"checks",
	"landing_checks",
	"execution.developer_models",
	"execution.developer_slots",
	"recurring_tasks.*.every",
	"recurring_tasks.*.model",
	"execution.check_timeout",
	"execution.check_stage_timeout",
	"execution.landing_check_timeout",
	"execution.repair_attempts_before_replan",
	"execution.integration_retries_before_reconciliation",
	"execution.transient_relaunches_before_blocking",
}

// operatorKeyReasons says why a key that is never a role's is the operator's,
// for the keys whose refusal is worth explaining beyond the general one.
var operatorKeyReasons = map[string]string{
	"install": "an install step is a command the harness runs with network access, and what it fetches and runs decides what reaches this machine",
}

// RoleMayApply refuses a role's write to a configuration key that is not the
// role's to apply, naming the key and saying the value stays the operator's. A
// key is written as the configuration file writes it, dotted, with a list
// entry as "[]" or its position: "install", "recurring_tasks.sweep.every".
func RoleMayApply(key string) error {
	segments := roleKeySegments(key)
	if len(segments) == 0 {
		return fmt.Errorf("a role's write names no configuration key")
	}
	for _, applicable := range roleApplicableKeys {
		if roleKeyUnder(segments, strings.Split(applicable, ".")) {
			return nil
		}
	}
	said := strings.Join(segments, ".")
	if reason, named := operatorKeyReasons[segments[0]]; named {
		return fmt.Errorf("%s is refused for a role's write: the value stays the operator's to set, because %s", said, reason)
	}
	return fmt.Errorf("%s is refused for a role's write: the value stays the operator's to set, because a role may apply only values that change how work is done and never who may do it", said)
}

func roleKeySegments(key string) []string {
	var segments []string
	for _, segment := range strings.Split(strings.TrimSpace(key), ".") {
		if segment = strings.TrimSpace(segment); segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// roleKeyUnder reports whether a key is pattern or lies under it.
func roleKeyUnder(key, pattern []string) bool {
	if len(key) < len(pattern) {
		return false
	}
	for index, segment := range pattern {
		if segment != SchemaAnyKey && segment != key[index] {
			return false
		}
	}
	return true
}
