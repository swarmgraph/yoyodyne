package orchestrator

// A watch session's holds whose cause a check can confirm, and the check
// runner that confirms it.
//
// Two of the session's holds have a cause an environment check can confirm:
// checks the machine refused to start (machineChecksHold), most often an Xcode
// license nobody has accepted, and a provider whose login lapsed. When the
// session places one for a cause it recognises, it writes a follow-up carrying
// the matching check and then waits on that follow-up alone. It starts no run
// to find out and asks the provider nothing itself: the check runner, run once
// at the top of every poll, is the one schedule that re-checks either cause
// (docs/designs/follow-ups.md, "What ends a follow-up"). When the runner finds
// the check passed it closes the follow-up with the time, and the session
// releases the hold, records that it did and why, and says so on the watch log
// `yoyo status` reads and in the channel.
//
// A cause the session does not recognise gets no follow-up, and its hold keeps
// the behaviour it had before: a stop no check can confirm is not one the
// runner can release.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/followup"
	"github.com/mason-bryant/yoyodyne/internal/followup/checkrun"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ScheduleFollowUps is the check runner and the follow-ups it keeps. It is
// satisfied by *checkrun.Runner. It is optional, and a session wired without it
// holds as it did before follow-ups existed.
type ScheduleFollowUps interface {
	Attach(followup.FollowUp) (followup.FollowUp, error)
	Load(id string) (followup.FollowUp, error)
	OpenAbout(hold string) (followup.FollowUp, bool, error)
	Round(ctx context.Context, now time.Time) checkrun.Round
}

// attachChecksFollowUp writes the follow-up for a hold over checks the machine
// could not run, where the cause is one the harness recognises, and returns the
// hold waiting on it. A hold whose follow-up could not be written is said on
// the schedule and keeps its trial run.
func (s Scheduler) attachChecksFollowUp(schedule *Schedule, held machineChecksHold) machineChecksHold {
	if s.FollowUps == nil {
		return held
	}
	check, recognised := checkrun.Recognise(held.detail)
	if !recognised {
		return held
	}
	hold := checkrun.HoldID(checkrun.HoldChecksCouldNotRun, held.runID)
	about := []followup.Subject{{Kind: followup.SubjectRun, ID: held.runID}}
	if held.workItemID != "" {
		about = append(about, followup.Subject{Kind: followup.SubjectWorkItem, ID: held.workItemID})
	}
	written, err := s.FollowUps.Attach(checkrun.HoldFollowUp(hold, check, about, s.now()))
	if err != nil {
		schedule.FollowUpProblem = fmt.Sprintf("the follow-up for the hold run %s placed could not be written, so the hold starts one run to find out instead: %v", held.runID, err)
		return held
	}
	held.followUp, held.hold, held.check = written.ID, hold, checkrun.Says(check)
	return held
}

// loginFollowUp is a lapsed login waiting on its follow-up, writing the
// follow-up where the outage has none yet. It reports false where the runner
// cannot take the login on: no runner, an outage that is not a login, or one
// naming no provider. The outage is then asked about as before.
func (s Scheduler) loginFollowUp(schedule *Schedule, outage runstate.ProviderOutage) (runstate.ProviderOutage, bool) {
	if s.FollowUps == nil || !loginLapsed(outage) {
		return runstate.ProviderOutage{}, false
	}
	hold := checkrun.HoldID(checkrun.HoldProviderLogin, string(outage.Provider))
	if _, open, err := s.FollowUps.OpenAbout(hold); open {
		return outage, true
	} else if err != nil {
		schedule.FollowUpProblem = fmt.Sprintf("some follow-ups could not be read: %v", err)
	}
	check := checkrun.EnvironmentCheck(followup.EnvironmentProviderLogin, string(outage.Provider))
	if _, err := s.FollowUps.Attach(checkrun.HoldFollowUp(hold, check, nil, s.now())); err != nil {
		schedule.FollowUpProblem = fmt.Sprintf("the follow-up for %s's login could not be written, so the session asks the provider itself: %v", outage.Provider, err)
		return runstate.ProviderOutage{}, false
	}
	return outage, true
}

func loginLapsed(outage runstate.ProviderOutage) bool {
	return outage.Cause == domain.ProviderUnauthenticated && outage.Provider != ""
}

// followUpRound runs the check runner's round and releases every hold of the
// session's own whose follow-up ended.
func (s Scheduler) followUpRound(ctx context.Context, schedule *Schedule, session *watchSession, pull Pull, checksHeld *machineChecksHold) {
	if s.FollowUps == nil {
		return
	}
	now := s.now()
	round := s.FollowUps.Round(ctx, now)
	if len(round.Problems) > 0 {
		schedule.FollowUpProblem = strings.Join(round.Problems, "; ")
	}
	for _, passed := range round.Passed {
		for _, hold := range passed.Holds() {
			if !checkrun.HarnessMayLift(hold) {
				continue
			}
			switch {
			case checksHeld.followUp == passed.ID:
				s.release(schedule, session, fmt.Sprintf("runs are being started again: the harness found that %s at %s, so it released the hold run %s placed on new work (follow-up %s)",
					checksHeld.check, localTime(passed.Passed.At), checksHeld.runID, passed.ID))
				*checksHeld = machineChecksHold{}
			case strings.HasPrefix(hold, checkrun.HoldProviderLogin+":") && pull.Outages != nil:
				if _, _, err := pull.Outages.Clear(); err != nil {
					schedule.OutageProblem = fmt.Sprintf("the provider's login passed its check and the outage could not be cleared: %v", err)
					continue
				}
				schedule.ProviderOutage = nil
				s.release(schedule, session, fmt.Sprintf("runs are being started again: the harness found that %s at %s, so it released the hold the lapsed login placed on new work (follow-up %s)",
					checkrun.Says(passed.Check), localTime(passed.Passed.At), passed.ID))
			}
		}
	}
	// A follow-up its owner, whoever waits, or the operator closed with a reason
	// ends the wait as well; the hold is released and the reason said.
	if checksHeld.followUp == "" {
		return
	}
	standing, err := s.FollowUps.Load(checksHeld.followUp)
	if err != nil {
		schedule.FollowUpProblem = fmt.Sprintf("the follow-up for the hold run %s placed could not be read: %v", checksHeld.runID, err)
		return
	}
	if standing.State == followup.StateClosed {
		s.release(schedule, session, fmt.Sprintf("runs are being started again: the follow-up for the hold run %s placed on new work was closed by its %s at %s: %s",
			checksHeld.runID, standing.Closed.By, localTime(standing.Closed.At), standing.Closed.Reason))
		*checksHeld = machineChecksHold{}
	}
}

// release records a hold of the session's own released, on the schedule and
// on the watch log, where the channel is told of it.
func (s Scheduler) release(schedule *Schedule, session *watchSession, reason string) {
	schedule.FollowUpReleased = append(schedule.FollowUpReleased, reason)
	session.released(reason)
	if s.Log != nil {
		fmt.Fprintln(s.Log, reason)
	}
}

// released records the session choosing work again because the harness
// released a hold it had placed itself.
func (w *watchSession) released(reason string) {
	if w.to == nil {
		return
	}
	w.state, w.said = runstate.WatchWatching, account{reason: reason}
	w.record(SessionState{State: runstate.WatchResumed, Reason: reason, Released: true, Draining: w.drain})
}

func localTime(at time.Time) string {
	return at.Local().Format("Jan 2 15:04 MST")
}
