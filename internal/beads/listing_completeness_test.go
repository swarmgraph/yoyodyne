package beads

import (
	"context"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestEveryWholeWorkListingRefusesAnUnknownCapAndRecordsTheCount(t *testing.T) {
	for _, verb := range []string{"ready", "list"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			runner := &fakeRunner{results: []execution.ProcessResult{
				{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "Error: unknown flag: --limit"},
				{Status: execution.ProcessSucceeded, Stdout: `[{"id":"yoyodyne-1"},{"id":"yoyodyne-2"}]`},
			}}
			record := &listingRecord{}
			client := Client{Runner: runner, Listings: record}
			var items []WorkItem
			var err error
			if verb == "ready" {
				items, err = client.Ready(context.Background())
			} else {
				items, err = client.List(context.Background(), "")
			}
			if items != nil || err == nil {
				t.Fatalf("possibly cut list accepted: %v, %v", items, err)
			}
			for _, want := range []string{"could not lift its default limit", "read 2 work item(s)", "list may be cut", "work may be missing"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error lacks %q: %v", want, err)
				}
			}
			if len(record.failed) != 1 || record.failed[0] != err.Error() || record.answered != 0 {
				t.Fatalf("record = %+v, want one failure carrying the count", record)
			}
		})
	}
}

func TestWholeWorkListingsKeepOtherDiagnosticsAndExactDefaultSizedLists(t *testing.T) {
	for _, verb := range []string{"ready", "list"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			var rows []string
			for range 100 {
				rows = append(rows, `{"id":"yoyodyne-1"}`)
			}
			runner := &fakeRunner{results: []execution.ProcessResult{{Status: execution.ProcessSucceeded, Stdout: "[" + strings.Join(rows, ",") + "]", Stderr: "Warning: update available"}}}
			client := Client{Runner: runner}
			items, err := client.workListing(context.Background(), verb, nil)
			if err != nil || len(items) != 100 {
				t.Fatalf("a whole list at the old default was refused: %d items, %v", len(items), err)
			}
		})
	}
}

func TestWorkListingsReportCutOutputForEveryStatus(t *testing.T) {
	for _, status := range []string{"ready", "", "open", "blocked", "in_progress", "closed", "deferred"} {
		for _, mode := range []string{"rows", "bytes", "json"} {
			t.Run(status+"-"+mode, func(t *testing.T) {
				t.Parallel()
				result := execution.ProcessResult{
					Status: execution.ProcessSucceeded,
					Stdout: `[{"id":"yoyodyne-1"},{"id":"yoyodyne-2"}]`,
					Stderr: "Showing 2 issues; more results matched but were hidden by --limit. Use --limit 0 for all, or --limit N to raise the cap.",
				}
				if status == "ready" {
					result.Stderr = "Showing 2 of 120 ready issues. Use --limit 0 for all, or --limit N to raise the cap."
				}
				if mode != "rows" {
					result.Stderr = ""
					result.Stdout = strings.TrimSuffix(result.Stdout, "]") + `,{"id":"yoyodyne-3"`
					if mode == "bytes" {
						result.OutputTruncation = "output cut at the bound"
					}
				}
				record := &listingRecord{}
				client := Client{Runner: &fakeRunner{results: []execution.ProcessResult{result}}, Listings: record}
				var items []WorkItem
				var err error
				if status == "ready" {
					items, err = client.Ready(context.Background())
				} else {
					items, err = client.List(context.Background(), status)
				}
				if items != nil || err == nil {
					t.Fatalf("cut output accepted: %v, %v", items, err)
				}
				for _, want := range []string{"cut", "read 2 complete work item(s)", "work may be missing"} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("error lacks %q: %v", want, err)
					}
				}
				if len(record.failed) != 1 || record.failed[0] != err.Error() || record.answered != 0 {
					t.Fatalf("record = %+v, want one failure carrying the count", record)
				}
			})
		}
	}
}
