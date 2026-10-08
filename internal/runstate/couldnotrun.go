package runstate

// CheckCouldNotRun is one configured check that said it could not run at all,
// by the convention checks.CouldNotRunPrefix describes, and the reason it gave.
// Such a check judged nothing about the change: it spent no repair attempt and
// stopped nothing, and it is recorded so the run says which check it went
// without and why.
type CheckCouldNotRun struct {
	Command string `json:"command"`
	Reason  string `json:"reason"`
}

// Says is the check and its reason in the words every surface uses.
func (c CheckCouldNotRun) Says() string {
	return c.Command + " could not run: " + c.Reason
}
