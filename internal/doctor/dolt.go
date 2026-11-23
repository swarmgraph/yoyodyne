package doctor

import (
	"fmt"
	"runtime"

	"github.com/mason-bryant/yoyodyne/internal/dolttool"
)

const doltCheck = "dolt"

// checkDolt asks whether the dolt the tracker's server mode needs is in the
// harness's tools folder, at the version it wants. It is a warning when it is
// not: until it is installed the store is opened directly, and no run stops.
func (d *diagnosis) checkDolt(repository string) Finding {
	finding := Finding{Check: doltCheck, Status: StatusWarning}
	root, err := d.stateRootPath()
	if err != nil {
		finding.Summary = fmt.Sprintf("whether dolt %s is installed could not be asked: the state root is not known", dolttool.Version)
		finding.Detail = err.Error()
		finding.Remedy = "export YOYODYNE_STATE_HOME=$HOME/.local/state/yoyodyne"
		return finding
	}
	const direct = "; the tracker's store is opened directly until it is installed"
	found, err := dolttool.Inspect(root)
	switch {
	case err != nil:
		finding.Summary = fmt.Sprintf("the harness's dolt could not be read, and dolt %s is wanted%s", dolttool.Version, direct)
		finding.Detail = err.Error()
	case found.State == dolttool.Installed:
		return Finding{Check: doltCheck, Status: StatusOK, Summary: fmt.Sprintf("dolt %s is installed for the harness", dolttool.Version), Detail: found.Detail}
	case found.State == dolttool.OtherVersion:
		finding.Summary = fmt.Sprintf("the harness's dolt is version %s, and dolt %s is wanted%s", found.Version, dolttool.Version, direct)
		finding.Detail = found.Detail
	case found.State == dolttool.Changed:
		finding.Summary = fmt.Sprintf("the harness's dolt is not the one setup installed, and dolt %s is wanted%s", dolttool.Version, direct)
		finding.Detail = found.Detail
	default:
		finding.Summary = fmt.Sprintf("dolt %s is not in the harness's tools folder%s", dolttool.Version, direct)
		finding.Detail = found.Detail
	}
	if dolttool.Checksum(d.env.GOOS, runtime.GOARCH) == "" {
		finding.Detail += fmt.Sprintf("; this build holds no checksum for dolt %s on %s/%s, so setup cannot install it, and the command below prints the published ones for a later build", dolttool.Version, d.env.GOOS, runtime.GOARCH)
		finding.Remedy = dolttool.ChecksumsCommand
		return finding
	}
	finding.Remedy = "yoyo setup --directory " + shellQuote(repository)
	return finding
}
