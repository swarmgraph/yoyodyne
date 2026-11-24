package maintain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type reportPile struct {
	filed []report.Report
	err   error
}

func (p *reportPile) Append(filed report.Report) error {
	if p.err != nil {
		return p.err
	}
	p.filed = append(p.filed, filed)
	return nil
}

// cacheFixture is a trim of a repository whose cache folder holds one file of
// size bytes for each age given.
func cacheFixture(t *testing.T, f *fixture, limit int64, size int, ages ...time.Duration) (*BuildCache, *reportPile) {
	t.Helper()
	repository := t.TempDir()
	folder := filepath.Join(repository, ".git", "yoyodyne", "go-build", "00")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, age := range ages {
		path := filepath.Join(folder, string(rune('a'+i)))
		if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, f.now.Add(-age), f.now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	pile := &reportPile{}
	return &BuildCache{
		Repository: repository, Limit: limit, Product: "calc", RepositoryID: "calc",
		Claims: f.sweeps, Reports: pile, Now: func() time.Time { return f.now },
	}, pile
}

// The pass that trims says the size either side, how many files went, and when,
// and a pass that finds the folder within its limit says that.
func TestAPassRecordsItsTrimOfTheBuildCache(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	trim, pile := cacheFixture(t, f, 1000, 400, 30*time.Hour, 20*time.Hour, 10*time.Hour)
	f.pass.BuildCache = trim

	step := stepNamed(t, f.pass1(t), StepBuildCache)
	for _, want := range []string{"1200 bytes before", "800 bytes after", "1 file(s) removed", local(f.now), "limit is 1000 bytes"} {
		if step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, want) {
			t.Errorf("step = %+v, want it run saying %q", step, want)
		}
	}
	f.now = f.now.Add(f.pass.Every)
	if step := stepNamed(t, f.pass1(t), StepBuildCache); step.Outcome != runstate.StepRan || !strings.Contains(step.Detail, "within its limit") {
		t.Errorf("second step = %+v, want it within its limit", step)
	}
	if len(pile.filed) != 0 {
		t.Errorf("a trim that reached its limit warned: %+v", pile.filed)
	}
}

// A folder still over its limit after a trim warns the development manager
// once, again only a day later, and a restart does not warn early.
func TestABuildCacheStillOverItsLimitWarnsAtMostOnceADay(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	trim, pile := cacheFixture(t, f, 1000, 400, -100*time.Hour, -100*time.Hour, -100*time.Hour)

	step := trim.Trim(context.Background())
	if len(pile.filed) != 1 || !strings.Contains(step.Detail, "development manager was warned") {
		t.Fatalf("step = %+v, filed = %+v, want one warning", step, pile.filed)
	}
	filed := pile.filed[0]
	if filed.Severity != report.SeverityWarning || filed.For != domain.RoleDevelopmentManager ||
		!strings.Contains(filed.Message, "1200 bytes") || !strings.Contains(filed.Message, "limit of 1000 bytes") {
		t.Fatalf("filed = %+v", filed)
	}
	restarted := &BuildCache{
		Repository: trim.Repository, Limit: trim.Limit, Product: "calc", RepositoryID: "calc",
		Claims: f.sweeps, Reports: pile, Now: trim.Now,
	}
	f.now = f.now.Add(23 * time.Hour)
	if step := restarted.Trim(context.Background()); len(pile.filed) != 1 || !strings.Contains(step.Detail, "already warned") {
		t.Fatalf("step = %+v, filed %d, want no second warning inside a day", step, len(pile.filed))
	}
	f.now = f.now.Add(time.Hour)
	if restarted.Trim(context.Background()); len(pile.filed) != 2 {
		t.Fatalf("filed %d, want a second warning a day later", len(pile.filed))
	}
}

// A warning that could not be filed does not use up the day's one.
func TestABuildCacheWarningThatCouldNotBeFiledIsTriedAgain(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	trim, pile := cacheFixture(t, f, 1000, 1200, 0)
	pile.err = errors.New("disk full")
	if step := trim.Trim(context.Background()); !strings.Contains(step.Detail, "could not be filed") {
		t.Fatalf("step = %+v, want it to say the warning was not filed", step)
	}
	pile.err = nil
	f.now = f.now.Add(10 * time.Minute)
	if step := trim.Trim(context.Background()); len(pile.filed) != 1 {
		t.Fatalf("step = %+v, filed %d, want the warning filed", step, len(pile.filed))
	}
}

func buildCacheTurns(t *testing.T, f *fixture) []runstate.Sweep {
	t.Helper()
	recorded, _, err := f.sweeps.List()
	if err != nil {
		t.Fatal(err)
	}
	var turns []runstate.Sweep
	for _, pass := range recorded {
		if pass.Task == config.BuildCacheTaskName {
			turns = append(turns, pass)
		}
	}
	return turns
}

// With no maintenance pass to carry it, the supervisor takes a trim of its own
// every hour, recorded, and a restart inside the hour takes no second one.
func TestTheBuildCacheIsTrimmedHourlyWithoutTheMaintenancePass(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	trim, _ := cacheFixture(t, f, 1000, 400, 30*time.Hour, 20*time.Hour, 10*time.Hour)
	look := func(b *BuildCache) {
		b.Look(context.Background(), f.now)
		b.Wait()
	}

	look(trim)
	turns := buildCacheTurns(t, f)
	if len(turns) != 1 || !turns[0].HarnessPass() || !strings.Contains(stepNamed(t, turns[0], StepBuildCache).Detail, "1 file(s) removed") {
		t.Fatalf("turns = %+v, want one recorded trim", turns)
	}
	f.now = f.now.Add(59 * time.Minute)
	look(trim)
	restarted := &BuildCache{Repository: trim.Repository, Limit: trim.Limit, Product: "calc", Claims: f.sweeps, Now: trim.Now}
	look(restarted)
	if turns := buildCacheTurns(t, f); len(turns) != 1 {
		t.Fatalf("%d trims inside an hour, want 1", len(turns))
	}
	f.now = f.now.Add(time.Minute)
	look(trim)
	if turns := buildCacheTurns(t, f); len(turns) != 2 {
		t.Fatalf("%d trims after an hour, want 2", len(turns))
	}
}

// While the maintenance pass trims often enough the supervisor takes none of
// its own, and once an hour has passed without one it does.
func TestTheSupervisorTrimsOnlyWhereThePassHasNotForAnHour(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	trim, _ := cacheFixture(t, f, 1000, 400, 30*time.Hour)
	trim.InPass = true
	f.pass.BuildCache = trim
	for i := 0; i < 12; i++ {
		trim.Look(context.Background(), f.now)
		trim.Wait()
		f.pass1(t)
		f.now = f.now.Add(f.pass.Every)
	}
	if turns := buildCacheTurns(t, f); len(turns) != 0 {
		t.Fatalf("%d trims beside passes that took one, want none", len(turns))
	}
	f.now = f.now.Add(time.Hour)
	trim.Look(context.Background(), f.now)
	trim.Wait()
	if turns := buildCacheTurns(t, f); len(turns) != 1 {
		t.Fatalf("%d trims after an hour without one, want 1", len(turns))
	}
}
