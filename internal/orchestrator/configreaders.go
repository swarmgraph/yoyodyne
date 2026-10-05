package orchestrator

// Reading the configuration a landing left, for both an immediate landing and
// a forge merge that reconciliation confirms later.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxLandedConfigBytes bounds the configuration a landing reads at its commit,
// the bound every state record here is held to.
const maxLandedConfigBytes = 1 << 20

// ConfigComparisonFiles reads the exact revisions a landing compares. The
// pipeline and reconciliation both use the repository manager's implementation.
type ConfigComparisonFiles interface {
	FileAtCommit(ctx context.Context, commit, path string, maxBytes int64) (gitworktree.FileAt, error)
}

type configLanding struct {
	repository string
	files      ConfigComparisonFiles
	readers    ConfigReaders
}

type configComparison struct {
	active        []runstate.ConfigMismatch
	templates     []runstate.ConfigTemplateMismatch
	activeError   error
	templateError error
}

func (c configLanding) compare(ctx context.Context, integration gitworktree.Integration) configComparison {
	if c.readers == nil {
		return configComparison{}
	}
	active, activeErr := c.readers.MismatchesIn(func(path string) ([]byte, error) {
		return c.configAtCommit(ctx, integration.TargetCommit, path)
	})
	templates, templateErr := c.templateConfigMismatches(ctx, integration)
	return configComparison{active: active, templates: templates, activeError: activeErr, templateError: templateErr}
}

func (c configComparison) notes() string {
	var notes, lines []string
	for _, mismatch := range c.active {
		lines = append(lines, mismatch.Says()+"; "+runstate.ConfigMismatchRemedy(mismatch.Service))
	}
	if len(lines) > 0 {
		notes = append(notes, "Running parts that cannot read the configuration this landing left: "+strings.Join(lines, "; "))
	}
	if c.activeError != nil {
		notes = append(notes, "Configuration reader records and comparison problems: "+c.activeError.Error())
	}
	lines = nil
	for _, mismatch := range c.templates {
		lines = append(lines, mismatch.Says()+"; "+runstate.ConfigMismatchRemedy(mismatch.Service))
	}
	if c.templateError != nil {
		lines = append(lines, "whether every running part can adopt the new keys in shipped templates could not be read whole: "+c.templateError.Error())
	}
	if len(lines) > 0 {
		notes = append(notes, "Running parts that cannot read new keys in shipped templates: "+strings.Join(lines, "; "))
	}
	return strings.Join(notes, "\n")
}

// configFindingDelivery separates a failed tracker write from a failed durable
// save. Only the former can leave a completed run with delivery still owed.
type configFindingDelivery struct{ cause error }

func (e configFindingDelivery) Error() string { return e.cause.Error() }
func (e configFindingDelivery) Unwrap() error { return e.cause }

type configFindingRecorder struct {
	configLanding
	store   interface{ Save(runstate.State) error }
	tracker WorkTracker
}

// name saves the comparison before trying to deliver it. Retrying uses that
// saved account, including read failures, even after services or files change.
func (r configFindingRecorder) name(ctx context.Context, state *runstate.State, integration gitworktree.Integration) (configComparison, error) {
	if state.ConfigComparison == nil && r.readers != nil {
		readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		comparison := r.compare(readCtx, integration)
		cancel()
		record := &runstate.ConfigComparison{
			TargetCommit: integration.TargetCommit, PreviousTargetCommit: integration.PreviousTargetCommit,
			Mismatches: comparison.active, TemplateMismatches: comparison.templates,
			Pending: comparison.notes() != "",
		}
		if comparison.activeError != nil {
			record.ActiveProblem = runstate.RecordFailure(comparison.activeError.Error())
		}
		if comparison.templateError != nil {
			record.TemplateProblem = runstate.RecordFailure(comparison.templateError.Error())
		}
		state.ConfigComparison = record
		if err := r.store.Save(*state); err != nil {
			return comparison, fmt.Errorf("save configuration comparison for run %s: %w", state.RunID, err)
		}
	}
	record := state.ConfigComparison
	if record == nil {
		return configComparison{}, nil
	}
	comparison := configComparison{active: record.Mismatches, templates: record.TemplateMismatches}
	if record.ActiveProblem != "" {
		comparison.activeError = errors.New(record.ActiveProblem)
	}
	if record.TemplateProblem != "" {
		comparison.templateError = errors.New(record.TemplateProblem)
	}
	if !record.Pending {
		return comparison, nil
	}
	noteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, deliveryErr := r.tracker.RecordOutcome(noteCtx, state.WorkItemID, comparison.notes())
	cancel()
	if deliveryErr != nil {
		record.DeliveryFailure = runstate.RecordFailure(deliveryErr.Error())
		if err := r.store.Save(*state); err != nil {
			return comparison, fmt.Errorf("save failed configuration finding delivery for run %s: %w", state.RunID, err)
		}
		return comparison, configFindingDelivery{fmt.Errorf("record configuration comparisons for run %s: %w", state.RunID, deliveryErr)}
	}
	record.Pending = false
	record.DeliveryFailure = ""
	if err := r.store.Save(*state); err != nil {
		// The delivered note may repeat on a retry, but no later save may
		// silently discard the durable obligation that still stands on disk.
		record.Pending = true
		return comparison, fmt.Errorf("save delivered configuration finding for run %s: %w", state.RunID, err)
	}
	return comparison, nil
}

// nameConfigReaders retains the promotion's revisions, comparing the confirmed
// merge instead when the forge names it. A failed delivery stays outstanding.
func (r Reconciler) nameConfigReaders(ctx context.Context, state *runstate.State, confirmed string) (configComparison, error) {
	if state.Integration == nil {
		return configComparison{}, nil
	}
	integration := integrationOf(*state)
	if confirmed != "" {
		integration.TargetCommit = confirmed
	}
	return (configFindingRecorder{
		configLanding: configLanding{repository: r.Repository, files: r.ConfigFiles, readers: r.ConfigReaders},
		store:         r.Store, tracker: r.Tracker,
	}).name(ctx, state, integration)
}

// templateConfigMismatches compares keys added to shipped templates at this
// landing, independently of the active files. Both versions come from Git:
// the checkout and the checker's embedded template can each be older.
func (c configLanding) templateConfigMismatches(ctx context.Context, integration gitworktree.Integration) ([]runstate.ConfigTemplateMismatch, error) {
	var mismatches []runstate.ConfigTemplateMismatch
	var problems []error
	for _, path := range config.BuiltinTemplatePaths() {
		after, err := c.templateAtCommit(ctx, integration.TargetCommit, path)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if after == nil {
			continue
		}
		before, err := c.templateAtCommit(ctx, integration.PreviousTargetCommit, path)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		added, err := config.AddedTemplateKeys(before, after)
		if err != nil {
			problems = append(problems, fmt.Errorf("compare shipped template %s: %w", path, err))
			continue
		}
		if len(added) == 0 {
			continue
		}
		found, err := c.readers.TemplateMismatches(path, added)
		mismatches = append(mismatches, found...)
		problems = append(problems, err)
	}
	return mismatches, errors.Join(problems...)
}

func (c configLanding) templateAtCommit(ctx context.Context, commit, path string) ([]byte, error) {
	if strings.TrimSpace(commit) == "" || c.files == nil {
		return nil, fmt.Errorf("read shipped template %s: the landing names no commit or repository reader", path)
	}
	file, err := c.files.FileAtCommit(ctx, commit, path, maxLandedConfigBytes)
	if errors.Is(err, gitworktree.ErrNotAtCommit) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read shipped template %s at %s: %w", path, commit, err)
	}
	if file.Content == nil && file.Size > 0 {
		return nil, fmt.Errorf("%s at %s is %d bytes, past the %d byte bound", path, commit, file.Size, maxLandedConfigBytes)
	}
	return file.Content, nil
}

// configAtCommit reads a configuration file a running part reads as commit
// holds it, where the file is inside the repository and the commit carries
// it, and as it stands on disk otherwise.
func (c configLanding) configAtCommit(ctx context.Context, commit, configPath string) ([]byte, error) {
	relative, inside := repositoryRelative(c.repository, configPath)
	if !inside || strings.TrimSpace(commit) == "" || c.files == nil {
		return os.ReadFile(configPath)
	}
	file, err := c.files.FileAtCommit(ctx, commit, relative, maxLandedConfigBytes)
	if errors.Is(err, gitworktree.ErrNotAtCommit) {
		return os.ReadFile(configPath)
	}
	if err != nil {
		return nil, err
	}
	if file.Content == nil && file.Size > 0 {
		return nil, fmt.Errorf("%s at %s is %d bytes, past the %d byte bound", relative, commit, file.Size, maxLandedConfigBytes)
	}
	return file.Content, nil
}

// repositoryRelative is path within repository in slash form, reporting
// whether it is inside it at all. Both are resolved through their symlinks
// where they can be, because a recorded path and the pipeline's root can name
// one directory two ways — /var and /private/var on macOS.
func repositoryRelative(repository, path string) (string, bool) {
	if strings.TrimSpace(repository) == "" {
		return "", false
	}
	for _, pair := range [][2]string{{repository, path}, {resolved(repository), resolved(path)}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
			return filepath.ToSlash(relative), true
		}
	}
	return "", false
}

func resolved(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}
