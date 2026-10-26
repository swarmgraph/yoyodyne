package usage

import (
	"encoding/json"
	"testing"
)

func TestAnAbsentCounterIsLeftOutAndAMeasuredZeroIsKept(t *testing.T) {
	counters := Counters{FreshInput: Measured(0), Output: Measured(7)}
	encoded, err := json.Marshal(counters)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"fresh_input":0,"output":7}` {
		t.Fatalf("encoded = %s", encoded)
	}
	var decoded Counters
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded != counters {
		t.Fatalf("decoded = %+v, want %+v", decoded, counters)
	}
	if decoded.CachedInput.Measured || !decoded.FreshInput.Measured {
		t.Fatalf("absent and zero were confused: %+v", decoded)
	}
	if (Counters{}).Any() || !decoded.Any() {
		t.Fatal("Any does not tell a reading from no reading")
	}
}

func TestInputIsAbsentUnlessEveryPartOfItWasMeasured(t *testing.T) {
	if (Counters{FreshInput: Measured(5), CachedInput: Measured(5)}).Input().Measured {
		t.Fatal("an input with its cache writes unreported was taken as whole")
	}
	whole := Counters{FreshInput: Measured(5), CachedInput: Measured(6), CacheWrite: Measured(0)}.Input()
	if !whole.Measured || whole.Tokens != 11 {
		t.Fatalf("input = %+v", whole)
	}
}

func TestPlusKeepsAnUnmeasuredCounterUnmeasured(t *testing.T) {
	sum := Counters{Output: Measured(3)}.Plus(Counters{Output: Measured(4), Reasoning: Measured(1)})
	if sum.Output != Measured(7) || sum.Reasoning != Measured(1) || sum.FreshInput.Measured {
		t.Fatalf("sum = %+v", sum)
	}
}

func TestBillingModesAreReadFromConfigurationAndAnUnstatedOneIsNotRecorded(t *testing.T) {
	if mode, err := ParseBillingMode(""); err != nil || mode != BillingNotRecorded {
		t.Fatalf("empty = %q, %v", mode, err)
	}
	for _, mode := range BillingModes() {
		if parsed, err := ParseBillingMode(string(mode)); err != nil || parsed != mode {
			t.Fatalf("%s = %q, %v", mode, parsed, err)
		}
	}
	if _, err := ParseBillingMode("monthly"); err == nil {
		t.Fatal("an unknown billing mode was accepted")
	}
}

func running(fresh, cached, output int64) *Reading {
	return &Reading{Session: "s", Running: true, Counters: Counters{FreshInput: Measured(fresh), CachedInput: Measured(cached), Output: Measured(output)}}
}

func TestAFreshSessionsRunningTotalIsTakenWhole(t *testing.T) {
	settled := Settle(nil, running(100, 50, 10), false, 0)
	if settled.Counters.FreshInput != Measured(100) || settled.Reset || settled.Running == nil {
		t.Fatalf("settled = %+v", settled)
	}
}

func TestAResumedSessionsRunningTotalBecomesTheDifference(t *testing.T) {
	first := Settle(nil, running(100, 50, 10), false, 0)
	second := Settle(first.Running, running(160, 250, 25), false, 0)
	want := Counters{FreshInput: Measured(60), CachedInput: Measured(200), Output: Measured(15)}
	if second.Counters != want || second.Reset {
		t.Fatalf("second = %+v, want %+v", second.Counters, want)
	}
	if *second.Running != running(160, 250, 25).Counters {
		t.Fatal("the running total was not kept for the next call")
	}
	// A counter measured now and never before counts from zero.
	third := Settle(&Counters{FreshInput: Measured(160)}, running(170, 260, 30), false, 0)
	if third.Counters.CachedInput != Measured(260) || third.Counters.FreshInput != Measured(10) {
		t.Fatalf("third = %+v", third.Counters)
	}
}

func TestARunningTotalThatFellIsAResetTakenWhole(t *testing.T) {
	previous := running(500, 400, 90).Counters
	settled := Settle(&previous, running(20, 0, 3), false, 0)
	if !settled.Reset || settled.Counters.FreshInput != Measured(20) || settled.Counters.Output != Measured(3) {
		t.Fatalf("settled = %+v", settled)
	}
}

func TestAPerCallReadingIsTakenAsItIsWhateverCameBefore(t *testing.T) {
	previous := running(500, 400, 90).Counters
	settled := Settle(&previous, &Reading{Counters: Counters{Output: Measured(4)}}, false, 0)
	if settled.Counters.Output != Measured(4) || settled.Running != nil || settled.Reset {
		t.Fatalf("settled = %+v", settled)
	}
}

func TestACallThatReportedNothingHasEveryCounterAbsent(t *testing.T) {
	settled := Settle(nil, nil, true, 0)
	if settled.Counters.Any() || !settled.Partial {
		t.Fatalf("settled = %+v", settled)
	}
}

func TestARepeatedFinalEventIsCountedOnce(t *testing.T) {
	var collector Collector
	final := Reading{EventID: "turn-1", Counters: Counters{Output: Measured(5)}}
	if !collector.Observe(final) || collector.Observe(final) {
		t.Fatal("a repeated event was not set aside")
	}
	if got := collector.Reading().Counters.Output; got != Measured(5) || collector.Repeated() != 1 {
		t.Fatalf("output = %+v, repeated = %d", got, collector.Repeated())
	}
	// Distinct per-call events in one call are both the call's.
	collector.Observe(Reading{EventID: "turn-2", Counters: Counters{Output: Measured(2)}})
	if got := collector.Reading().Counters.Output; got != Measured(7) {
		t.Fatalf("output = %+v", got)
	}
}

func TestTheLaterOfTwoRunningTotalsInOneCallIsKept(t *testing.T) {
	var collector Collector
	collector.Observe(*running(10, 0, 1))
	collector.Observe(*running(30, 5, 4))
	if got := collector.Reading().Counters; got != running(30, 5, 4).Counters {
		t.Fatalf("reading = %+v", got)
	}
	var empty *Collector
	if empty.Reading() != nil || (&Collector{}).Reading() != nil {
		t.Fatal("a call with no readings reported one")
	}
}
