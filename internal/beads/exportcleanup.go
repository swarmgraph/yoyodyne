package beads

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// ExportTemporaryAge leaves a full day for an exporter that stopped making
// progress. Age alone never authorizes removal: open files are kept as well.
const ExportTemporaryAge = 24 * time.Hour

var exportTemporaryName = regexp.MustCompile(`^\.~(issues|interactions)\.jsonl\.[0-9]+$`)

type ExportTemporary struct {
	Path       string    `json:"path"`
	Bytes      int64     `json:"bytes"`
	ModifiedAt time.Time `json:"modified_at"`
}

type ExportCleanup struct {
	At      time.Time         `json:"at"`
	Removed []ExportTemporary `json:"removed,omitempty"`
	Kept    []string          `json:"kept,omitempty"`
}

// CleanExportTemporaries removes old, regular export temporaries in the primary
// checkout, from bd and the harness's confined snapshot writer. Both create
// these names exclusively and never reopen an
// abandoned one: a new export gets a new name. This lets lsof prove that an old
// candidate has no live holder without taking bd's store lock. Files changed
// during the inspection are kept, as are symlinks and every unrelated name.
//
// An unavailable, incomplete, or timed-out lsof inspection removes nothing.
// Holding the directory confines removal even if its pathname is replaced.
func CleanExportTemporaries(ctx context.Context, repository string, runner execution.ProcessRunner, now time.Time) (ExportCleanup, error) {
	cleaned := ExportCleanup{At: now.UTC()}
	declared, err := repowrite.NewRoot(repository)
	if err != nil {
		return cleaned, err
	}
	root, err := repowrite.OpenPinnedRoot(declared.Path())
	if err != nil {
		return cleaned, err
	}
	defer root.Close()
	beads, err := root.OpenDirectory(".beads")
	if errors.Is(err, os.ErrNotExist) {
		return cleaned, nil
	}
	if err != nil {
		return cleaned, err
	}
	defer beads.Close()
	entries, err := beads.ReadDirectory(".")
	if err != nil {
		return cleaned, err
	}
	type candidate struct {
		name string
		info os.FileInfo
	}
	var candidates []candidate
	for _, entry := range entries {
		if !exportTemporaryName.MatchString(entry.Name()) && !repowrite.IsTemporaryFile(entry.Name()) {
			continue
		}
		info, err := beads.Lstat(entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return cleaned, err
		}
		if info.Mode().IsRegular() && now.Sub(info.ModTime()) > ExportTemporaryAge {
			candidates = append(candidates, candidate{entry.Name(), info})
		}
	}
	if len(candidates) == 0 {
		return cleaned, nil
	}
	if err := beads.Unchanged(); err != nil {
		return cleaned, err
	}
	if runner == nil {
		return cleaned, errors.New("no process runner can check whether export temporaries are open, so they are kept")
	}
	var raw boundedExportOutput
	result, err := runner.Run(ctx, execution.Command{
		Name: "lsof", Args: []string{"-nP", "-F0fDi", "+d", beads.Path()},
		Timeout: DefaultTimeout, MaxOutputBytes: maxBDOutputBytes, RawStdout: &raw,
	}, nil)
	if err != nil {
		return cleaned, fmt.Errorf("check open export temporaries; all are kept: %w", err)
	}
	// lsof exits 1 when any directory entry has no holder, even when it lists
	// the others. That is a complete inspection; diagnostics, a cut answer, or
	// any other failure is not.
	completed := result.Status == execution.ProcessSucceeded || (result.Status == execution.ProcessFailed && result.ExitCode == 1)
	noOpenFiles := result.Status == execution.ProcessFailed && result.ExitCode == 1 && raw.Len() == 0
	if !completed || result.Stderr != "" || result.OutputTruncation != "" {
		return cleaned, fmt.Errorf("lsof could not check every export temporary, so all are kept: status %s, exit %d: %s %s",
			result.Status, result.ExitCode, strings.TrimSpace(result.Stderr), result.OutputTruncation)
	}
	open := make(map[exportFileIdentity]bool)
	if !noOpenFiles {
		open, err = openExportFiles(raw.String())
		if err != nil {
			return cleaned, fmt.Errorf("read lsof's answer; all export temporaries are kept: %w", err)
		}
	}
	if err := beads.Unchanged(); err != nil {
		return cleaned, err
	}
	for _, candidate := range candidates {
		path := filepath.Join(beads.Path(), candidate.name)
		current, err := beads.Lstat(candidate.name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return cleaned, err
		}
		identity, known := exportIdentity(current)
		if !known {
			return cleaned, errors.New("this platform cannot identify open export files, so they are kept")
		}
		if open[identity] || !current.Mode().IsRegular() || !os.SameFile(candidate.info, current) ||
			!current.ModTime().Equal(candidate.info.ModTime()) || current.Size() != candidate.info.Size() {
			cleaned.Kept = append(cleaned.Kept, path)
			continue
		}
		if err := beads.Remove(candidate.name); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return cleaned, fmt.Errorf("remove abandoned export %s: %w", path, err)
		}
		cleaned.Removed = append(cleaned.Removed, ExportTemporary{Path: path, Bytes: current.Size(), ModifiedAt: current.ModTime().UTC()})
	}
	if len(cleaned.Removed) > 0 {
		return cleaned, beads.Sync()
	}
	return cleaned, nil
}

type exportFileIdentity struct{ device, inode uint64 }

// Device and inode identify an open file even when its holder opened it through
// a hard link or a different spelling of the checkout's path.
func openExportFiles(output string) (map[exportFileIdentity]bool, error) {
	open := make(map[exportFileIdentity]bool)
	var identity exportFileIdentity
	var inFile, device, inode bool
	finish := func() error {
		if !inFile {
			return nil
		}
		if !device || !inode {
			return errors.New("lsof omitted a file's device or inode")
		}
		open[identity] = true
		inFile, device, inode = false, false, false
		return nil
	}
	for _, field := range strings.Split(output, "\x00") {
		field = strings.TrimLeft(field, "\n")
		if field == "" {
			continue
		}
		switch field[0] {
		case 'p', 'f':
			if err := finish(); err != nil {
				return nil, err
			}
			inFile = field[0] == 'f'
		case 'D':
			value, err := strconv.ParseUint(strings.TrimPrefix(field[1:], "0x"), 16, 64)
			if err != nil || !inFile {
				return nil, errors.New("lsof answered an invalid file device")
			}
			identity.device, device = value, true
		case 'i':
			value, err := strconv.ParseUint(field[1:], 10, 64)
			if err != nil || !inFile {
				return nil, errors.New("lsof answered an invalid file inode")
			}
			identity.inode, inode = value, true
		default:
			return nil, fmt.Errorf("lsof answered an unexpected field %q", field)
		}
	}
	if err := finish(); err != nil {
		return nil, err
	}
	if len(open) == 0 {
		return nil, errors.New("lsof reported success without identifying any open file")
	}
	return open, nil
}
