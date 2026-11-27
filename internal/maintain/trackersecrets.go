package maintain

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The secrets step: the tracker's export in the primary checkout
// (beads.ExportPath), which is committed to Git, read for whole secrets on
// every pass.
//
// Before secrets were removed by their shape (execution.WithSecretShapes), the
// last lines a failed provider session wrote went onto the item's notes with any
// key or token in them, and the export publishes every note with the
// repository. The step only reads the file. Nothing is edited, because notes are
// append-only and a published value cannot be taken back anyway: what helps is
// the credential being replaced, which only a person who holds it can do. So
// each whole secret found is filed once as a critical report naming the item
// and the kind of secret, never the value, for the Lead Product Manager to put
// to the operator.
//
// Only whole secrets count (execution.FindCompleteSecrets): notes discuss this
// very check and quote the prefixes it looks for, and a step that named every
// item mentioning "Bearer" would be one nobody reads. It is not a check a
// change must pass, for the same reason: the export changes with every note.
//
// A match is known again by a fingerprint of the item, the kind, and the value,
// carried in the report's run id, so the same match is filed once however many
// passes find it and across restarts of the supervisor. The fingerprint is the
// first twelve hexadecimal digits of a SHA-256 hash, which says nothing about a
// value of forty random characters or more.

// StepTrackerSecrets is the secrets step's name, as the sweep record carries it.
const StepTrackerSecrets = "secrets"

// secretRunPrefix begins the run id of every report the secrets step files.
const secretRunPrefix = "tracker-secret:"

// maxExportLine bounds one line of the export. An item with every note it was
// ever given is far below it.
const maxExportLine = 64 << 20

// ReportList is a report pile that can be read back, which the secrets step
// uses to know what it already filed. It is satisfied by *runstate.ReportStore.
type ReportList interface {
	List() ([]report.Report, error)
}

// exportSecret is one whole secret in one item of the export.
type exportSecret struct {
	item        string
	kind        execution.SecretKind
	fingerprint string
}

// checkTrackerSecrets reads the export for whole secrets and files a report for
// each it has not filed before. It runs off the supervisor's goroutine, last,
// and touches nothing of the supervisor's but secretsFiled, which only this
// goroutine reads or writes while a pass runs.
func (p *Pass) checkTrackerSecrets() runstate.SweepStep {
	step := runstate.SweepStep{Name: StepTrackerSecrets}
	if p.Repository == "" {
		step.Outcome, step.Detail = runstate.StepSkipped, "no repository is wired, so the tracker's export is not read for secrets"
		return step
	}
	path := filepath.Join(p.Repository, beads.ExportPath)
	found, unreadable, err := readExportSecrets(path)
	if errors.Is(err, os.ErrNotExist) {
		step.Outcome, step.Detail = runstate.StepSkipped, fmt.Sprintf("there is no tracker export at %s to read for secrets", path)
		return step
	}
	if err != nil {
		step.Outcome, step.Detail = runstate.StepFailed, fmt.Sprintf("the tracker's export could not be read for secrets: %v", err)
		return step
	}
	step.Outcome = runstate.StepRan
	tail := ""
	if unreadable > 0 {
		tail = fmt.Sprintf("; %d lines of it were not work items and were read as plain text", unreadable)
	}
	if len(found) == 0 {
		step.Detail = "the tracker's export holds no whole key or token" + tail
		return step
	}
	var named []string
	for _, secret := range found {
		named = append(named, fmt.Sprintf("%s (%s)", secret.item, secret.kind))
	}
	step.Detail = fmt.Sprintf("THE TRACKER'S EXPORT HOLDS %d WHOLE KEYS OR TOKENS: %s", len(found), strings.Join(named, ", ")) + tail
	if p.Reports == nil {
		step.Detail += "; no report pile is wired, so nobody could be told"
		return step
	}
	if err := p.loadSecretsFiled(); err != nil {
		step.Detail += fmt.Sprintf("; the reports already filed could not be read, so none was filed and the next pass tries again: %v", err)
		return step
	}
	filed, failed := 0, ""
	for _, secret := range found {
		if p.secretsFiled[secret.fingerprint] {
			continue
		}
		if err := p.fileSecret(secret); err != nil {
			failed = err.Error()
			continue
		}
		p.secretsFiled[secret.fingerprint] = true
		filed++
	}
	switch {
	case failed != "":
		step.Detail += fmt.Sprintf("; %d reported now, and the rest could not be filed and the next pass tries again: %s", filed, failed)
	case filed == 0:
		step.Detail += "; each was already reported"
	default:
		step.Detail += fmt.Sprintf("; %d reported now, the rest already reported", filed)
	}
	return step
}

// loadSecretsFiled reads which matches earlier passes reported, once a
// process, from the pile itself, so a restart does not file them again. A pile
// that cannot be read back is known only from this process's own filings.
func (p *Pass) loadSecretsFiled() error {
	if p.secretsFiled != nil {
		return nil
	}
	filed := map[string]bool{}
	if lister, ok := p.Reports.(ReportList); ok {
		reports, err := lister.List()
		if err != nil {
			return err
		}
		for _, earlier := range reports {
			if fingerprint, ok := strings.CutPrefix(earlier.RunID, secretRunPrefix); ok {
				filed[fingerprint] = true
			}
		}
	}
	p.secretsFiled = filed
	return nil
}

// fileSecret files one critical report naming the item and the kind of secret.
func (p *Pass) fileSecret(secret exportSecret) error {
	message := fmt.Sprintf("credential: Maintenance found a whole %s in work item %s in the tracker's export (%s), which is committed to Git, so the value is published with the repository. "+
		"Whoever holds that credential has to replace it at its provider and retire the old one; the note cannot be edited, because notes are append-only. "+
		"The value is not repeated here, and this is not reported again for the same value.",
		secret.kind, secret.item, beads.ExportPath)
	now := p.now()
	collected, err := report.Collect([]report.Entry{{
		Severity: report.SeverityCritical,
		Message:  oneline.Fold(message, report.MaxMessageBytes-len(oneline.Marker)),
	}}, report.Attribution{
		Role:         report.HarnessReporter,
		RunID:        secretRunPrefix + secret.fingerprint,
		Build:        p.Build,
		ProductID:    p.Product,
		RepositoryID: p.RepositoryID,
	}, now)
	if err != nil {
		return err
	}
	return p.Reports.Append(collected[0])
}

// readExportSecrets returns every whole secret in the export, one entry for
// each distinct value in each item, ordered by item, and how many lines were
// not work items. A line that does not decode is still read, as plain text,
// under the item id it would have, or "an unreadable line" where none is found.
func readExportSecrets(path string) ([]exportSecret, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	seen := map[string]bool{}
	var found []exportSecret
	unreadable := 0
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// A line longer than the reader's buffer is read whole, up to the bound.
			rest, readErr := readLongLine(reader, line)
			line, err = rest, readErr
		}
		if len(strings.TrimSpace(string(line))) > 0 {
			item, texts, ok := exportItem(line)
			if !ok {
				unreadable++
			}
			for _, text := range texts {
				for _, match := range execution.FindCompleteSecrets(text) {
					secret := exportSecret{item: item, kind: match.Kind, fingerprint: fingerprint(item, match.Kind, text[match.Start:match.End])}
					if !seen[secret.fingerprint] {
						seen[secret.fingerprint] = true
						found = append(found, secret)
					}
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].item != found[j].item {
			return found[i].item < found[j].item
		}
		if found[i].kind != found[j].kind {
			return found[i].kind < found[j].kind
		}
		return found[i].fingerprint < found[j].fingerprint
	})
	return found, unreadable, nil
}

func readLongLine(reader *bufio.Reader, start []byte) ([]byte, error) {
	line := append([]byte(nil), start...)
	for {
		more, err := reader.ReadSlice('\n')
		line = append(line, more...)
		if len(line) > maxExportLine {
			return nil, fmt.Errorf("a line of the export is longer than %d bytes", maxExportLine)
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, err
		}
	}
}

// exportItem is one line of the export as the item it records: its id, and
// every string in it, decoded, so a secret after an escaped line break or quote
// is read as the note holds it. A line that is not a JSON object is returned
// whole as its one text.
func exportItem(line []byte) (string, []string, bool) {
	var decoded map[string]any
	if err := json.Unmarshal(line, &decoded); err != nil {
		return "an unreadable line", []string{string(line)}, false
	}
	item, _ := decoded["id"].(string)
	if item == "" {
		item = "an item with no id"
	}
	var texts []string
	var walk func(value any)
	walk = func(value any) {
		switch value := value.(type) {
		case string:
			texts = append(texts, value)
		case []any:
			for _, element := range value {
				walk(element)
			}
		case map[string]any:
			for _, element := range value {
				walk(element)
			}
		}
	}
	walk(decoded)
	return item, texts, true
}

func fingerprint(item string, kind execution.SecretKind, value string) string {
	sum := sha256.Sum256([]byte(item + "\x00" + string(kind) + "\x00" + value))
	return hex.EncodeToString(sum[:6])
}
