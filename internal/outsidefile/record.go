// Package outsidefile is how a file nobody in the project wrote reaches the
// repository without any developer run gaining network reach: the ruling in
// docs/designs/pinned-outside-files.md.
//
// A project declares a pinned outside file with a record beside it, named by
// adding RecordSuffix to the file's name. The harness fetches only records
// already on the target branch, in its own process, from hosts the project
// lists and under limits no configuration relaxes, and lands what it fetched
// through a reviewed run of its own. Every run compares every pinned file with
// its record before its checks (Compare), so a pinned file changed by hand, or
// the harness's fields written by anything but that run, fails the change.
//
// The package is trusted code holding the capability; the configuration names
// hosts and a size limit and nothing else (Policy).
package outsidefile

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// RecordSuffix is what is added to a pinned file's name to name its record.
const RecordSuffix = ".yoyodyne-source.yaml"

// MaxRecordBytes bounds a record read. A record is a handful of short fields,
// so anything near this is not a record.
const MaxRecordBytes = 64 << 10

// Record is one pinned outside file's declaration and, once the harness has
// fetched it, what that fetch found.
type Record struct {
	// Written by whoever declares the file.
	File        string `yaml:"file"`
	URL         string `yaml:"url"`
	Version     string `yaml:"version"`
	License     string `yaml:"license"`
	LicenseFile string `yaml:"license_file"`
	For         string `yaml:"for"`
	// SHA256 is the one field both sides write: the declarer gives it where the
	// source publishes a checksum, and the harness records it where they did not.
	SHA256 string `yaml:"sha256,omitempty"`
	// Written by the harness alone, when it fetches.
	Size        int64  `yaml:"size,omitempty"`
	FetchedFrom string `yaml:"fetched_from,omitempty"`
	FetchedAt   string `yaml:"fetched_at,omitempty"`
}

// IsRecord reports whether a repository path names a record.
func IsRecord(repositoryPath string) bool {
	name := path.Base(repositoryPath)
	return strings.HasSuffix(name, RecordSuffix) && len(name) > len(RecordSuffix)
}

// Records is the record paths among a listing of repository paths, in the
// order they were given.
func Records(paths []string) []string {
	var records []string
	for _, candidate := range paths {
		if IsRecord(candidate) {
			records = append(records, candidate)
		}
	}
	return records
}

// PinnedPath is the repository path of the file a record path declares.
func PinnedPath(recordPath string) string {
	return strings.TrimSuffix(recordPath, RecordSuffix)
}

// LicensePath is the repository path of the license file a record names.
func (r Record) LicensePath(recordPath string) string {
	return path.Join(path.Dir(recordPath), r.LicenseFile)
}

// Fetched reports whether the harness has written its fields into the record.
func (r Record) Fetched() bool {
	return r.Size != 0 || r.FetchedFrom != "" || r.FetchedAt != ""
}

// Declared is the record with the harness's own fields taken out, which is the
// part of it a person or a role wrote. A checksum is kept only where the record
// was not fetched, because once it has been the checksum may be the harness's.
func (r Record) Declared() Record {
	declared := r
	declared.Size, declared.FetchedFrom, declared.FetchedAt = 0, "", ""
	if r.Fetched() {
		declared.SHA256 = ""
	}
	return declared
}

var (
	sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// An SPDX identifier, or an expression joining several.
	licensePattern = regexp.MustCompile(`^[A-Za-z0-9.+-]+( (AND|OR|WITH) [A-Za-z0-9.+-]+)*$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.+_-]*$`)
	// A work item id of the shape the tracker gives one: a prefix, a hyphen,
	// and the rest. The tracker itself is asked whether it holds the item
	// before anything is fetched for it; this package sits beneath the
	// configuration and does not reach the tracker.
	workItemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._]*-[A-Za-z0-9._-]+$`)
)

// RecordError is a failure that is about the record: something no later
// attempt gets past while the record says what it says. It stops the record
// until the record changes on the target branch, and is reported to the
// development manager. Everything else that stops a fetch is the network's
// and is waited out (NetworkError).
type RecordError struct {
	Record string
	Reason string
}

func (e *RecordError) Error() string {
	if e.Record == "" {
		return e.Reason
	}
	return e.Record + ": " + e.Reason
}

func recordError(record, format string, args ...any) *RecordError {
	return &RecordError{Record: record, Reason: fmt.Sprintf(format, args...)}
}

// IsRecordError reports whether err is about the record rather than the
// network.
func IsRecordError(err error) bool {
	var refused *RecordError
	return errors.As(err, &refused)
}

// ParseRecord reads and validates the record at recordPath. Unknown fields are
// refused, so a typo fails rather than declaring something nobody meant.
func ParseRecord(recordPath string, content []byte) (Record, error) {
	if len(content) > MaxRecordBytes {
		return Record{}, recordError(recordPath, "the record is %d bytes, more than the %d a record may be", len(content), MaxRecordBytes)
	}
	var record Record
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&record); err != nil {
		return Record{}, recordError(recordPath, "the record cannot be read: %v", err)
	}
	if err := record.Validate(recordPath); err != nil {
		return Record{}, err
	}
	return record, nil
}

// Validate checks a record's fields against the path it is filed at.
func (r Record) Validate(recordPath string) error {
	if !IsRecord(recordPath) {
		return recordError(recordPath, "a record's name ends in %s", RecordSuffix)
	}
	var problems []string
	want := path.Base(PinnedPath(recordPath))
	if r.File != want {
		problems = append(problems, fmt.Sprintf("file is %q, and a record named %s declares %q", r.File, path.Base(recordPath), want))
	}
	if r.URL == "" {
		problems = append(problems, "url is empty")
	}
	if !versionPattern.MatchString(r.Version) {
		problems = append(problems, fmt.Sprintf("version %q is not one exact version", r.Version))
	}
	if !licensePattern.MatchString(r.License) {
		problems = append(problems, fmt.Sprintf("license %q is not an SPDX identifier", r.License))
	}
	if r.LicenseFile == "" || r.LicenseFile != path.Base(r.LicenseFile) || r.LicenseFile == "." || r.LicenseFile == ".." {
		problems = append(problems, fmt.Sprintf("license_file %q is not a file in the record's directory", r.LicenseFile))
	}
	if !workItemPattern.MatchString(r.For) {
		problems = append(problems, fmt.Sprintf("for %q is not a work item id", r.For))
	}
	if r.SHA256 != "" && !sha256Pattern.MatchString(r.SHA256) {
		problems = append(problems, fmt.Sprintf("sha256 %q is not 64 lowercase hexadecimal digits", r.SHA256))
	}
	if r.Fetched() {
		if r.SHA256 == "" || r.Size <= 0 || r.FetchedFrom == "" || r.FetchedAt == "" {
			problems = append(problems, "the harness's fields (sha256, size, fetched_from, fetched_at) are written together or not at all")
		}
		if r.FetchedAt != "" {
			if _, err := time.Parse(time.RFC3339, r.FetchedAt); err != nil {
				problems = append(problems, fmt.Sprintf("fetched_at %q is not a time", r.FetchedAt))
			}
		}
	}
	if len(problems) > 0 {
		return recordError(recordPath, "%s", strings.Join(problems, "; "))
	}
	return nil
}

// Due reports whether the harness should fetch a record on the target branch:
// its file is missing, or it carries no checksum.
func (r Record) Due(filePresent bool) bool {
	return !filePresent || r.SHA256 == ""
}

// WithHarnessFields is the record's content with the harness's fields set from
// what a fetch found. It edits the record in place rather than writing it
// again, so everything the declarer wrote, comments included, is kept and the
// change to the record is only the harness's fields.
func WithHarnessFields(recordPath string, content []byte, fetched Fetched) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return nil, recordError(recordPath, "the record cannot be read: %v", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, recordError(recordPath, "the record is not one mapping of fields")
	}
	mapping := document.Content[0]
	set := func(key, value string, tag string) {
		for index := 0; index+1 < len(mapping.Content); index += 2 {
			if mapping.Content[index].Value == key {
				mapping.Content[index+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
				return
			}
		}
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
	}
	set("sha256", fetched.SHA256, "!!str")
	set("size", strconv.FormatInt(fetched.Size, 10), "!!int")
	set("fetched_from", fetched.FinalURL, "!!str")
	set("fetched_at", fetched.At.UTC().Format(time.RFC3339), "!!str")
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	written, err := ParseRecord(recordPath, out.Bytes())
	if err != nil {
		return nil, err
	}
	if written.SHA256 != fetched.SHA256 || written.Size != fetched.Size || written.FetchedFrom != fetched.FinalURL {
		return nil, recordError(recordPath, "the record does not read back as what the fetch found")
	}
	return out.Bytes(), nil
}
