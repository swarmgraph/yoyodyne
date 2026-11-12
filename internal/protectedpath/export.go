package protectedpath

import "sort"

// The paths the harness itself put in the worktree.
//
// A worktree is given the primary checkout's copy of the tracker's export, so a
// run reads the work around its own rather than the copy its base commit
// happened to carry, and that copy is then held out of the change with Git's
// skip-worktree bit. The hold is what keeps one derived file from turning
// parallel development into a queue of merge conflicts: every run is given its
// own refreshed copy, and two runs that both committed one conflict against each
// other over a file neither was asked to write.
//
// The bit alone is not a guarantee. It lives in the worktree's index under
// `.git`, a developer's sandbox grants writes there, and one
// `git update-index --no-skip-worktree` turns the refreshed export into an
// ordinary modification that the harness then commits and promotes. Nothing
// re-checked it: the worktree was proven clean when it was created and never
// again. So the export is refused in a developer's diff by the same gate that
// refuses an upstream artifact home, and what holds is a check made against the
// change rather than an index bit surviving whatever ran in the worktree. That
// matters most for the case the read-only posture already takes seriously — an
// agent following injected instructions is exactly who would flip the bit.
//
// Reading the change rather than the bit is what covers the states a bit check
// would pass. Staging the refreshed copy and then setting the hold again leaves
// an index Git still reports as held, while the commit carries the staged blob
// regardless — so the guard has to be a comparison against the run's base
// commit, which for a held path is a comparison against the index and catches
// both spellings of the same smuggle.
//
// A grant lifts this refusal exactly as it lifts the other, because a grant is
// item text authored and reviewed before the run starts, which is the one thing
// the flipped bit is not. A gate with no stated way out is a gate agents work
// around, and the way out here is the same one every other refused path has.

// ExportInstruction is what a developer whose change contains a held export is
// told. It names the mechanism rather than only the path, because the file is
// one the harness put there: a developer told it may not change a file it never
// wrote, and not why the file exists, has nothing to act on but deleting it.
const ExportInstruction = "One of the refused paths is a file the harness copied into your worktree from the checkout the worktree was cut from, so that you read the work around your own rather than the copy your base commit carried. " +
	"It is derived from a store outside Git that is authoritative for it, nothing a run writes reaches that store, and every run is given its own copy — so the same file changed by two runs is a merge conflict between them rather than a contribution. " +
	"Git is told to leave that path alone in this worktree, which is why it is normally in no diff at all; its being in yours means the hold was lifted, whether or not you meant to lift it. " +
	"Put the file back to the content your run's base commit carries and leave it alone; you can read that content with `git show <base commit>:<path>`. The same comparison runs again on your next attempt, so it is the change rather than the index bit that has to be right."

// HeldExports is what this set holds out of a run's change, in a stable order,
// so a refusal can say which paths are the harness's own rather than the
// project's documents.
func (s Set) HeldExports() []string {
	return append([]string(nil), s.exports...)
}

// HeldExportsAmong reports which of these paths are exports this set holds out
// of the change. It is what tells a refusal which of the two rules it caught,
// and an empty result is the ordinary answer.
func (s Set) HeldExportsAmong(paths []string) []string {
	var held []string
	for _, candidate := range paths {
		clean, ok := normalize(candidate)
		if !ok || !within(clean, s.exports) {
			continue
		}
		held = appendUnique(held, clean)
	}
	sort.Strings(held)
	return held
}
