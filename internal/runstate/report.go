package runstate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/home"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// maxEncodedReportBytes bounds one encoded report, including the trailing
// newline. The writer and the reader share it, so a report that was written is
// always one that can be read back.
const maxEncodedReportBytes = 64 << 10

// ReportStore is where what agents noticed is collected, in the same
// operating-system state root as runs and conversations and beside them rather
// than among them. It is one append-only log per product because a report
// outlives the run or conversation that produced it: a run's state is settled
// and cleaned up, and what it reported is still the operator's to read.
type ReportStore struct {
	root      string
	productID domain.ProductID
}

func NewReportStore(root string, productID domain.ProductID) (*ReportStore, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("state root must be an absolute path")
	}
	if err := domain.ValidateIdentifier("product id", string(productID)); err != nil {
		return nil, err
	}
	return &ReportStore{
		root:      home.ProductDirectory(root, string(productID)),
		productID: productID,
	}, nil
}

func (s *ReportStore) Root() string { return s.root }

// Path names the log itself, so a failure can say where the collected reports
// actually are.
func (s *ReportStore) Path() string { return filepath.Join(s.root, "reports.jsonl") }

// HandlingPath names the log of what became of those reports. It is a second
// file beside the first rather than a column in it, which is the whole shape of
// this: the pile is written once by whoever noticed something and is never
// rewritten, and a disposition recorded later must not be able to touch it.
func (s *ReportStore) HandlingPath() string { return filepath.Join(s.root, "report-handlings.jsonl") }

// Append records one report durably. It is an append rather than a rewrite for
// the same reason the event logs are: a report is written once and never
// revised, and two processes reporting at the same time must not overwrite each
// other's record.
func (s *ReportStore) Append(reported report.Report) error {
	if err := s.validate(reported); err != nil {
		return err
	}
	encoded, err := encodeReport(reported)
	if err != nil {
		return err
	}
	return s.locked(func() error { return s.appendLine(s.Path(), "report", encoded) })
}

// RepeatPath names the log of reports kept as repeats of a standing one. It is
// a third file beside the reports and the handlings, for the reason the
// handlings are a second: the report repeated is never rewritten.
func (s *ReportStore) RepeatPath() string { return filepath.Join(s.root, "report-repeats.jsonl") }

// lockPath names the file a report, a repeat and a handling are each written
// under, so the check that a standing report is still unhandled and the
// writing of a repeat of it are one step no handling can come between.
func (s *ReportStore) lockPath() string { return filepath.Join(s.root, "reports.lock") }

func (s *ReportStore) locked(write func() error) error {
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	file, err := os.OpenFile(s.lockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open report lock: %w", err)
	}
	defer closeStateFile(file)
	if err := lockStateFile(context.Background(), file); err != nil {
		return fmt.Errorf("lock reports: %w", err)
	}
	defer unlockStateFile(file)
	return write()
}

// Filed is what became of one report a run filed: kept as a new report, or as
// a repeat of a standing one, with that report's count of repeats once this
// one is counted. NotUsed says why a standing report the run named could not
// be used, for the run's closing lines.
type Filed struct {
	Report  report.Report
	Repeat  *report.Repeat
	Count   int
	NotUsed string
}

// File records one report a run filed, as a repeat of the standing report it
// says again where report.Match finds one and as a new report otherwise. The
// match is made under the lock every report, repeat and handling is written
// under, against the pile as it stands then, so a handling recorded since the
// run was handed its list sends the report in as a new one.
//
// A pile, handlings or repeats that cannot be read are no reason to lose the
// report: it is filed as a new one.
func (s *ReportStore) File(reported report.Report, named string, offered []string) (Filed, error) {
	if err := s.validate(reported); err != nil {
		return Filed{}, err
	}
	var filed Filed
	err := s.locked(func() error {
		reports, _, readErr := s.Scan()
		handlings, handlingErr := s.Handlings()
		repeats, repeatErr := s.Repeats()
		standing, named, notUsed := "", named, ""
		if readErr == nil && handlingErr == nil && repeatErr == nil {
			var byName bool
			standing, byName, notUsed = report.Match(reported, named, report.Matching{Reports: reports, Handlings: handlings, Offered: offered})
			if !byName {
				named = ""
			}
		} else if named != "" {
			notUsed = "the reports standing in this project could not be read to check the report it names"
		}
		if standing == "" {
			encoded, err := encodeReport(reported)
			if err != nil {
				return err
			}
			if err := s.appendLine(s.Path(), "report", encoded); err != nil {
				return err
			}
			filed = Filed{Report: reported, NotUsed: notUsed}
			return nil
		}
		repeat := report.RepeatOf(reported, standing, named != "")
		if err := repeat.Validate(); err != nil {
			return err
		}
		encoded, err := encodeLogLine(repeat, "report repeat")
		if err != nil {
			return err
		}
		if err := s.appendLine(s.RepeatPath(), "report repeat", encoded); err != nil {
			return err
		}
		count := 1
		for _, earlier := range repeats {
			if earlier.ReportID == standing {
				count++
			}
		}
		filed = Filed{Report: reported, Repeat: &repeat, Count: count}
		return nil
	})
	return filed, err
}

// Repeats returns every repeat recorded, in the order it was recorded. A line
// that will not decode is dropped rather than failing the read: it is a repeat
// a restart cut short, and the report it repeats still stands whole.
func (s *ReportStore) Repeats() ([]report.Repeat, error) {
	file, err := os.Open(s.RepeatPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open report repeat log: %w", err)
	}
	defer file.Close()
	var repeats []report.Repeat
	_, err = scanLog(file, maxEncodedReportBytes, func(line []byte) error {
		var decoded report.Repeat
		if err := json.Unmarshal(line, &decoded); err != nil {
			return err
		}
		if decoded.ProductID != s.productID {
			return fmt.Errorf("report repeat product %q does not match store product %q", decoded.ProductID, s.productID)
		}
		if err := decoded.Validate(); err != nil {
			return err
		}
		repeats = append(repeats, decoded)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read report repeat log: %w", err)
	}
	return repeats, nil
}

// Handle records what became of one report. It is an append to a second log
// rather than a change to the first, so a report and the decision about it are
// two facts recorded by two hands at two times, and reading the pile can never
// be reading something somebody edited afterwards.
//
// Handling a report twice is not refused here. The store keeps both records and
// the later one is what is read, which is what an append-only log is for: a
// second decision about something is history worth having, not a write to
// reject.
func (s *ReportStore) Handle(handling report.Handling) error {
	if err := s.validateHandling(handling); err != nil {
		return err
	}
	encoded, err := encodeReportHandling(handling)
	if err != nil {
		return err
	}
	return s.locked(func() error { return s.appendLine(s.HandlingPath(), "report handling", encoded) })
}

// appendLine writes one encoded record to one of the logs, durably. Both logs
// are written exactly the same way and for the same reason — two processes
// recording at once must not overwrite each other — so the writing is one piece
// of code and what it is writing is a word in the failures.
func (s *ReportStore) appendLine(path, what string, encoded []byte) error {
	if len(encoded) > maxEncodedReportBytes {
		return fmt.Errorf("encoded %s is %d bytes, limit is %d", what, len(encoded), maxEncodedReportBytes)
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return fmt.Errorf("inspect %s log: %w", what, statErr)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open %s log: %w", what, err)
	}
	written, err := file.Write(encoded)
	if err != nil {
		file.Close()
		return fmt.Errorf("append %s: %w", what, err)
	}
	if written != len(encoded) {
		file.Close()
		return fmt.Errorf("append %s: %w", what, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync %s log: %w", what, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s log: %w", what, err)
	}
	if created {
		return syncDirectory(s.root)
	}
	return nil
}

// List returns every collected report in the order it was recorded. A log that
// does not exist yet is a product nothing has reported about, which is not a
// failure to read. A line that will not decode is: a pile that quietly dropped
// what it could not parse is one nobody can trust to be complete, and a surface
// that lists the pile names the failure instead. The sink reads past such a line
// by position with Scan, and says so.
func (s *ReportStore) List() ([]report.Report, error) {
	reports, skipped, err := s.Scan()
	if err != nil {
		return nil, err
	}
	if err := firstSkipped("report log", skipped); err != nil {
		return nil, err
	}
	return reports, nil
}

// Scan returns every report that decoded, in the order it was recorded, and
// beside them the lines that would not, each at the position it holds among the
// records. It is the read a positional cursor is kept against: a report after a
// torn line has the position it would have had without the tear, so one bad
// line costs the reader that line and nothing behind it.
func (s *ReportStore) Scan() ([]report.Report, []SkippedLine, error) {
	file, err := os.Open(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open report log: %w", err)
	}
	defer file.Close()

	var reports []report.Report
	skipped, err := scanLog(file, maxEncodedReportBytes, func(line []byte) error {
		decoded, err := decodeReport(line)
		if err != nil {
			return err
		}
		if err := s.validate(decoded); err != nil {
			return err
		}
		reports = append(reports, decoded)
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("read report log: %w", err)
	}
	return reports, skipped, nil
}

// Handlings returns every recorded disposition in the order it was recorded. A
// log that does not exist yet is a pile nobody has worked through, which is not
// a failure to read. A line that will not decode is, for the reason it is on the
// pile itself.
func (s *ReportStore) Handlings() ([]report.Handling, error) {
	file, err := os.Open(s.HandlingPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open report handling log: %w", err)
	}
	defer file.Close()

	var handlings []report.Handling
	skipped, err := scanLog(file, maxEncodedReportBytes, func(line []byte) error {
		var decoded report.Handling
		if err := json.Unmarshal(line, &decoded); err != nil {
			return err
		}
		if err := s.validateHandling(decoded); err != nil {
			return err
		}
		handlings = append(handlings, decoded)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read report handling log: %w", err)
	}
	if err := firstSkipped("report handling log", skipped); err != nil {
		return nil, err
	}
	return handlings, nil
}

func decodeReport(data []byte) (report.Report, error) {
	var decoded report.Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		return report.Report{}, err
	}
	if err := decoded.Validate(); err != nil {
		return report.Report{}, err
	}
	return decoded, nil
}

func encodeReport(reported report.Report) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(reported); err != nil {
		return nil, fmt.Errorf("encode report: %w", err)
	}
	return buffer.Bytes(), nil
}

func encodeLogLine(record any, what string) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, fmt.Errorf("encode %s: %w", what, err)
	}
	return buffer.Bytes(), nil
}

func encodeReportHandling(handling report.Handling) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(handling); err != nil {
		return nil, fmt.Errorf("encode report handling: %w", err)
	}
	return buffer.Bytes(), nil
}

func (s *ReportStore) validate(reported report.Report) error {
	if reported.ProductID != s.productID {
		return fmt.Errorf("report product %q does not match store product %q", reported.ProductID, s.productID)
	}
	return reported.Validate()
}

func (s *ReportStore) validateHandling(handling report.Handling) error {
	if handling.ProductID != s.productID {
		return fmt.Errorf("report handling product %q does not match store product %q", handling.ProductID, s.productID)
	}
	return handling.Validate()
}
