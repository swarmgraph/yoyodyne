package beads

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSchedulingWaitUpdatesOneValueWithoutEditingNotes(t *testing.T) {
	at := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	first, _ := NextSchedulingWait(nil, "waiting behind Review changes (yoyodyne-2)", at)
	second, _ := NextSchedulingWait(first, "waiting behind Check changes (yoyodyne-3)", at.Add(time.Hour))
	encode := func(wait *SchedulingWait) string {
		metadata := map[string]string{}
		if wait != nil {
			value, _ := json.Marshal(wait)
			metadata[schedulingWaitKey] = string(value)
		}
		data, _ := json.Marshal(map[string]any{"id": "yoyodyne-1", "title": "Earlier work", "status": "open", "notes": "Earlier notes stay here", "metadata": metadata})
		return "[" + string(data) + "]"
	}
	runner := &fakeRunner{responses: []string{encode(nil), encode(first), encode(first), encode(first), encode(second), encode(second), encode(nil)}}
	client := Client{Runner: runner, Binary: "bd-test", Dir: "/repo"}
	for _, step := range []struct {
		reason string
		at     time.Time
		want   *SchedulingWait
	}{
		{first.Reason, at, first}, {first.Reason, at.Add(time.Minute), first},
		{second.Reason, at.Add(time.Hour), second}, {"", at.Add(2 * time.Hour), nil},
	} {
		item, err := client.RecordSchedulingWait(context.Background(), "yoyodyne-1", step.reason, step.at)
		if err != nil {
			t.Fatal(err)
		}
		if item.Notes != "Earlier notes stay here" {
			t.Fatal("notes changed")
		}
		if (step.want == nil) != (item.SchedulingWait == nil) || (step.want != nil && *item.SchedulingWait != *step.want) {
			t.Fatalf("wait = %+v, want %+v", item.SchedulingWait, step.want)
		}
	}
	writes := 0
	for _, args := range runner.args {
		if args[0] == "update" {
			writes++
		}
		for _, arg := range args {
			if strings.Contains(arg, "notes=") {
				t.Fatalf("edited notes: %v", args)
			}
		}
	}
	if writes != 3 {
		t.Fatalf("writes = %d, want first wait, replaced reason and clear", writes)
	}
	if !second.FirstPassedOver.Equal(first.FirstPassedOver) || !second.ReasonSince.Equal(at.Add(time.Hour)) {
		t.Fatalf("wait clocks = %+v", second)
	}
}
