package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type decidingConversations struct{ recorded []runstate.Conversation }

func (c decidingConversations) Recorded() ([]runstate.Conversation, error) { return c.recorded, nil }
func (decidingConversations) InFlight(runstate.ConversationIdentity) (bool, error) {
	return false, nil
}

type decidingAmendments struct{ records []amendment.Record }

func (a decidingAmendments) List() ([]amendment.Record, error) { return a.records, nil }

// The channel puts each decision on the operator's list to him with its
// account: what changes, why, what approving and declining each lead to, and
// why it is his — a document, a proposed change, and a concern alike — so he
// can decide from the answer without asking any role what it means.
func TestTheChannelsStandingSaysWhatEachDecisionOfTheOperatorsChanges(t *testing.T) {
	t.Parallel()

	goals := amendment.Proposal{
		SchemaVersion: 1, ID: "amendment-0000000000000000000000000000000a", Role: domain.RoleDeveloper, Agent: "developer",
		RunID: "run-1", WorkItemID: "yoyodyne-ifd.432.5", ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		Artifact: "v1-goals", Kind: artifact.KindGoals, Owner: domain.RoleProductManager,
		Change: "The legibility goal names the dashboard.", Why: "The page counts by it.", RaisedAt: time.Now().Add(-time.Hour),
	}
	sink, _, posts := newSteeringSink(t, testOperator)
	sink.sources = &readmodel.Sources{
		Amendments: decidingAmendments{records: []amendment.Record{{Proposal: &goals}}},
		Conversations: decidingConversations{recorded: []runstate.Conversation{{
			ConversationID: "chat-1", Agent: "product-manager", Role: domain.RoleProductManager,
			PendingWrites: []runstate.PendingWrite{{
				ID: "document-4.1", Turn: 4, Action: "create", Artifact: "v2-goals", Kind: "goals", Title: "V2 goals",
				Directory: "docs/product/goals", Body: "# V2 goals\n\n- [hosted] Hosted teams.\n", Reason: "the operator asked for the next goals",
			}},
			PendingConcerns: []runstate.PendingConcern{{
				ID: "c5.1", Turn: 5, Kind: "conflict", Subject: "Drop review", Detail: "it would land unreviewed work",
				Question: "Should review be optional?",
			}},
		}}},
	}
	sink.steering.handle(context.Background(), topLevel(testOperator, "<@"+testApp+"> status", "1750000001.000100"))

	answer := onlyPost(t, posts).Text
	for _, said := range []string{
		"What changes: adds a new goals document, v2-goals (\"V2 goals\"), under docs/product/goals",
		"After: # V2 goals",
		"Why: the operator asked for the next goals",
		"If you approve: it is published",
		"What changes: a change to v1-goals: The legibility goal names the dashboard.",
		"If you decline: v1-goals stays as it is",
		"What changes: whether the " + domain.RoleProductManager.Title() + " goes on to propose: Drop review",
		"Why it is yours: the work would cut against a goal",
		"Why it is yours: it states the product's intent",
	} {
		if !strings.Contains(answer, said) {
			t.Errorf("the channel's answer does not say %q:\n%s", said, answer)
		}
	}
}
