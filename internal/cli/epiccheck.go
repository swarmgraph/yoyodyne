package cli

import "github.com/mason-bryant/yoyodyne/internal/epiccheck"

// epicChecks makes the check of the whole epic (internal/epiccheck) for this
// product. The reviewer session that makes it, and when the harness starts one,
// are built by the next piece of the design; until then a check asked of it is
// refused for want of a session, after the one decision has been asked.
type epicChecks struct {
	parts components
}

func (e epicChecks) checker() (epiccheck.Checker, error) {
	store, err := e.parts.epicProgress()
	if err != nil {
		return epiccheck.Checker{}, err
	}
	return epiccheck.Checker{Progress: store, MayStart: e.parts.wouldStart()}, nil
}
