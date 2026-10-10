package chat

import "testing"

// The work a reply admitted is every item it put in the queue, once each:
// its admitted proposals and the creations the tracker carried out, and
// nothing the tracker refused or that was not a creation.
func TestAdmittedWorkIsEveryItemTheReplyPutInTheQueueOnce(t *testing.T) {
	t.Parallel()

	reply := Reply{
		Admitted: []AdmittedItem{{ProposalID: "p1", WorkItemID: "yoyodyne-ifd.500"}},
		Actions: []TrackerOutcome{
			{Action: TrackerAction{Action: actionCreate}, Applied: true, WorkItemID: "yoyodyne-ifd.500"},
			{Action: TrackerAction{Action: actionCreate}, Applied: true, WorkItemID: "yoyodyne-ifd.501"},
			{Action: TrackerAction{Action: actionCreate}, Applied: false},
			{Action: TrackerAction{Action: "close", ID: "yoyodyne-ifd.9"}, Applied: true, WorkItemID: "yoyodyne-ifd.9"},
		},
	}
	got := reply.AdmittedWork()
	if len(got) != 2 || got[0] != "yoyodyne-ifd.500" || got[1] != "yoyodyne-ifd.501" {
		t.Fatalf("AdmittedWork() = %v, want the two items put in the queue, once each", got)
	}
	if got := (Reply{}).AdmittedWork(); len(got) != 0 {
		t.Errorf("AdmittedWork() of a reply that admitted nothing = %v", got)
	}
}

// The work a reply acted on is every existing item its applied tracker actions
// changed, once each: reading, surveying, creating, and a refused action are
// not acting on waiting work.
func TestActedOnWorkIsEveryItemTheReplyChanged(t *testing.T) {
	t.Parallel()

	reply := Reply{
		Actions: []TrackerOutcome{
			{Action: TrackerAction{Action: actionRead, ID: "yoyodyne-ifd.414.1"}, Applied: true, WorkItemID: "yoyodyne-ifd.414.1"},
			{Action: TrackerAction{Action: actionSurvey}, Applied: true},
			{Action: TrackerAction{Action: actionCreate}, Applied: true, WorkItemID: "yoyodyne-ifd.501"},
			{Action: TrackerAction{Action: actionUpdate, ID: "yoyodyne-ab2"}, Applied: true, WorkItemID: "yoyodyne-ab2"},
			{Action: TrackerAction{Action: actionClose, ID: "yoyodyne-ab2"}, Applied: true, WorkItemID: "yoyodyne-ab2"},
			{Action: TrackerAction{Action: actionClose, ID: "yoyodyne-gtx"}, Applied: false, WorkItemID: "yoyodyne-gtx"},
			{Action: TrackerAction{Action: actionPark, ID: "yoyodyne-ifd.428.75.1"}, Applied: true, WorkItemID: "yoyodyne-ifd.428.75.1"},
		},
	}
	got := reply.ActedOnWork()
	if len(got) != 2 || got[0] != "yoyodyne-ab2" || got[1] != "yoyodyne-ifd.428.75.1" {
		t.Fatalf("ActedOnWork() = %v, want the two items changed, once each", got)
	}
}
