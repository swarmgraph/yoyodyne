package cli

import (
	"io"
	"strings"
)

// commandHelp renders everything the executable prints when asked for help: the
// command list, and each command's own usage. It is assembled from the same
// functions the commands themselves print with rather than from a second copy of
// the text, so help that goes stale here is help that has gone stale everywhere.
//
// It exists because the product manager is given a description of the surfaces
// the product ships and cannot run a command to find out what they are. A
// command with no usage function of its own -- init and version, which take
// flags and nothing else -- is named in the command list above and not repeated.
func commandHelp() string {
	printers := []func(io.Writer){
		printUsage,
		printSetupUsage,
		printChatUsage,
		printAgentUsage,
		printRoleUsage,
		printConfigUsage,
		printArtifactUsage,
		printAmendmentUsage,
		printEvaluationUsage,
		printGoalsUsage,
		printStaleUsage,
		printConformanceUsage,
		printInvariantUsage,
		printDirectiveUsage,
		printInterventionUsage,
		printExchangeUsage,
		printReportsUsage,
		printSweepsUsage,
		printRunUsage,
		printWorkUsage,
		printStartUsage,
		printStopUsage,
		printTriageUsage,
		printStatusUsage,
		printPauseUsage,
		printResumeUsage,
		printReleaseUsage,
		printGateUsage,
		printReviewUsage,
		printCostUsage,
		printReconcileUsage,
		printSlackUsage,
		printDoctorUsage,
		printDashboardUsage,
		printStateRootUsage,
		printProjectUsage,
	}
	var rendered strings.Builder
	for index, print := range printers {
		if index > 0 {
			rendered.WriteString("\n")
		}
		print(&rendered)
	}
	return rendered.String()
}
