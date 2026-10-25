package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The readers the accomplishment is read from are the terminal's own stores,
// held here to the model's interfaces so the dashboard and `yoyo status --spend
// --from` cannot be wired to different ones.
var (
	_ readmodel.AccomplishmentSpend         = (*runstate.SpendStore)(nil)
	_ readmodel.AccomplishmentRuns          = (*runstate.Store)(nil)
	_ readmodel.AccomplishmentConversations = (*runstate.ConversationStore)(nil)
	_ readmodel.Passes                      = (*runstate.SweepStore)(nil)
)

// accomplishmentSources opens every record what the spend accomplished is read
// from. A store that will not open costs the part of the reading read from it,
// and the reason travels with the gap.
func accomplishmentSources(stateRoot string, productID domain.ProductID) readmodel.AccomplishmentSources {
	sources := readmodel.AccomplishmentSources{}
	if store, err := runstate.NewSpendStore(stateRoot, productID); err != nil {
		sources.SpendProblem = err.Error()
	} else {
		sources.Spend = store
	}
	if store, err := runstate.NewStore(stateRoot, productID); err != nil {
		sources.RunsProblem = err.Error()
		sources.PassesProblem = err.Error()
	} else {
		sources.Runs = store
		sources.Passes = store.Sweeps()
	}
	if store, err := runstate.NewConversationStore(stateRoot, productID); err != nil {
		sources.ConversationsProblem = err.Error()
	} else {
		sources.Conversations = store
	}
	return sources
}

type accomplishmentOutput struct {
	Accomplishment readmodel.Accomplishment `json:"accomplishment"`
	statusHolds
}

// reportAccomplishment says what the spend over a range of days accomplished:
// each role's spend and what its passes led to, the same by week, what each
// review led to, and what every completed item cost in full.
func reportAccomplishment(ctx context.Context, stateRoot string, productID domain.ProductID, span readmodel.AccomplishmentRange, holds statusHolds, jsonOutput bool, stdout, stderr io.Writer) int {
	reading := readmodel.ReadAccomplishment(ctx, accomplishmentSources(stateRoot, productID), span)
	if jsonOutput {
		code := writeJSON(stdout, stderr, accomplishmentOutput{Accomplishment: reading, statusHolds: holds})
		if reading.Problem != "" && code == 0 {
			return 1
		}
		return code
	}
	if reading.Problem != "" {
		fmt.Fprintln(stderr, reading.Problem)
		return 1
	}
	printAccomplishment(stdout, reading)
	return 0
}

const (
	accomplishmentHeader = "%-21s %10s %6s %7s %10s %13s %9s %8s\n"
	accomplishmentRow    = "%-21s %10s %6d %7s %10s %13s %9s %8s\n"
	completedHeader      = "%-24s %10s %12s %9s %9s %11s %5s  %s\n"
	completedRow         = "%-24s %10s %12s %9s %9s %11s %5d  %s\n"
)

func printAccomplishment(writer io.Writer, reading readmodel.Accomplishment) {
	mark := ""
	if reading.Floor {
		mark = "≥ "
	}
	fmt.Fprintf(writer, "what the spend accomplished, %s to %s (%s): %s%s\n", reading.From, reading.To, reading.Zone, mark, dollars(reading.CostUSD))
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "by role; a management role's passes by what each led to, as a share of its passes:")
	printRoleAccomplishments(writer, reading.Roles)

	if len(reading.Weeks) > 0 {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "by week, Monday to Sunday, clipped to the range:")
		for _, week := range reading.Weeks {
			var roles []string
			for _, role := range week.Roles {
				roles = append(roles, fmt.Sprintf("%s %s", role.Role, dollars(role.CostUSD)))
			}
			spent := "nothing spent"
			if len(roles) > 0 {
				spent = dollars(week.CostUSD) + " (" + strings.Join(roles, ", ") + ")"
			}
			fmt.Fprintf(writer, "  %s to %s: %s; %s\n", week.From, week.To, spent, completedPhrase(week.Completed, week.CostPerCompletedItem))
		}
	}

	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "reviews: %d", len(reading.Reviews))
	var outcomes []string
	for _, share := range reading.ReviewOutcomes {
		if share.Count == 0 {
			continue
		}
		outcomes = append(outcomes, fmt.Sprintf("%d %s (%s, %s)", share.Count, share.Says, percent(share.Share), dollars(share.CostUSD)))
	}
	if len(outcomes) > 0 {
		fmt.Fprintf(writer, ": %s", strings.Join(outcomes, ", "))
	}
	fmt.Fprintln(writer)
	for _, review := range reading.Reviews {
		if review.Outcome != readmodel.ReviewPreventedDefect {
			continue
		}
		fmt.Fprintf(writer, "  prevented a defect: %s on %s, %d finding(s), %s\n", review.WorkItemID, review.At.In(readmodel.DeliveryZone()).Format("2006-01-02 15:04"), review.Findings, dollars(review.CostUSD))
	}

	fmt.Fprintln(writer)
	fmt.Fprintf(writer, "completed items: %s\n", completedPhrase(len(reading.Completed), reading.CostPerCompletedItem))
	if len(reading.Completed) > 0 {
		fmt.Fprintf(writer, completedHeader, "item", "total", "development", "review", "retries", "management", "runs", "title")
		for _, item := range reading.Completed {
			total := dollars(item.TotalUSD)
			if item.Unpriced > 0 {
				total = "≥ " + total
			}
			fmt.Fprintf(writer, completedRow, item.WorkItemID, total, dollars(item.DevelopmentUSD), dollars(item.ReviewUSD), dollars(item.RetriesUSD), dollars(item.ManagementUSD), item.Runs, item.Title)
		}
	}
	if reading.UnnamedPasses > 0 {
		fmt.Fprintf(writer, "\n%d management pass(es) costing %s named no work item, so that spend is in no item's cost above\n", reading.UnnamedPasses, dollars(reading.UnnamedManagementUSD))
	}
	if reading.Unpriced > 0 {
		fmt.Fprintf(writer, "\n%d invocation(s) in the range ended without the provider reporting their cost, so the figures are a floor\n", reading.Unpriced)
	}
	for _, problem := range reading.NotRead {
		fmt.Fprintf(writer, "not read: %s\n", problem)
	}
}

func printRoleAccomplishments(writer io.Writer, roles []readmodel.RoleAccomplishment) {
	fmt.Fprintf(writer, accomplishmentHeader, "role", "USD", "calls", "passes", "unblocked", "records only", "repeated", "nothing")
	for _, role := range roles {
		if !role.Management {
			fmt.Fprintf(writer, accomplishmentRow, role.Role, dollars(role.CostUSD), role.Invocations, "-", "-", "-", "-", "-")
			continue
		}
		shares := map[string]string{}
		for _, share := range role.Outcomes {
			shares[share.Outcome] = percent(share.Share)
		}
		fmt.Fprintf(writer, accomplishmentRow, role.Role, dollars(role.CostUSD), role.Invocations, fmt.Sprint(role.Passes),
			shares[string(readmodel.PassUnblocked)], shares[string(readmodel.PassChangedRecords)],
			shares[string(readmodel.PassRepeated)], shares[string(readmodel.PassChangedNothing)])
	}
	fmt.Fprintln(writer, "unblocked: acted on a work item that then progressed; records only: changed the tracker or a record and nothing it acted on progressed;")
	fmt.Fprintln(writer, "repeated: changed nothing, with the same inputs and account as an earlier pass of its task; nothing: left the tracker and records as they were")
}

func completedPhrase(count int, each float64) string {
	switch count {
	case 0:
		return "no item completed"
	case 1:
		return fmt.Sprintf("1 item completed, costing %s in full", dollars(each))
	}
	return fmt.Sprintf("%d items completed, costing %s each on average in full", count, dollars(each))
}

func dollars(amount float64) string {
	return fmt.Sprintf("$%.2f", amount)
}

func percent(share float64) string {
	return fmt.Sprintf("%.0f%%", share*100)
}
