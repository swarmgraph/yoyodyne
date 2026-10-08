package execution

import (
	"os"
	"strings"
)

// A `yoyo` the harness starts on its own schedule says so in its environment.
//
// Most verbs are typed by a person or run by an agent, and AgentRoleVariable
// tells those two apart. A few are also run by the harness itself: the
// supervisor's maintenance pass runs `yoyo reconcile` every few minutes. A verb
// that records the operator's hand step — a reconcile is one when a person runs
// it — has to tell that pass from a person, or every pass would be counted as
// the operator settling runs by hand. This is how: the harness marks the process
// it starts, and the verb records nothing where it finds the mark.
//
// A launchd job of the product's own is marked by launchd rather than by the
// harness. launchd sets XPC_SERVICE_NAME to the job's label in every process it
// starts for the job, and every label the product has had is under
// jobLabelPrefix: the launch agent that starts the supervisor, and the
// operator's maintenance job com.yoyodyne.maintenance, which ran `yoyo
// reconcile` every ten minutes from a script nothing here can mark. The
// supervisor retires that job when it starts (internal/maintenancejob), and
// until it has, the job's reconcile is still a schedule rather than a person.
//
// Like the role marker it identifies rather than guards. It decides only what
// is counted, never what is permitted.

// StartedByVariable names what started a `yoyo` process when the harness did,
// such as the supervisor's maintenance pass. It is under the harness's own
// prefix, so it is carried through to what that process starts in turn.
const StartedByVariable = "YOYODYNE_STARTED_BY"

// LaunchdJobVariable is where launchd names the job a process was started for.
// A terminal has it too, naming the terminal application or set to 0, which is
// why only the product's own labels are read as a mark.
const LaunchdJobVariable = "XPC_SERVICE_NAME"

// jobLabelPrefix begins the label of every launchd job the product has
// installed or retired: com.yoyodyne.supervisor.<product> and
// com.yoyodyne.maintenance.
const jobLabelPrefix = "com.yoyodyne."

// WithStartedBy returns environment marked as started by what is named. A nil
// environment is this process's own, which is what a command with no explicit
// environment would have inherited; a mark already there is replaced.
func WithStartedBy(environment []string, by string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	kept := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if strings.HasPrefix(entry, StartedByVariable+"=") {
			continue
		}
		kept = append(kept, entry)
	}
	return append(kept, StartedByVariable+"="+by)
}

// StartedBy reports what the harness says started the process environment
// describes, and whether it says anything. A nil environment asks about this
// process's own. An empty mark is no mark. The harness's own mark is read
// first; where there is none, a launchd job of the product's own is named by
// its label.
func StartedBy(environment []string) (string, bool) {
	if environment == nil {
		environment = os.Environ()
	}
	job := ""
	for _, entry := range environment {
		name, value, named := strings.Cut(entry, "=")
		if !named {
			continue
		}
		switch name {
		case StartedByVariable:
			if by := strings.TrimSpace(value); by != "" {
				return by, true
			}
		case LaunchdJobVariable:
			if label := strings.TrimSpace(value); strings.HasPrefix(label, jobLabelPrefix) {
				job = "the launchd job " + label
			}
		}
	}
	return job, job != ""
}
