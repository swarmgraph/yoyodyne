package beads

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func testPassFirst() PassFirst {
	return PassFirst{
		Role:   domain.RoleDevelopmentManager,
		By:     domain.RoleProductManager,
		At:     time.Date(2026, 11, 27, 19, 13, 0, 0, time.UTC),
		Reason: "lift the design hold by Saturday",
	}
}

func itemWithPassFirst(t *testing.T, request *PassFirst) string {
	t.Helper()
	metadata := map[string]string{}
	if request != nil {
		value, err := request.encode()
		if err != nil {
			t.Fatal(err)
		}
		metadata[passFirstKey] = value
	}
	encoded, err := json.Marshal(map[string]any{
		"id": "yoyodyne-9k1y.22", "title": "t", "status": "open", "priority": 0,
		"issue_type": "task", "metadata": metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// A request to take an item first in a role's next pass is written to the
// tracker whole and read back, an ending writes it empty, and a write bd did
// not keep is a failure in either direction.
func TestARequestToTakeAnItemFirstIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	request := testPassFirst()
	runner := &fakeRunner{responses: []string{"[" + itemWithPassFirst(t, &request) + "]"}}
	item, err := (Client{Runner: runner}).Update(context.Background(), "yoyodyne-9k1y.22", WorkItemChange{PassFirst: &request})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	wrote := ""
	for _, arg := range runner.args[0] {
		if strings.HasPrefix(arg, "--set-metadata="+passFirstKey+"=") {
			wrote = arg
		}
	}
	if !strings.Contains(wrote, `"role":"development-manager"`) || !strings.Contains(wrote, "lift the design hold by Saturday") {
		t.Fatalf("the update wrote %q, want the whole request under %s", wrote, passFirstKey)
	}
	if got := item.FirstInPassOf(domain.RoleDevelopmentManager); got == nil || !got.At.Equal(request.At) {
		t.Fatalf("FirstInPassOf() = %#v, want the request read back", got)
	}
	if item.FirstInPassOf(domain.RoleArchitect) != nil {
		t.Fatal("a request to the development manager reads as one to the architect")
	}

	ended := PassFirst{}
	clearing := &fakeRunner{responses: []string{"[" + itemWithPassFirst(t, nil) + "]"}}
	if _, err := (Client{Runner: clearing}).Update(context.Background(), "yoyodyne-9k1y.22", WorkItemChange{PassFirst: &ended}); err != nil {
		t.Fatalf("Update() ending error = %v", err)
	}
	if !contains(clearing.args[0], "--set-metadata="+passFirstKey+"=") {
		t.Fatalf("the ending wrote no request: %#v", clearing.args[0])
	}
	stuck := &fakeRunner{responses: []string{"[" + itemWithPassFirst(t, &request) + "]"}}
	if _, err := (Client{Runner: stuck}).Update(context.Background(), "yoyodyne-9k1y.22", WorkItemChange{PassFirst: &ended}); err == nil ||
		!strings.Contains(err.Error(), "still named first") {
		t.Fatalf("Update() ending a request bd kept = %v, want it reported", err)
	}
	unstored := &fakeRunner{responses: []string{"[" + itemWithPassFirst(t, nil) + "]"}}
	if _, err := (Client{Runner: unstored}).Update(context.Background(), "yoyodyne-9k1y.22", WorkItemChange{PassFirst: &request}); err == nil {
		t.Fatal("Update() with a request bd did not store = nil error, want a failure")
	}
}

// A request is refused for a role whose pass reads no single queue, a closed
// item carries none, and the listing line says who, when and why.
func TestARequestToTakeAnItemFirstIsHeldToWhatAPassCanCarry(t *testing.T) {
	t.Parallel()

	toProgramManager := testPassFirst()
	toProgramManager.Role = domain.RoleProgramManager
	if err := (WorkItemChange{PassFirst: &toProgramManager}).validate(); err == nil {
		t.Fatal("a request to a program manager's pass was accepted")
	}
	request := testPassFirst()
	if (WorkItem{Status: "closed", PassFirst: &request}).FirstInPassOf(domain.RoleDevelopmentManager) != nil {
		t.Fatal("a closed item is still named first")
	}
	if got := passFirstIn(map[string]json.RawMessage{passFirstKey: json.RawMessage(`"{\"role\":\"developer\"}"`)}); got != nil {
		t.Fatalf("passFirstIn() = %#v for a value the harness could not have written, want none", got)
	}
	zone := time.FixedZone("PST", -8*60*60)
	if line := request.LineIn(zone); line != "named first for the development manager's next pass by the Lead Product Manager at 11:13 PST on November 27: lift the design hold by Saturday" {
		t.Fatalf("LineIn() = %q", line)
	}
}
