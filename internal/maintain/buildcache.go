package maintain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

// StepBuildCache is the step that holds the shared build cache under
// execution.build_cache_limit_gib (docs/designs/shared-build-cache-size.md).
const StepBuildCache = config.BuildCacheTaskName

const (
	// BuildCacheEvery is the longest the shared build cache goes without a look.
	BuildCacheEvery = time.Hour
	// buildCacheWarnEvery is the least time between two warnings that the cache
	// is still over its limit after a trim.
	buildCacheWarnEvery = 24 * time.Hour
)

// BuildCacheClaims is where the trims outside the maintenance pass, and the
// warning, keep their cadence, so neither starts over when the supervisor
// restarts. It is satisfied by *runstate.SweepStore.
type BuildCacheClaims interface {
	Claim(ctx context.Context, task string, every time.Duration, now time.Time) (runstate.SweepClaim, error)
	Settle(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
	Owe(ctx context.Context, task, problem string) (runstate.SweepClaim, error)
	Append(recorded runstate.Sweep) error
}

// BuildCache trims the shared build cache. The maintenance pass takes a trim as
// one of its steps; as a resident of the supervisor it takes one of its own,
// with a record of its own, whenever BuildCacheEvery has passed since the last:
// where the pass is off or runs less often than that, or one pass has run long.
type BuildCache struct {
	Repository string
	// Limit is the size in bytes the cache is held to.
	Limit        int64
	Product      domain.ProductID
	RepositoryID string
	Build        string
	Claims       BuildCacheClaims
	Reports      Reports
	// InPass says the maintenance pass runs at least every BuildCacheEvery, so
	// the first trim after a start is left to it.
	InPass bool
	Now    func() time.Time
	Log    func(format string, args ...any)
	// RecordFailures files or clears the shared finding after recording a trim
	// of its own.
	RecordFailures func(context.Context) error

	mu       sync.Mutex
	trimming bool
	// last is when the last trim started in this process, and nextDue when a
	// claim refused as too soon is due.
	last, nextDue time.Time
	turns         sync.WaitGroup
}

// Name is how the trim is named in what the supervisor says.
func (b *BuildCache) Name() string { return "build cache trim" }

// Look is the trim as a resident of the supervisor.
func (b *BuildCache) Look(ctx context.Context, now time.Time) {
	b.mu.Lock()
	if b.last.IsZero() && b.InPass {
		b.last = now
	}
	due := !b.trimming && now.Sub(b.last) >= BuildCacheEvery && !now.Before(b.nextDue)
	b.mu.Unlock()
	if !due {
		return
	}
	claimed, err := b.Claims.Claim(ctx, config.BuildCacheTaskName, BuildCacheEvery, now)
	if err != nil {
		next := now.Add(BuildCacheEvery)
		var notDue runstate.SweepNotDueError
		if errors.As(err, &notDue) {
			next = notDue.NextDue
		} else {
			b.log("the build cache trim could not be claimed, so it was not taken: %v", err)
		}
		b.mu.Lock()
		b.nextDue = next
		b.mu.Unlock()
		return
	}
	b.mu.Lock()
	b.nextDue = claimed.NextDue(BuildCacheEvery)
	b.mu.Unlock()
	b.turns.Add(1)
	go func() {
		defer b.turns.Done()
		b.turn(ctx, now)
	}()
}

// Wait blocks until every trim this resident started has been recorded. It is
// for a test.
func (b *BuildCache) Wait() { b.turns.Wait() }

// turn is one trim outside the maintenance pass, recorded as a pass of its own.
func (b *BuildCache) turn(ctx context.Context, started time.Time) {
	step := b.Trim(ctx)
	steps := []runstate.SweepStep{step}
	recorded := runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion,
		ProductID:     b.Product,
		Task:          config.BuildCacheTaskName,
		StartedAt:     started.UTC(),
		EndedAt:       b.now().UTC(),
		Steps:         steps,
		Result:        &sweep.Result{Status: sweep.StatusComplete, Summary: summarize(steps)},
		Problem:       bounded(failures(steps)),
	}
	b.log("build cache trim: %s, %s", step.Outcome, step.Detail)
	if err := b.Claims.Append(recorded); err != nil {
		b.log("the build cache trim could not be recorded, so `yoyo sweeps` will not show it: %v", err)
	}
	if _, err := b.Claims.Settle(ctx, config.BuildCacheTaskName, recorded.Problem); err != nil {
		b.log("the build cache trim's claim could not be settled: %v", err)
	}
	if b.RecordFailures != nil {
		if err := b.RecordFailures(ctx); err != nil {
			b.log("the build cache trim's failure finding could not be recorded: %v", err)
		}
	}
}

// Trim looks at the cache once, trims it where it is over its limit, and says
// what it came to.
func (b *BuildCache) Trim(ctx context.Context) runstate.SweepStep {
	step := runstate.SweepStep{Name: StepBuildCache}
	now := b.now()
	b.mu.Lock()
	if b.trimming {
		b.mu.Unlock()
		step.Outcome, step.Detail = runstate.StepSkipped, "a trim of the shared build cache is already under way"
		return step
	}
	b.trimming, b.last = true, now
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.trimming = false
		b.mu.Unlock()
	}()

	trim, err := execution.TrimBuildCache(ctx, b.Repository, b.Limit, now)
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the shared build cache could not be held to its limit: %v", err)
		if trim.Trimmed {
			step.Detail += fmt.Sprintf("; removed %d file(s) at %s, from %s to %s", trim.Removed, local(now), sayGiB(trim.Before), sayGiB(trim.After))
		}
		step.Detail = bounded(step.Detail)
	} else if trim.Folder == "" {
		step.Outcome, step.Detail = runstate.StepSkipped, "there is no shared build cache folder to hold to a limit"
	} else if !trim.Trimmed {
		step.Outcome, step.Detail = runstate.StepRan, fmt.Sprintf("the shared build cache %s is %s, within its limit of %s", trim.Folder, sayGiB(trim.Before), sayGiB(b.Limit))
	} else {
		step.Outcome, step.Detail = runstate.StepRan, fmt.Sprintf("trimmed the shared build cache %s at %s: %s before, %s after, %d file(s) removed, oldest first; its limit is %s", trim.Folder, local(now), sayGiB(trim.Before), sayGiB(trim.After), trim.Removed, sayGiB(b.Limit))
	}
	if trim.Trimmed && trim.After > b.Limit && ctx.Err() == nil {
		step.Detail = bounded(step.Detail + "; " + b.warn(ctx, trim, err, now))
	}
	return step
}

// warn files the one warning a day that the cache is still over its limit, and
// says what it did. The claim is what holds it to a day across a restart; a
// warning that could not be filed is owed again at once.
func (b *BuildCache) warn(ctx context.Context, trim execution.BuildCacheTrim, failed error, now time.Time) string {
	if b.Claims == nil || b.Reports == nil {
		return "it is still over the limit, and no report pile is wired, so the development manager could not be warned"
	}
	if _, err := b.Claims.Claim(ctx, config.BuildCacheWarningName, buildCacheWarnEvery, now); err != nil {
		if errors.Is(err, runstate.ErrSweepNotDue) {
			return "it is still over the limit, and the development manager was already warned within the last day"
		}
		return fmt.Sprintf("it is still over the limit, and whether the development manager was warned today could not be read, so no warning was filed: %v", err)
	}
	because := fmt.Sprintf("what is left was changed in the last %d hours or is not the harness's to remove", int(execution.BuildCacheKept/time.Hour))
	if failed != nil {
		because = fmt.Sprintf("some files could not be removed (%v)", failed)
	}
	message := fmt.Sprintf("The shared build cache %s is still %s after a trim, over its limit of %s (execution.build_cache_limit_gib), because %s. It goes on filling the disk until builds write less or the limit is raised.",
		trim.Folder, sayGiB(trim.After), sayGiB(b.Limit), because)
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityWarning,
		For:      domain.RoleDevelopmentManager,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        "build-cache:" + now.UTC().Format("20060102T150405"),
		Build:        b.Build,
		ProductID:    b.Product,
		RepositoryID: b.RepositoryID,
	}, now)
	if err == nil {
		err = b.Reports.Append(collected[0])
	}
	if err != nil {
		if _, oweErr := b.Claims.Owe(ctx, config.BuildCacheWarningName, err.Error()); oweErr != nil {
			err = errors.Join(err, oweErr)
		}
		return fmt.Sprintf("it is still over the limit, and the warning to the development manager could not be filed: %v", err)
	}
	if _, err := b.Claims.Settle(ctx, config.BuildCacheWarningName, ""); err != nil {
		b.log("the build cache warning's claim could not be settled: %v", err)
	}
	return "it is still over the limit, and the development manager was warned"
}

func (b *BuildCache) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *BuildCache) log(format string, args ...any) {
	if b.Log != nil {
		b.Log(format, args...)
	}
}

// sayGiB is a size in the unit the limit is set in, and in bytes below a tenth
// of it.
func sayGiB(size int64) string {
	if size < 1<<30/10 {
		return fmt.Sprintf("%d bytes", size)
	}
	return fmt.Sprintf("%.1f GiB", float64(size)/(1<<30))
}

// trimBuildCache is the trim as a step of the maintenance pass. It runs off the
// supervisor's goroutine.
func (p *Pass) trimBuildCache(ctx context.Context) runstate.SweepStep {
	if p.BuildCache == nil {
		return runstate.SweepStep{Name: StepBuildCache, Outcome: runstate.StepSkipped, Detail: "no trim of the shared build cache is wired, so its size is not held to a limit"}
	}
	return p.BuildCache.Trim(ctx)
}
