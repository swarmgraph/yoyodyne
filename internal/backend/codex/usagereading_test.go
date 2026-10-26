package codex

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/usage"
)

func parseRecorded(t *testing.T, lines []string) *streamParser {
	t.Helper()
	parser := newStreamParser(testRunID, domain.RoleDeveloper, 0, fixedClock{}, execution.NewRedactor(), func(execution.Event) error { return nil }, nil, Dialect{})
	for _, line := range lines {
		if err := parser.ParseLine(line); err != nil {
			t.Fatalf("ParseLine(%q) error = %v", line, err)
		}
	}
	return parser
}

func TestTheRecordedTurnHandsOverAReadingWithTheOverlapsTakenOut(t *testing.T) {
	lines := readRecordedLines(t, filepath.Join("testdata", "streams", "codex-cli-0.159.2", "reply-and-turn-completed.jsonl"))
	result := parseRecorded(t, lines).Result()
	reading := result.UsageReading
	if reading == nil {
		t.Fatal("a turn that reported its usage handed over no reading")
	}
	want := usage.Counters{
		FreshInput:  usage.Measured(16304 - 13184),
		CachedInput: usage.Measured(13184),
		CacheWrite:  usage.Measured(0),
		Output:      usage.Measured(5),
		Reasoning:   usage.Measured(0),
	}
	if reading.Counters != want {
		t.Fatalf("counters = %+v, want %+v", reading.Counters, want)
	}
	if !reading.Running || reading.Session != "01a0f87c-cdff-7233-adc4-8d2078a8ec52" {
		t.Fatalf("reading = %+v", reading)
	}
	if len(reading.Adjustments) != 2 || !strings.Contains(reading.Adjustments[0], "cached_input_tokens") || !strings.Contains(reading.Adjustments[1], "reasoning_output_tokens") {
		t.Fatalf("adjustments = %v", reading.Adjustments)
	}
	if result.UsagePartial || result.UsageRepeated != 0 {
		t.Fatalf("a completed turn was marked partial or repeated: %+v", result)
	}
}

func TestAStreamThatNeverReachedTheProviderHandsOverNoReading(t *testing.T) {
	lines := readRecordedLines(t, filepath.Join("testdata", "streams", "codex-cli-0.159.2", "no-provider-reached.jsonl"))
	result := parseRecorded(t, lines).Result()
	if result.UsageReading != nil || result.UsagePartial {
		t.Fatalf("result = %+v", result)
	}
}

func TestATurnCutOffAfterAReadingIsPartial(t *testing.T) {
	lines := readRecordedLines(t, filepath.Join("testdata", "streams", "codex-cli-0.159.2", "reply-and-turn-completed.jsonl"))
	var cut []string
	for _, line := range lines {
		if strings.Contains(line, `"turn.completed"`) {
			break
		}
		cut = append(cut, line)
	}
	cut = append(cut, `{"msg":{"type":"token_count","input_tokens":100,"cached_input_tokens":40,"output_tokens":7}}`)
	parser := parseRecorded(t, cut)
	result := parser.Result()
	if parser.SawTerminal() || !result.UsagePartial || result.UsageReading == nil {
		t.Fatalf("result = %+v", result)
	}
	want := usage.Counters{FreshInput: usage.Measured(60), CachedInput: usage.Measured(40), Output: usage.Measured(7)}
	if result.UsageReading.Counters != want {
		t.Fatalf("counters = %+v, want %+v", result.UsageReading.Counters, want)
	}
}
