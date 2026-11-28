package readmodel

// RenderReports is the one line `yoyo status` prints about the unhandled
// reports: how many, for whom, which no pass reads, and the command that reads
// them (docs/designs/reports-nobody-reads.md, Decision 1). It is printed in
// every project whenever any report stands unhandled, and not at all otherwise;
// a pile that could not be read is said by the attention line's own problem.
func (s Standing) RenderReports() string {
	if s.ReportsSays == "" {
		return ""
	}
	return "reports: " + s.ReportsSays + "\n"
}
