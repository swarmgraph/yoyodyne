package orchestrator

// What a recurring pass was handed of the work waiting in its role's
// conversation, which of it the pass took, and the note on the item first in
// its order when the pass did not take that one.
//
// A pass is handed that work in backlog order, highest priority first and then
// oldest admitted, so the first item is the one the role was asked to reach
// first. A pass that leaves it and takes something else may be right to, but
// it has to leave a trace: without one, an item can sit first in the order of
// pass after pass and nobody can tell from the item, from the pass records, or
// from `yoyo status` whether the passes ran, failed, or took something else.
//
// # How taking is judged
//
// An item is taken where one of the pass's tracker actions changed it, or where
// its account names it in a finding the role did something about. It is left
// where neither holds, and where the account's "left" entries name it the role's
// reason is kept beside it; failing that, a finding the role marked "left"
// that names the item is its reason. A role that names an item in "left" and
// also acts on it in the tracker took it: the tracker is what happened.
//
// # When the note is written
//
// Only on a pass that answered with its account. A pass that delivered nothing,
// took no turn, or failed before giving an account leaves no note on any item:
// there was no passing-over to say, only a pass that did not run or did not
// finish, and its own record says which. The note is appended, never replaces
// what the item's notes already hold.

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// boundedDelivery is a reading's items held to what the record keeps. The
// listing a turn carries names no more than this, so nothing is cut in
// practice; the bound is here so a longer listing costs its tail rather than
// the pass's report.
func boundedDelivery(items []runstate.DeliveredWork) []runstate.DeliveredWork {
	var kept []runstate.DeliveredWork
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		if len(kept) == runstate.MaxSweepDelivered {
			break
		}
		kept = append(kept, runstate.DeliveredWork{ID: strings.TrimSpace(item.ID), Priority: max(item.Priority, 0)})
	}
	return kept
}

// judgeDelivered marks each delivered item taken or left, as the package
// comment says, with the role's reason against an item it left where its
// account gave one.
func judgeDelivered(delivered []runstate.DeliveredWork, actedOn []string, account *sweep.Result) []runstate.DeliveredWork {
	if len(delivered) == 0 {
		return nil
	}
	changed := make(map[string]bool, len(actedOn))
	for _, id := range actedOn {
		changed[strings.TrimSpace(id)] = true
	}
	judged := make([]runstate.DeliveredWork, len(delivered))
	for i, item := range delivered {
		item.Taken, item.Reason = false, ""
		switch {
		case changed[item.ID]:
			item.Taken = true
		case account == nil:
		default:
			reason, left := account.LeftReason(item.ID)
			if !left {
				var dealt bool
				dealt, reason = findingsAbout(account.Findings, item.ID)
				item.Taken = dealt
			}
			if !item.Taken {
				item.Reason = boundedLeftReason(reason)
			}
		}
		judged[i] = item
	}
	return judged
}

// findingsAbout reads the findings for one item: whether one the role did
// something about names it, and otherwise what the first one it left says.
func findingsAbout(findings []sweep.Finding, id string) (bool, string) {
	reason := ""
	for _, finding := range findings {
		named := namesItem(finding.Issue, id) || namesItem(finding.Detail, id)
		for _, filed := range finding.Filed {
			named = named || strings.TrimSpace(filed) == id
		}
		if !named {
			continue
		}
		if finding.Disposition != sweep.DispositionLeft {
			return true, ""
		}
		if reason == "" {
			reason = strings.TrimSpace(finding.Issue)
			if detail := strings.TrimSpace(finding.Detail); detail != "" {
				reason += ": " + detail
			}
		}
	}
	return false, reason
}

// namesItem reports whether text names the item by its identifier as a whole
// word, so "yoyodyne-ifd.414.1" is not found inside "yoyodyne-ifd.414.12". A
// full stop after it is the end of a sentence where nothing follows it but a
// space or the end of the text.
func namesItem(text, id string) bool {
	if id == "" {
		return false
	}
	for from := 0; ; {
		at := strings.Index(text[from:], id)
		if at < 0 {
			return false
		}
		start, end := from+at, from+at+len(id)
		before := start == 0 || !identifierByte(text[start-1])
		after := end == len(text) || !identifierByte(text[end]) ||
			(text[end] == '.' && (end+1 == len(text) || !identifierByte(text[end+1])))
		if before && after {
			return true
		}
		from = start + 1
	}
}

func identifierByte(b byte) bool {
	return b == '-' || b == '.' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// maxPassedOverReasonBytes is how much of the role's reason the record and the
// note carry. The account bounds a "left" reason well under it; a finding's
// issue and detail together can run longer, and are cut rather than refused.
const maxPassedOverReasonBytes = 1 << 10

func boundedLeftReason(reason string) string {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) <= maxPassedOverReasonBytes {
		return reason
	}
	const marker = " […]"
	cut := maxPassedOverReasonBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return strings.TrimSpace(reason[:cut]) + marker
}

// notePassedOver writes the note on the item first in the pass's order where
// the pass answered with its account and did not take that item, and returns
// what the pass's record should say where the note could not be written.
func (t Trigger) notePassedOver(ctx context.Context, pass string, role domain.AgentRole, recorded runstate.Sweep) string {
	first, handed := recorded.FirstDelivered()
	if !handed || first.Taken || recorded.Turns == 0 || recorded.Result == nil {
		return ""
	}
	// A trigger wired without a tracker still records the item as not taken,
	// which is what the record exists to say; only the note is left unwritten.
	if t.ItemNotes == nil {
		return ""
	}
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if err := t.ItemNotes.AppendNote(write, first.ID, passedOverNote(pass, role, recorded, first)); err != nil {
		return fmt.Sprintf("%s was first in the order this pass was handed and the pass did not take it, and the note saying so could not be written on the item: %v", first.ID, err)
	}
	return ""
}

// passedOverNote is the note the item carries: which pass, when, where the
// item stood in what the pass was handed, and the role's reason or that it
// gave none. It is read by a person opening the item, so it says the pass by
// the name `yoyo sweeps` lists it under and the time in local time with the
// zone named.
func passedOverNote(pass string, role domain.AgentRole, recorded runstate.Sweep, first runstate.DeliveredWork) string {
	reason := first.Reason
	if reason == "" {
		reason = "no reason given"
	}
	where := fmt.Sprintf("was first of the %d items it was handed", len(recorded.Delivered))
	took := "; it took none of the others"
	switch taken := takenCount(recorded.Delivered); {
	case len(recorded.Delivered) == 1:
		where, took = "was the only item it was handed", ""
	case taken > 0:
		took = fmt.Sprintf("; it took %d of the others", taken)
	}
	return fmt.Sprintf(
		"Passed over by the %s's recurring pass %s at %s: this item (P%d) %s from the work waiting in the %s's conversation, and the pass did not take it%s. Reason: %s.",
		role.Title(), pass, recorded.StartedAt.Local().Format("15:04 MST on January 2, 2006"),
		first.Priority, where, role.Title(), took, strings.TrimSuffix(reason, "."))
}

func takenCount(delivered []runstate.DeliveredWork) int {
	taken := 0
	for _, item := range delivered {
		if item.Taken {
			taken++
		}
	}
	return taken
}
