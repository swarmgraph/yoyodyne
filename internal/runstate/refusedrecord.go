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
// said as that. Validation stays exactly as strict either way: the record is
// refused, never skipped and never repaired.

import (
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"strings"

	harnessbuild "github.com/mason-bryant/yoyodyne/internal/buildinfo"
)

// UnknownValueError is a recorded field holding a value outside the vocabulary
// this build knows.
type UnknownValueError struct {
	Field string
	Value string
}

func (e UnknownValueError) Error() string {
	return fmt.Sprintf("%s is invalid: %q is not a value this build knows", e.Field, e.Value)
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
