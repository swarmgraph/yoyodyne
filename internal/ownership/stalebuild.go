package ownership

// Who moves a watch session left on an older build than the one that should be
// running, as a closed vocabulary. The Slack sink works out which cause stands
// (slack.residentDeliveries) from the supervisor's record and the repository,
// and asks here who moves it, so it never decides on its own that the operator
// has a step.

// KindStaleBuild is the watch session running a build older than what has
// landed. It is not an entry on the attention line: the Slack sink says it,
// and asks the registry who moves it.
const KindStaleBuild Kind = "stale-build"

// StaleBuildCause is why the watch session is not on a build carrying every
// landing that changes what gets built.
type StaleBuildCause string

const (
	// StaleBuildMoveWaiting is a newer build installed and the session holding
	// its own until the least time between two moves is up
	// (execution.build_move_interval). It moves by itself, by rule.
	StaleBuildMoveWaiting StaleBuildCause = "move-waiting"
	// StaleBuildMoveDue is a move that has fallen due and that the session has
	// not begun, inside the grace after which the supervisor restarts it onto
	// the installed build itself.
	StaleBuildMoveDue StaleBuildCause = "move-due"
	// StaleBuildMoveStuck is a move past the moment the supervisor was to
	// restart the session itself, with the session still on its old build.
	StaleBuildMoveStuck StaleBuildCause = "move-stuck"
	// StaleBuildNotBuilt is a landing that changes what gets built and that the
	// supervisor has not built and installed within the time a build takes: the
	// build is failing on the main line.
	StaleBuildNotBuilt StaleBuildCause = "not-built"
	// StaleBuildUncommitted is the same, where the checkout the binary is built
	// from has uncommitted changes to what gets built, so the supervisor will
	// not build until they are committed or set aside.
	StaleBuildUncommitted StaleBuildCause = "uncommitted"
	// StaleBuildUnsupervised is no supervisor running, so nothing builds or
	// installs a landing at all.
	StaleBuildUnsupervised StaleBuildCause = "unsupervised"
)

// StaleBuildCauses is the whole vocabulary.
func StaleBuildCauses() []StaleBuildCause {
	return []StaleBuildCause{StaleBuildMoveWaiting, StaleBuildMoveDue, StaleBuildMoveStuck, StaleBuildNotBuilt, StaleBuildUncommitted, StaleBuildUnsupervised}
}

// ResolveStaleBuild is who moves a stale build of one cause.
func ResolveStaleBuild(cause StaleBuildCause) Answer {
	return Resolve(KindStaleBuild, Record{StaleBuild: cause})
}

// staleBuildRule is who moves a stale build. None of the causes is on the
// operator's closed list, so none is his. A move the session or the supervisor
// makes by itself is the harness's. A session that has not moved even after
// the supervisor's own deadline, and a main line that does not build, are the
// development manager's, who decides what becomes of a part or a line the
// harness cannot get moving. Uncommitted changes in the checkout are somebody's
// work in progress that no role may touch; the closed list has no reason for
// them, so they are the Lead Product Manager's to classify, as every entry the
// list does not cover is.
func staleBuildRule(r Record) (Answer, bool) {
	switch r.StaleBuild {
	case StaleBuildMoveWaiting:
		return owned(MoverHarness, "nothing: the session moves onto the installed build by itself once the least time between two moves is up", "")
	case StaleBuildMoveDue:
		return owned(MoverHarness, "nothing: the supervisor restarts the session onto the installed build if it has not moved by itself", "")
	case StaleBuildMoveStuck:
		return owned(MoverDevelopmentManager, "find why the session has not moved onto the installed build, which the supervisor was to restart it onto", "health.observe")
	case StaleBuildNotBuilt:
		return owned(MoverDevelopmentManager, "have the main line's build fixed, so the supervisor can build and install it", "triage decision")
	case StaleBuildUncommitted:
		return owned(MoverProductManager, "have whoever made the uncommitted changes in the checkout commit them or set them aside, so the supervisor can build", "")
	case StaleBuildUnsupervised:
		return owned(MoverHarness, "start the product's supervisor, which builds and installs each landing", "yoyo start")
	}
	return Answer{}, false
}
