package maystart

// Path is one way the harness starts work. The paths are the closed list below;
// one that is not on it is one the decision cannot judge.
type Path string

const (
	ReadyWork       Path = "ready-work"
	NamedRun        Path = "named-run"
	HoldTest        Path = "hold-test"
	DecidedRerun    Path = "decided-rerun"
	DecidedRepair   Path = "decided-repair"
	FinishMerge     Path = "finish-merge"
	ContinueRun     Path = "continue-run"
	PickUpRun       Path = "pick-up-run"
	FeedbackRepair  Path = "feedback-repair"
	PublishDocument Path = "publish-document"
	BringUpToDate   Path = "bring-up-to-date"
	ResolveConflict Path = "resolve-conflict"
	BringInFile     Path = "bring-in-file"
	UpdateQueued    Path = "update-queued"
	UpdateForPerson Path = "update-for-person"
	AfterLogin      Path = "after-login"
	RecurringPass   Path = "recurring-pass"
	PromptedPass    Path = "prompted-pass"
	UnmetGoalReview Path = "unmet-goal-review"
	MessagesForWork Path = "messages-for-work"
)

// PathEntry is one path on the closed list.
type PathEntry struct {
	Path Path
	// Says is the path in plain words, for whoever is told about it.
	Says string
	// Wired is whether the code that starts work this way asks Decide first.
	Wired bool

	starts startedBy
}

// startedBy is what a path starts, which is what says which rule judges it.
type startedBy int

const (
	startsRun startedBy = iota + 1
	startsDocument
	startsPass
	startsGoalReview
	startsMessageReading
)

// Paths is the closed list. Each call returns its own copy.
func Paths() []PathEntry {
	return []PathEntry{
		{Path: ReadyWork, Says: "the scheduler pulling ready work", Wired: true, starts: startsRun},
		{Path: NamedRun, Says: "a run the operator names", Wired: true, starts: startsRun},
		{Path: HoldTest, Says: "the test run that checks whether a hold can lift", Wired: true, starts: startsRun},
		{Path: DecidedRerun, Says: "a fresh run the development manager decides after a stop", Wired: true, starts: startsRun},
		{Path: DecidedRepair, Says: "a repair the development manager decides", Wired: true, starts: startsRun},
		{Path: FinishMerge, Says: "finishing an interrupted merge", starts: startsRun},
		{Path: ContinueRun, Says: "continuing a silent run or one whose checks ran out of time", starts: startsRun},
		{Path: PickUpRun, Says: "picking up a waiting or interrupted run", starts: startsRun},
		{Path: FeedbackRepair, Says: "a repair from feedback on a pull request", Wired: true, starts: startsRun},
		{Path: PublishDocument, Says: "publishing a document", starts: startsDocument},
		{Path: BringUpToDate, Says: "the attempt to bring a handed back change up to date", Wired: true, starts: startsRun},
		{Path: ResolveConflict, Says: "the run that resolves a conflict with the team's branch", Wired: true, starts: startsRun},
		{Path: BringInFile, Says: "the run that brings in a pinned outside file", Wired: true, starts: startsRun},
		{Path: UpdateQueued, Says: "bringing a change queued to merge on GitHub up to date with its target branch", starts: startsRun},
		{Path: UpdateForPerson, Says: "bringing a change that waits for a person to merge it up to date with its target branch", starts: startsRun},
		{Path: AfterLogin, Says: "taking up the checks of a run that stopped because GitHub refused the harness's login", starts: startsRun},
		{Path: RecurringPass, Says: "a role's recurring pass", Wired: true, starts: startsPass},
		{Path: PromptedPass, Says: "a pass started by an event, a label or a request", Wired: true, starts: startsPass},
		{Path: UnmetGoalReview, Says: "the review of unmet goals", Wired: true, starts: startsGoalReview},
		{Path: MessagesForWork, Says: "reading outside messages for work", Wired: true, starts: startsMessageReading},
	}
}

func (p Path) starts() startedBy {
	for _, entry := range Paths() {
		if entry.Path == p {
			return entry.starts
		}
	}
	return 0
}
