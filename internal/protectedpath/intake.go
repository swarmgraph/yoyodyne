package protectedpath

// The section of the configuration no grant reaches.
//
// A work item may grant a run the project configuration file, or the whole
// configuration directory, so the run can change an ordinary setting there. The
// `intake` section is the exception: it says which source the harness reads
// messages from, which rules decide what they start, and who is asked about
// them, and it is the operator's alone (scheduled-message-intake, section 1). A
// grant is written by a role, and the reason the section is his is that no role
// may repoint a query or send its questions to him, so a grant of the file does
// not reach it.
//
// A path cannot say that, because the section shares its file with settings a
// grant does reach. So the section is compared as configuration: decoded from
// the copy at the run's base and the copy in the change, and refused where the
// two differ, whatever the item grants. Decoding rather than comparing lines is
// what lets a run reformat or reorder the file around the section without being
// refused, and what stops it changing the section by an anchor or alias defined
// elsewhere in the file.

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// IntakeSection is the configuration key whose value no grant reaches.
const IntakeSection = "intake"

// IntakeConfigurationFiles are the repository-relative files a project's
// configuration can be read from, in the slash form a changed path is compared
// in. The file before the configuration directory existed is named too: the
// section is the operator's in whichever file a project keeps it.
var IntakeConfigurationFiles = []string{config.DirectoryName + "/" + config.FileName, config.LegacyFileName}

// MaxIntakeConfigurationBytes bounds how much of one copy of the configuration
// file is read to compare its intake section. A copy past it cannot be shown to
// leave the section alone, so it is refused rather than passed.
const MaxIntakeConfigurationBytes = 1 << 20

// IntakeInstruction is what a developer whose change altered the intake
// section, or an item's author reading why a run stopped, is told. It says who
// does change the section, for the reason the role-definition refusal does: a
// developer told only "no" tries the same change in other words.
const IntakeInstruction = "No grant reaches the " + IntakeSection + " section of the project configuration: it says where the harness reads messages from, which rules decide what they start, and who is asked about them, and it is the operator's alone. " +
	"A grant of the configuration file or directory lets a run change other settings in that file, never this section. Put the section back exactly as it was at the start of your run, keep any other change the item asked for, and say in your summary what the section should say and that the operator makes that change by editing the file himself."

// IntakeChanges names the settings in the intake section that differ between
// two copies of one configuration file, decoded as configuration. A nil copy is
// a file that is not there, which holds no intake section; an absent section
// and an empty one are the same configuration, so neither is a change.
//
// Each name is a dotted path into the section, such as
// "intake.queries.slack-bugs" for one query, or "intake.limits" for a setting
// beside the queries, so the refusal says which part of the section moved. An
// error means one of the copies could not be decoded, and so could not be shown
// to leave the section alone; the caller refuses the change on it.
func IntakeChanges(base, change []byte) ([]string, error) {
	before, err := intakeSection(base)
	if err != nil {
		return nil, fmt.Errorf("the copy at the run's base: %w", err)
	}
	after, err := intakeSection(change)
	if err != nil {
		return nil, fmt.Errorf("the copy in the change: %w", err)
	}
	var changed []string
	differences(IntakeSection, before, after, 2, &changed)
	sort.Strings(changed)
	return changed, nil
}

// intakeSection decodes one copy of a configuration file and returns its intake
// section, or nil where the file or the section is missing or empty.
func intakeSection(content []byte) (any, error) {
	if content == nil {
		return nil, nil
	}
	var document map[string]any
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("it could not be read as configuration: %w", err)
	}
	return empty(document[IntakeSection]), nil
}

// empty folds an empty mapping into nil, so `intake: {}` and no section at all
// compare equal, as the configuration reads them.
func empty(value any) any {
	if mapping, ok := value.(map[string]any); ok && len(mapping) == 0 {
		return nil
	}
	return value
}

// differences appends the dotted name of each part of two values that differs,
// descending into mappings on both sides for at most depth further levels: deep
// enough to name one query, and no deeper, because the query is the unit the
// operator reads in the file.
func differences(name string, before, after any, depth int, changed *[]string) {
	before, after = empty(before), empty(after)
	if reflect.DeepEqual(before, after) {
		return
	}
	beforeMap, beforeIsMap := asMapping(before)
	afterMap, afterIsMap := asMapping(after)
	if depth == 0 || !beforeIsMap || !afterIsMap {
		*changed = append(*changed, name)
		return
	}
	keys := map[string]bool{}
	for key := range beforeMap {
		keys[key] = true
	}
	for key := range afterMap {
		keys[key] = true
	}
	for key := range keys {
		differences(name+"."+key, beforeMap[key], afterMap[key], depth-1, changed)
	}
}

// asMapping reads a decoded value as a mapping, treating a missing value as an
// empty one so a query added where there were none is named as that query.
func asMapping(value any) (map[string]any, bool) {
	if value == nil {
		return map[string]any{}, true
	}
	mapping, ok := value.(map[string]any)
	return mapping, ok
}

// ErrIntakeUnreadable marks a copy of the configuration file that could not be
// read at all — not an ordinary file, or larger than is read — as distinct from
// one that was read and could not be decoded.
var ErrIntakeUnreadable = errors.New("the file could not be read")

// DescribeIntakeProblem is the line a refusal carries for a configuration file
// whose intake section could not be compared, so the developer is told why
// rather than only that the section was refused.
func DescribeIntakeProblem(file string, err error) string {
	return fmt.Sprintf("the %s section of %s could not be compared with the run's base, so it is refused: %s", IntakeSection, file, strings.TrimSpace(err.Error()))
}
