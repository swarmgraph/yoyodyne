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

func TestALaunchdJobOfTheProductsOwnIsAMarkAndATerminalIsNot(t *testing.T) {
	t.Parallel()

	if by, started := StartedBy([]string{LaunchdJobVariable + "=com.yoyodyne.maintenance"}); !started || by != "the launchd job com.yoyodyne.maintenance" {
		t.Errorf("StartedBy() = %q, %v; want the operator's maintenance job named", by, started)
	}
	if by, started := StartedBy([]string{StartedByVariable + "=the supervisor's maintenance pass", LaunchdJobVariable + "=com.yoyodyne.supervisor.calc"}); !started || by != "the supervisor's maintenance pass" {
		t.Errorf("StartedBy() = %q, %v; want the harness's own mark ahead of the job it runs under", by, started)
	}
	for _, terminal := range []string{"0", "application.com.apple.Terminal.1234", ""} {
		if by, started := StartedBy([]string{LaunchdJobVariable + "=" + terminal}); started {
			t.Errorf("a terminal named %q reads as started by %q", terminal, by)
		}
	}
}
