package runstate

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// structuredStrings are the strings in the run record that are not free text:
// identifiers, commits, paths, and enumerations, each held to a shape or a value
// set somewhere or minted by the harness itself, and none of them a sentence a
// provider, a forge, or an agent phrases. A string belongs here only if that is
// true of it. Anything else belongs in State.recordedTexts, where it is bounded.
//
// Keys name a string by its place in the record, with [] standing for any
// element of a list.
var structuredStrings = map[string]string{
	"retirement.run_id":                                     "the validated run whose publication superseded this run",
	"retirement.commit":                                     "the validated confirmed merge revision",
	"retirement.target_branch":                              "a validated local target branch",
	"retirement.prior_status":                               "the validated status before retirement",
	"reconcile_findings[].step":                             "a validated settlement step",
	"config_comparison.target_commit":                       "a validated compared revision",
	"config_comparison.previous_target_commit":              "the integration's validated previous revision",
	"config_comparison.mismatches[].service":                "the recorded service name",
	"config_comparison.mismatches[].build":                  "the revision reported by the running service",
	"config_comparison.mismatches[].config_path":            "the configuration's absolute path",
	"config_comparison.mismatches[].keys[]":                 "names of configuration keys",
	"config_comparison.template_mismatches[].service":       "the recorded service name",
	"config_comparison.template_mismatches[].build":         "the revision reported by the running service",
	"config_comparison.template_mismatches[].config_path":   "the configuration's absolute path",
	"config_comparison.template_mismatches[].template_path": "the shipped template's path",
	"config_comparison.template_mismatches[].keys[]":        "names of configuration keys",

	"run_id":                               "matched against the run id pattern",
	"product_id":                           "a validated identifier",
	"repository_id":                        "an identifier the configuration names",
	"work_item_id":                         "the tracker's identifier",
	"work_item_labels[]":                   "the tracker's label identifiers",
	"selection.by":                         "who chose the work, in the harness's fixed vocabulary",
	"selection.lift.run_id":                "matched against the run id pattern",
	"selection.lift.branch":                "a branch name the harness cut, bounded where the selection is validated",
	"lifted_commit":                        "matched against the commit pattern",
	"backend":                              "an enumeration",
	"account_alias":                        "matched against the account alias pattern",
	"config_revision":                      "matched against the configuration revision pattern",
	"build":                                "matched against the revision pattern",
	"workflow_instance_id":                 "a validated identifier",
	"provider_session_id":                  "the provider's session identifier",
	"provider_model":                       "a model selector",
	"provider_resolved_model":              "the provider's model identifier",
	"provider_resolved_effort":             "provider-reported effort, with absence recorded separately",
	"review_resolved_effort":               "provider-reported effort, with absence recorded separately",
	"provider_effort":                      "an effort level from the configuration, validated against the provider's levels",
	"developer_model":                      "a model selector from the configuration",
	"status":                               "an enumeration",
	"phase":                                "an enumeration",
	"stop_class":                           "an enumeration, refused at the save when it names no class",
	"worktree_path":                        "a path the harness cut",
	"branch":                               "a branch name the harness cut",
	"base_commit":                          "matched against the commit pattern",
	"harness_commit":                       "matched against the commit pattern",
	"artifacts_retired_by":                 "a run id",
	"preserved_work_ref":                   "a ref the harness names",
	"target_branch":                        "a local branch name",
	"review_session_id":                    "the provider's session identifier",
	"review_model":                         "a model selector",
	"review_resolved_model":                "the provider's model identifier",
	"review_effort":                        "an effort level from the configuration, validated against the provider's levels",
	"review_base_commit":                   "matched against the commit pattern",
	"review_head_commit":                   "matched against the commit pattern",
	"review_decision":                      "an enumeration",
	"review_approves":                      "an enumeration",
	"landing_outcome":                      "an enumeration",
	"landing_blocked_by":                   "a work item identifier resolved against the tracker",
	"review_finding_details[].severity":    "an enumeration",
	"review_finding_details[].disposition": "an enumeration",
	"review_finding_details[].absent":      "a repository-relative path validated by review.Finding.Validate and checked against the repository evidence",
	"check_failure.command":                "a configured command or the names of recorded forge checks",
	"check_failure.forge_head_commit":      "matched against the commit pattern",
	"check_failure.local_promotion.target_branch":                                            "a local branch name",
	"check_failure.local_promotion.source_commit":                                            "matched against the commit pattern",
	"check_failure.local_promotion.target_commit":                                            "matched against the commit pattern",
	"check_failure.local_promotion.previous_target_commit":                                   "matched against the commit pattern",
	"repair_continuations[].superseded_check_failure.command":                                "a configured command or the names of recorded forge checks",
	"repair_continuations[].superseded_check_failure.forge_head_commit":                      "matched against the commit pattern",
	"repair_continuations[].superseded_check_failure.local_promotion.target_branch":          "a local branch name",
	"repair_continuations[].superseded_check_failure.local_promotion.source_commit":          "matched against the commit pattern",
	"repair_continuations[].superseded_check_failure.local_promotion.target_commit":          "matched against the commit pattern",
	"repair_continuations[].superseded_check_failure.local_promotion.previous_target_commit": "matched against the commit pattern",
	"check_stage.command":                                           "a command the configuration declares",
	"checks_passed.content":                                         "a digest the worktree manager computes over the change",
	"developer_summary.content":                                     "a digest the worktree manager computes over the change",
	"checks_passed.commit":                                          "the harness commit the record already holds",
	"checks_passed.commands[]":                                      "commands the configuration declares",
	"check_stage_continuations[].command":                           "a command the configuration declares",
	"landing_checks.commit":                                         "the integrated commit the harness recorded",
	"landing_checks.target_branch":                                  "a local branch name",
	"landing_checks.checks[].command":                               "a command the configuration declares",
	"landing_checks.filed_work_item":                                "the tracker's identifier",
	"path_refusal.paths[]":                                          "repository paths the change touched, bounded in number",
	"path_refusal.grants[]":                                         "paths the work item granted, bounded in number",
	"verification.probe.outcome":                                    "an enumeration",
	"verification.checks[].outcome":                                 "an enumeration",
	"refused_amendments[].role":                                     "a validated role identifier",
	"amendments[].role":                                             "a validated role identifier",
	"amendments[].artifact":                                         "an artifact id the amendment channel resolved",
	"amendments[].id":                                               "an amendment identifier",
	"amendments[].folded_into":                                      "an amendment identifier",
	"stale_block_clear.outcome":                                     "an enumeration",
	"stale_block_clear.status":                                      "a tracker status value",
	"environmental.cause":                                           "an enumeration",
	"environmental.provider_stop":                                   "an enumeration",
	"environmental.round_charged_by":                                "a run id",
	"integration_stop.cause":                                        "an enumeration",
	"integration_stop.phase":                                        "an enumeration",
	"replay_conflict.target_branch":                                 "a local branch name",
	"replay_conflict.phase":                                         "an enumeration",
	"replay_conflict.target_commit":                                 "matched against the commit pattern",
	"replay_conflict.paths[]":                                       "repository paths the replay stopped on, bounded in number",
	"integration_resumptions[].cause":                               "an enumeration",
	"integration_resumptions[].superseded_refusal.cause":            "an enumeration",
	"integration_resumptions[].superseded_refusal.provider_stop":    "an enumeration",
	"integration_resumptions[].superseded_refusal.round_charged_by": "a run id",
	"sweep_continuations[].cause":                                   "the pause cause, an enumeration",
	"retries[].boundary":                                            "a boundary from the harness's fixed vocabulary",
	"usage_limit_model":                                             "a model selector",
	"pause_cause":                                                   "an enumeration",
	"provider_outage_channel":                                       "an enumeration",
	"provider_stop":                                                 "an enumeration",
	"after_reply.outcome":                                           "an enumeration",
	"directive_pause.directive_id":                                  "a directive identifier",
	"directive_pause.kind":                                          "the directive's kind, from a fixed vocabulary",
	"dependency_pause.blockers[]":                                   "work item identifiers",
	"tracker_pause.boundary":                                        "a boundary from the harness's fixed vocabulary",
	"redeploy_stop.phase":                                           "an enumeration",
	"readopted.phase":                                               "an enumeration",
	"redeploy_stop.session_id":                                      "a watch session identifier, validated as one",
	"readopted.session_id":                                          "a watch session identifier, validated as one",
	"integration.target_branch":                                     "a local branch name",
	"integration.source_commit":                                     "a commit",
	"integration.target_commit":                                     "a commit",
	"integration.previous_target_commit":                            "a commit",
	"pull_request.remote":                                           "a remote name",
	"pull_request.branch":                                           "a local branch name",
	"pull_request.url":                                              "the forge's URL for the request",
	"pull_request.head_commit":                                      "matched against the commit pattern",
	"pull_request.state":                                            "the forge's state name",
	"pull_request.merge_method":                                     "an enumeration",
	"pull_request.merge_commit":                                     "a commit",
	"pull_request.checks.head_commit":                               "matched against the commit pattern",
	"pull_request.checks.failing[].paths[]":                         "repository paths a check annotated, bounded in number",
	"pull_request.checks.failing[].on_change[]":                     "repository paths the change touched, bounded in number",
	"pull_request.target_red.target_branch":                         "a local branch name",
	"pull_request.target_red.head_commit":                           "matched against the commit pattern",
	"pull_request.target_red.checks[].work_item":                    "a work item identifier",
}

var timeType = reflect.TypeOf(time.Time{})

// walkRecord calls visit with every string in value and its place in the
// record, descending through every nested record, pointer, and list. It reports
// through t any field of a kind it cannot see strings inside — a map, an
// interface, an array, an unexported field — because a string there would be one
// this test could not classify, which is the omission it exists to rule out.
func walkRecord(t *testing.T, value reflect.Value, key string, visit func(key string, text *string)) {
	t.Helper()
	switch value.Kind() {
	case reflect.String:
		visit(key, (*string)(value.Addr().UnsafePointer()))
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
	case reflect.Pointer:
		if !value.IsNil() {
			walkRecord(t, value.Elem(), key, visit)
		}
	case reflect.Slice:
		for index := 0; index < value.Len(); index++ {
			walkRecord(t, value.Index(index), key+"[]", visit)
		}
	case reflect.Struct:
		if value.Type() == timeType {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name == "" || name == "-" {
				t.Errorf("%s.%s carries no JSON name, so this test cannot say where it is in the record", key, field.Name)
				continue
			}
			if !field.IsExported() {
				t.Errorf("%s.%s is unexported, so this test cannot see strings inside it", key, field.Name)
				continue
			}
			place := name
			if key != "" {
				place = key + "." + name
			}
			walkRecord(t, value.Field(index), place, visit)
		}
	default:
		t.Errorf("%s is a %s, a kind of field this test cannot see strings inside; teach walkRecord it or make it a kind it knows", key, value.Kind())
	}
}

// populated fills value so that every nested record is present and every list
// holds one element, and every string holds a short piece of text: the record
// with every string it can carry actually there, which is what the walk and the
// bound are both asked about.
func populated(value reflect.Value) {
	switch value.Kind() {
	case reflect.String:
		value.SetString("x")
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		populated(value.Elem())
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		populated(value.Index(0))
	case reflect.Struct:
		if value.Type() == timeType {
			return
		}
		for index := 0; index < value.NumField(); index++ {
			if value.Type().Field(index).IsExported() {
				populated(value.Field(index))
			}
		}
	}
}

func populatedState() *State {
	var state State
	populated(reflect.ValueOf(&state).Elem())
	return &state
}

// Every string in the run record is either free text held to a bound or a
// structured value, and the test says which by walking the record rather than
// by naming the fields somebody remembered — through every nested record and
// list, so a string inside a refused amendment or a retry is asked about exactly
// as one on the record itself. Three work items bounded three fields one at a
// time, and each moved the unbounded case onto the next field nobody had
// listed; a string added to the record now fails here, at whatever depth, until
// it is bounded in State.recordedTexts or listed above as structured, and a kind
// of field the walk cannot see into fails here too.
func TestEveryStringInTheRunRecordIsBoundedOrStructured(t *testing.T) {
	t.Parallel()

	state := populatedState()
	bounded := make(map[*string]recordedText)
	boundedKeys := make(map[string]bool)
	for _, field := range state.recordedTexts() {
		if _, repeated := bounded[field.text]; repeated {
			t.Errorf("%s is bounded twice", field.path)
		}
		bounded[field.text] = field
		boundedKeys[field.key] = true
		if field.limit <= len(field.cutNote) {
			t.Errorf("%s is bounded to %d bytes, which its own cut note does not fit in", field.key, field.limit)
		}
	}

	seen := make(map[string]bool)
	walkRecord(t, reflect.ValueOf(state).Elem(), "", func(key string, text *string) {
		seen[key] = true
		recorded, isBounded := bounded[text]
		_, isStructured := structuredStrings[key]
		switch {
		case isBounded && recorded.key != key:
			// The entry has to bound the string it names, or the list is bounding
			// one string under another's name.
			t.Errorf("recordedTexts names %s but bounds %s", recorded.key, key)
		case isBounded && isStructured:
			t.Errorf("%s is both bounded and listed as structured; it is one or the other", key)
		case !isBounded && !isStructured:
			t.Errorf("%s is a string in the run record that is neither bounded in State.recordedTexts nor listed as structured", key)
		}
	})
	for key := range boundedKeys {
		if !seen[key] {
			t.Errorf("recordedTexts names %s, which is not a string in the run record", key)
		}
	}
	for key := range structuredStrings {
		if !seen[key] {
			t.Errorf("structuredStrings names %s, which is not a string in the run record", key)
		}
	}
}

// Each bounded string, nested ones included, is bounded where the rule has to
// hold: over its bound the record fails validation, and cut the record is
// exactly as valid as it was before, with the string still saying what it began
// with and that it was cut. It is asked of a record with every nested record
// present, so a bound that does not fit the nested record's own Validate —
// which would leave the store refusing what it had just cut — fails here.
func TestEveryBoundedStringIsRefusedOverItsBoundAndValidOnceCut(t *testing.T) {
	t.Parallel()

	baseline := errorText(populatedState().Validate())
	for index, field := range populatedState().recordedTexts() {
		t.Run(field.path, func(t *testing.T) {
			t.Parallel()

			state := populatedState()
			text := state.recordedTexts()[index].text
			const head = "what the record has to keep: "
			*text = head + strings.Repeat("x", field.limit)
			if errorText(state.Validate()) == baseline {
				t.Fatalf("an over-long %s does not fail validation", field.path)
			}
			state.boundRecordedTexts()
			if got := errorText(state.Validate()); got != baseline {
				t.Fatalf("a cut %s still fails validation: %s", field.path, strings.TrimPrefix(got, baseline))
			}
			assertCut(t, "cut", *text, field, head)
		})
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// A record handed to the store is not the store's to rewrite: cutting a nested
// string in place would rewrite the caller's copy through the pointer the two
// share.
func TestSavingAnOverlongNestedStringLeavesTheCallersRecordAlone(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	long := "the connection was reset: " + strings.Repeat("x", MaxRetryFailureBytes)
	state.Retries = []Retry{{Boundary: "developer", Attempt: 1, At: time.Now().UTC(), Failure: long}}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save() over-long retry failure error = %v, want it cut rather than refused", err)
	}
	if state.Retries[0].Failure != long {
		t.Fatalf("Save() rewrote the caller's retry failure to %d bytes", len(state.Retries[0].Failure))
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := loaded.Retries[0].Failure; len(got) > MaxRetryFailureBytes || !strings.HasPrefix(got, "the connection was reset: ") {
		t.Fatalf("the saved retry failure was not cut to its bound: %d bytes", len(got))
	}
}

// And each bounded string on the record itself is bounded through the store in
// both directions: it is cut on the way in rather than the record refused, and a
// record written before the bound existed still loads, with the string cut and
// saying so.
func TestEveryBoundedFieldIsCutOnWriteAndToleratedOnRead(t *testing.T) {
	t.Parallel()

	var probe State
	for _, field := range probe.recordedTexts() {
		t.Run(field.key, func(t *testing.T) {
			t.Parallel()

			store := newTestStore(t)
			state := validStateCarrying(t, field.key)
			if err := store.Create(state); err != nil {
				t.Fatalf("Create() error = %v", err)
			}
			const head = "what the record has to keep: "
			long := head + strings.Repeat("x", field.limit)
			text := func(s *State) *string {
				for _, candidate := range s.recordedTexts() {
					if candidate.key == field.key {
						return candidate.text
					}
				}
				t.Fatalf("%s is not among the recorded texts", field.key)
				return nil
			}

			*text(&state) = long
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), field.key+" is ") {
				t.Fatalf("Validate() over-long %s error = %v, want it named", field.key, err)
			}

			if err := store.Save(state); err != nil {
				t.Fatalf("Save() over-long %s error = %v, want it cut rather than refused", field.key, err)
			}
			saved, err := store.Load(state.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			assertCut(t, "saved", *text(&saved), field, head)

			// What a harness writing before the bound left on disk. It goes to the
			// file directly because every write the store offers cuts it.
			path, err := store.statePath(state.RunID)
			if err != nil {
				t.Fatalf("statePath() error = %v", err)
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if err := os.WriteFile(path, encoded, 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			historical, err := store.Load(state.RunID)
			if err != nil {
				t.Fatalf("Load() pre-bound record error = %v", err)
			}
			assertCut(t, "historical", *text(&historical), field, head)
		})
	}
}

func assertCut(t *testing.T, which, got string, field recordedText, head string) {
	t.Helper()
	if len(got) > field.limit {
		t.Fatalf("%s %s is %d bytes, over its %d byte bound", which, field.path, len(got), field.limit)
	}
	if !strings.HasPrefix(got, head) || !strings.HasSuffix(got, field.cutNote) {
		t.Fatalf("%s %s lost its head or did not say it was cut: %q", which, field.path, got)
	}
}

// validStateCarrying is a record on which the named field may be set at all,
// since a few of them are only coherent beside another.
func validStateCarrying(t *testing.T, key string) State {
	t.Helper()
	state := testState(t, StatusRunning)
	switch key {
	case "workflow_divergence":
		state.WorkflowInstanceID = "instance-1"
	case "landing_impediment_problem":
		state.LandingOutcome = LandingEvidence
		state.LandingReason = "the work is not doable yet"
	}
	return state
}
