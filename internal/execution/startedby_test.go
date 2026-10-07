package execution

import "testing"

func TestTheHarnessMarksWhatItStartsAndAnEmptyMarkIsNone(t *testing.T) {
	t.Parallel()

	marked := WithStartedBy([]string{"PATH=/bin", StartedByVariable + "=an older pass"}, "the supervisor's maintenance pass")
	if by, started := StartedBy(marked); !started || by != "the supervisor's maintenance pass" {
		t.Errorf("StartedBy() = %q, %v; want the newest mark alone", by, started)
	}
	count := 0
	for _, entry := range marked {
		if len(entry) >= len(StartedByVariable) && entry[:len(StartedByVariable)] == StartedByVariable {
			count++
		}
	}
	if count != 1 {
		t.Errorf("the environment carries %d marks, want one: %v", count, marked)
	}
	if _, started := StartedBy([]string{"PATH=/bin"}); started {
		t.Error("an unmarked environment reads as started by the harness")
	}
	if _, started := StartedBy([]string{StartedByVariable + "= "}); started {
		t.Error("an empty mark reads as a mark")
	}
}
