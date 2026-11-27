package execution

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// A program that makes a temporary file "the ordinary way" on macOS does not
// ask $TMPDIR. The system's mktemp, with no template or with -t, makes its file
// in the per-user folder under /var/folders that confstr(3) names, whatever
// TMPDIR says, and a Java program's java.io.tmpdir is that same folder. A
// developer session's sandbox grants neither that folder nor anything under
// it, and granting it would open a folder every process the user runs shares,
// so a project's own check failed there with "mkdtemp failed on
// /var/folders/...: Operation not permitted" while the harness's check stage,
// which runs outside the sandbox, passed.
//
// So the harness points both at a folder inside the run's own scratch folder,
// which the sandbox already grants: a mktemp found ahead of the system's on the
// session's PATH that adds that folder where the caller named no place, and
// JAVA_TOOL_OPTIONS carrying -Djava.io.tmpdir. Nothing becomes writable that
// was not, and the folder goes when the run's scratch folder does
// (RemoveScratchDirectory). Everything that does read TMPDIR is left to the
// TMPDIR the provider sets.

// scratchTemporaryName is the folder inside a run's scratch folder that
// temporary files made the ordinary way land in.
const scratchTemporaryName = "tmp"

// scratchProgramsName is the folder inside a run's scratch folder holding the
// programs the harness puts on the session's PATH.
const scratchProgramsName = "bin"

// ScratchTemporary is where a developer session's temporary files go: Folder,
// inside the run's scratch folder, and Programs, the folder holding the mktemp
// that sends them there.
type ScratchTemporary struct {
	Folder   string
	Programs string
}

// PrepareScratchTemporary makes the temporary folder and the mktemp program
// inside the scratch folder cut for runID, after confirming it is that folder
// (ConfirmScratchDirectory). Both writes go through the confined writer with
// the scratch folder as their root. Preparing the same run again rewrites the
// program and keeps whatever the run left in the temporary folder.
func PrepareScratchTemporary(scratch, runID, repositoryRoot string) (ScratchTemporary, error) {
	if err := ConfirmScratchDirectory(scratch, runID, repositoryRoot); err != nil {
		return ScratchTemporary{}, err
	}
	// A single quote would end the quoted path the program carries, and a
	// double quote would end the one JAVA_TOOL_OPTIONS carries.
	if strings.ContainsAny(scratch, "'\"\n") {
		return ScratchTemporary{}, fmt.Errorf("the scratch directory %q for run %s has a quote or a line break in its path, so no temporary folder can be named inside it", scratch, runID)
	}
	root, err := repowrite.NewRoot(scratch)
	if err != nil {
		return ScratchTemporary{}, fmt.Errorf("confine the temporary folder for run %s to its scratch directory: %w", runID, err)
	}
	folder, err := prepareDirectory(root, scratchTemporaryName)
	if err != nil {
		return ScratchTemporary{}, fmt.Errorf("create the temporary folder for run %s: %w", runID, err)
	}
	programs, err := prepareDirectory(root, scratchProgramsName)
	if err != nil {
		return ScratchTemporary{}, fmt.Errorf("create the program folder for run %s: %w", runID, err)
	}
	pinned, err := repowrite.OpenPinnedRoot(programs)
	if err != nil {
		return ScratchTemporary{}, fmt.Errorf("open the program folder for run %s: %w", runID, err)
	}
	defer pinned.Close()
	if err := pinned.WriteFile("mktemp", []byte(mktempProgram(folder)), 0o755, false); err != nil {
		return ScratchTemporary{}, fmt.Errorf("write the mktemp program for run %s: %w", runID, err)
	}
	if err := pinned.Unchanged(); err != nil {
		return ScratchTemporary{}, err
	}
	return ScratchTemporary{Folder: folder, Programs: programs}, nil
}

// Environment is environment with Programs first on PATH and java.io.tmpdir
// set to Folder. A JAVA_TOOL_OPTIONS already there keeps its options, after
// this one, so an option the project set itself still wins.
func (t ScratchTemporary) Environment(environment []string) []string {
	javaOption := `-Djava.io.tmpdir="` + t.Folder + `"`
	out := make([]string, 0, len(environment)+2)
	sawPath, sawJava := false, false
	for _, entry := range environment {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "PATH":
			sawPath = true
			if value == "" {
				entry = "PATH=" + t.Programs
			} else {
				entry = "PATH=" + t.Programs + string(filepath.ListSeparator) + value
			}
		case "JAVA_TOOL_OPTIONS":
			sawJava = true
			entry = "JAVA_TOOL_OPTIONS=" + strings.TrimSpace(javaOption+" "+value)
		}
		out = append(out, entry)
	}
	if !sawPath {
		out = append(out, "PATH="+t.Programs+string(filepath.ListSeparator)+"/usr/bin:/bin:/usr/sbin:/sbin")
	}
	if !sawJava {
		out = append(out, "JAVA_TOOL_OPTIONS="+javaOption)
	}
	return out
}

// mktempProgram is a shell script standing in for mktemp. Where the caller
// named a template or a folder, it runs the next mktemp on PATH exactly as
// asked: a relative template still lands in the working directory. Where it
// named neither -- `mktemp`, `mktemp -d`, `mktemp -t prefix` -- it adds
// `-p folder`, which both the BSD and the GNU mktemp read as where to put the
// file.
func mktempProgram(folder string) string {
	return `#!/bin/sh
# Written by Yoyodyne for this developer run. macOS mktemp puts a file made
# with no template in a folder the run's sandbox may not write, whatever
# TMPDIR says, so this sends it to the run's own temporary folder instead.
folder='` + folder + `'
self=$(cd "$(dirname "$0")" && pwd -P)
real=
saved_ifs=$IFS
IFS=:
for directory in $PATH; do
	[ -n "$directory" ] || continue
	resolved=$(cd "$directory" 2>/dev/null && pwd -P) || continue
	[ "$resolved" = "$self" ] && continue
	if [ -x "$directory/mktemp" ]; then
		real="$directory/mktemp"
		break
	fi
done
IFS=$saved_ifs
[ -n "$real" ] || real=/usr/bin/mktemp
placed=no
templated=no
expect_value=no
options_done=no
for argument in "$@"; do
	if [ "$expect_value" = yes ]; then
		expect_value=no
		continue
	fi
	if [ "$options_done" = yes ]; then
		templated=yes
		continue
	fi
	case $argument in
	--) options_done=yes ;;
	--tmpdir|--tmpdir=*|-p*) placed=yes ;;
	--*) ;;
	-*)
		flags=${argument#-}
		while [ -n "$flags" ]; do
			flag=${flags%"${flags#?}"}
			flags=${flags#?}
			case $flag in
			p)
				placed=yes
				[ -n "$flags" ] || expect_value=yes
				flags=
				;;
			t)
				[ -n "$flags" ] || expect_value=yes
				flags=
				;;
			esac
		done
		;;
	*)
		templated=yes
		options_done=yes
		;;
	esac
done
if [ "$placed" = no ] && [ "$templated" = no ]; then
	mkdir -p "$folder" 2>/dev/null
	exec "$real" -p "$folder" "$@"
fi
exec "$real" "$@"
`
}
