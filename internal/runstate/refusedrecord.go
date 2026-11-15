package runstate

// What a reader says when it refuses a stored record.
//
// A process runs the build it was started from while builds land behind it, and
// a newer build can write a value an older one has never heard of. Before the
// dashboard was a supervised part it ran an old build for a day after a deploy
// and refused every run record that carried a newer stop class, with nothing
// but "invalid run state: stop_class is invalid" — which reads as a damaged
// record and sends whoever reads it looking for corruption that is not there.
//
// So a refused record is named with the field and the value that refused it,
// and beside them the builds that bear on whose value it is: the build reading
// it, the build deployed on disk, and the build that dispatched the run. A
// reader on an older build than the one deployed is the demonstrated case of
// incompatibility, and is said as that, with the remedy; a reader that is the
// deployed build and still refuses the value is reading invalid data, and is
// said as that. Validation stays exactly as strict either way for a write and for
// a reading that precedes acting on the record: the record is refused, never
// repaired. A reading for showing (Store.Read) is the one exception, and only for
// an unknown value: it keeps the record with the value marked unknown, because a
// listing that refused it is a surface with the run missing from it.

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	harnessbuild "github.com/mason-bryant/yoyodyne/internal/buildinfo"
)

// UnknownValueError is a recorded field holding a value outside the vocabulary
// this build knows.
//
// It is the one refusal a reading for showing reads past (Store.Read): a value
// this build does not know is most often one a newer build writes, so a listing
// keeps the record and marks the value unknown rather than losing the run. A
// write, and a reading that precedes acting on the run, still refuse it.
type UnknownValueError struct {
	Field string `json:"field"`
	Value string `json:"value"`
}

func (e UnknownValueError) Error() string {
	return fmt.Sprintf("%s is invalid: %q is not a value this build knows", e.Field, e.Value)
}

// Shown is the value as a surface prints it: unknown, with the value the record
// holds, so a reader is neither told a meaning this build cannot give it nor
// left without what was written.
func (e UnknownValueError) Shown() string {
	return UnknownValueShown(e.Value)
}

// ValueShown is one of a run record's values as a surface prints it: as written
// where this build knows it, and unknown with what was recorded where the
// reading found this build does not (State.UnknownValues).
func (s State) ValueShown(field, value string) string {
	for _, unknown := range s.UnknownValues {
		if unknown.Field == field && unknown.Value == value {
			return unknown.Shown()
		}
	}
	return value
}

// UnknownValueShown is how every surface prints a recorded value this build
// does not know.
func UnknownValueShown(value string) string {
	return fmt.Sprintf("unknown to this build (recorded as %q)", value)
}

// fieldPath is a wrapper's prefix that names where in a record a problem was
// found, such as review_finding_details[0] — as opposed to one that only says
// what kind of problem it is, such as "invalid run state".
var fieldPath = regexp.MustCompile(`^[a-z0-9_]+(\[[0-9]+\])?(\.[a-z0-9_]+(\[[0-9]+\])?)*$`)

// onlyUnknownValues reports the values a refusal names that this build does not
// know, and whether those are the whole of it. A refusal that also says
// anything else — a missing field, a contradiction, a record of another product
// — is not one a reading for showing may read past, because only an unknown
// value is explained by the record having been written by another build.
//
// A refusal is walked as validation builds it: joined problems, each possibly
// wrapped with the name of the part of the record it was found in, which is
// kept in front of the field so the value is named where it sits.
func onlyUnknownValues(err error) ([]UnknownValueError, bool) {
	if err == nil {
		return nil, false
	}
	if unknown, ok := err.(UnknownValueError); ok {
		return []UnknownValueError{unknown}, true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var found []UnknownValueError
		for _, problem := range joined.Unwrap() {
			values, only := onlyUnknownValues(problem)
			if !only {
				return nil, false
			}
			found = append(found, values...)
		}
		return found, len(found) > 0
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		inner := wrapped.Unwrap()
		values, only := onlyUnknownValues(inner)
		if !only {
			return nil, false
		}
		if inner != nil {
			if prefix, cut := strings.CutSuffix(err.Error(), inner.Error()); cut {
				if part := strings.TrimSuffix(prefix, ": "); fieldPath.MatchString(part) {
					for index := range values {
						values[index].Field = part + "." + values[index].Field
					}
				}
			}
		}
		return values, true
	}
	return nil, false
}

// RecordBuilds is which builds bear on a refused record.
type RecordBuilds struct {
	// Reader is the build of the process that refused the record, Deployed the
	// build of the binary on disk it was started from, and Writer the build the
	// record names as having made it. Each is empty where it is not known.
	Reader   string
	Deployed string
	Writer   string
}

// ReadingBuilds is this process's build and the build deployed over it, as far
// as each can be read.
func ReadingBuilds() RecordBuilds {
	builds := RecordBuilds{Reader: harnessbuild.Commit()}
	if executable, err := os.Executable(); err == nil {
		if info, err := buildinfo.ReadFile(executable); err == nil {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					builds.Deployed = setting.Value
				}
			}
		}
	}
	return builds
}

// RefusedRecordError is a stored record a reader refused, named with what
// refused it and the builds that bear on whose value that was.
type RefusedRecordError struct {
	// Record is what was refused, as a path or an identifier.
	Record  string
	Problem error
	Builds  RecordBuilds
}

func (e RefusedRecordError) Unwrap() error { return e.Problem }

func (e RefusedRecordError) Error() string {
	said := fmt.Sprintf("the record %s was refused: %v", e.Record, e.Problem)
	var unknown UnknownValueError
	if !errors.As(e.Problem, &unknown) {
		return said
	}
	return said + ". " + e.Builds.verdict(unknown)
}

// verdict says whose value an unknown one is, as far as the builds show.
func (b RecordBuilds) verdict(unknown UnknownValueError) string {
	var named []string
	if b.Reader != "" {
		named = append(named, "this process runs build "+shortBuild(b.Reader))
	} else {
		named = append(named, "this process's build recorded no revision")
	}
	if b.Deployed != "" {
		named = append(named, "the binary on disk is build "+shortBuild(b.Deployed))
	}
	if b.Writer != "" {
		named = append(named, "the record was made by build "+shortBuild(b.Writer))
	}
	builds := strings.Join(named, ", ") + "."
	builds = strings.ToUpper(builds[:1]) + builds[1:]
	switch {
	case b.Reader != "" && b.Deployed != "" && b.Reader != b.Deployed:
		return fmt.Sprintf("%s This process is older than the build deployed over it, so %s %q is most likely a value the deployed build writes and this one predates: an incompatibility between builds, not damaged data. Restarting this process onto the deployed build reads it.", builds, unknown.Field, unknown.Value)
	case b.Reader != "" && b.Reader == b.Deployed:
		return fmt.Sprintf("%s This process is the deployed build and does not know %s %q, so the record holds invalid data rather than a newer build's value.", builds, unknown.Field, unknown.Value)
	case b.Writer != "" && b.Writer != b.Reader:
		return fmt.Sprintf("%s The record was made by another build, so %s %q may be that build's value; nothing here shows the record is damaged.", builds, unknown.Field, unknown.Value)
	default:
		return fmt.Sprintf("%s Which build wrote %s %q cannot be shown from here.", builds, unknown.Field, unknown.Value)
	}
}
