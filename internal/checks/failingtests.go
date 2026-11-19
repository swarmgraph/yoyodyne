package checks

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
)

// Failing tests: how a failed check's output names each failing test and the
// file it is in, read by a reading the project declares (failing_tests in the
// configuration) rather than by anything the harness knows about a language.
//
// A reading is up to three regular expressions, each matched against one line
// of the check's output at a time:
//
//   - Test opens a failing test. Its "test" group is the test's name, and a
//     "file" group, where the runner prints both on one line, is its file.
//   - File, where declared, names the file of the latest failing test that has
//     none yet, by its "file" group. A line matching it with no such test is
//     not about a failing test and is passed over.
//   - Group, where declared, closes the failing tests named since the last one:
//     its "directory" group, less StripPrefix, is the directory their files are
//     in. A group line with no failing test before it is a failure that named no
//     test, and a failing test with no group line after it is output cut short;
//     either one means the failure could not be read as named tests.
//
// The reading is all or nothing. Where any failure in the output cannot be
// named as a test with its file, the result says why and lists no tests, and a
// check read that way is handled exactly as a check with no reading is. A
// reading only ever describes a check's failure; it never changes whether the
// check passed. The design that asks for it is "A check that fails only in
// tests the change does not touch" in docs/designs/recoverable-and-terminal-failures.md.

// The named groups a reading's expressions are read by.
const (
	readingTestGroup      = "test"
	readingFileGroup      = "file"
	readingDirectoryGroup = "directory"
)

// MaxFailingTests is how many failing tests one check's record lists; the rest
// are counted in FailingTests.More.
const MaxFailingTests = 20

// The bounds on what one failing test, and the reason a failure could not be
// read, may take on the record.
const (
	maxFailingTestName   = 200
	maxFailingTestFile   = 300
	maxFailingTestUnread = 300
	maxReadingLine       = 4096
)

// TestReading is how one or more configured checks name their failing tests,
// as the project declares it under failing_tests.
type TestReading struct {
	// Checks names the configured checks this reading applies to, the way
	// execution.heavy_checks names them: a command is a named check where it is
	// the name, or begins with the name and then a space. The longest name that
	// matches a command decides which reading it gets.
	Checks []string `yaml:"checks" json:"checks"`
	// Test is the expression for a line opening a failing test, with a "test"
	// group and, optionally, a "file" group.
	Test string `yaml:"test" json:"test"`
	// File is the expression for a line naming the file of the failing test
	// before it, with a "file" group. It may be empty where Test names the file.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// Group is the expression for a line closing the failing tests before it,
	// with a "directory" group naming the directory their files are in.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
	// StripPrefix is taken off the start of each directory Group names, so a
	// name the runner prints, such as a Go import path, becomes a directory in
	// the repository. A directory equal to it is the repository's top.
	StripPrefix string `yaml:"strip_prefix,omitempty" json:"strip_prefix,omitempty"`
}

// FailingTest is one failing test, and the file it is in relative to the
// repository's top.
type FailingTest struct {
	Name string `json:"name"`
	File string `json:"file"`
}

// FailingTests is what a declared reading made of one failed check's output.
type FailingTests struct {
	// Tests are the failing tests, first named first, at most MaxFailingTests.
	// It is empty whenever Unread is set.
	Tests []FailingTest `json:"tests,omitempty"`
	// More is how many further failing tests were named past MaxFailingTests.
	More int `json:"more,omitempty"`
	// Unread is why the failure could not be read as failing tests each with
	// its file, and empty where it was.
	Unread string `json:"unread,omitempty"`
}

// Named reports whether every failing test was named with its file.
func (f FailingTests) Named() bool {
	return f.Unread == "" && len(f.Tests) > 0
}

// Describe is the failing tests in one line of ordinary words, for a run's
// record, a work item's notes, and a hand-back.
func (f FailingTests) Describe() string {
	if !f.Named() {
		unread := f.Unread
		if unread == "" {
			unread = "the output names no failing test"
		}
		return "The failing tests could not be named with their files from the check's output, by the reading the project declares: " + unread + "."
	}
	parts := make([]string, 0, len(f.Tests))
	for _, test := range f.Tests {
		parts = append(parts, fmt.Sprintf("%s (%s)", test.Name, test.File))
	}
	text := "Failing tests, each with its file: " + strings.Join(parts, ", ")
	if f.More > 0 {
		text += fmt.Sprintf(", and %d more", f.More)
	}
	return text + "."
}

// ReadingFor is the reading that applies to a configured command: the one
// naming the longest check the command is. ok is false where none applies.
func ReadingFor(readings []TestReading, command string) (reading TestReading, ok bool) {
	command = strings.TrimSpace(command)
	longest := -1
	for _, candidate := range readings {
		for _, name := range candidate.Checks {
			name = strings.TrimSpace(name)
			if ReadingNames(name, command) && len(name) > longest {
				reading, ok, longest = candidate, true, len(name)
			}
		}
	}
	return reading, ok
}

// ReadingNames reports whether a check name in a reading names a command.
func ReadingNames(name, command string) bool {
	name, command = strings.TrimSpace(name), strings.TrimSpace(command)
	if name == "" || !strings.HasPrefix(command, name) {
		return false
	}
	rest := command[len(name):]
	return rest == "" || unicode.IsSpace(rune(rest[0]))
}

// Problems is everything wrong with a reading as written, in the words the
// configuration's refusal uses, and nothing for one that can be used.
func (r TestReading) Problems() []string {
	var problems []string
	if len(r.Checks) == 0 {
		problems = append(problems, "names no check it applies to")
	}
	for _, name := range r.Checks {
		if strings.TrimSpace(name) == "" {
			problems = append(problems, "names a check with no name")
		}
	}
	if _, err := r.compile(); err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

// compiledReading is a reading with its expressions compiled.
type compiledReading struct {
	test, file, group *regexp.Regexp
	stripPrefix       string
}

func (r TestReading) compile() (compiledReading, error) {
	compiled := compiledReading{stripPrefix: strings.Trim(strings.TrimSpace(r.StripPrefix), "/")}
	if strings.TrimSpace(r.Test) == "" {
		return compiled, fmt.Errorf("has no test expression")
	}
	var err error
	if compiled.test, err = compileReadingExpression("test", r.Test, readingTestGroup); err != nil {
		return compiled, err
	}
	if strings.TrimSpace(r.File) != "" {
		if compiled.file, err = compileReadingExpression("file", r.File, readingFileGroup); err != nil {
			return compiled, err
		}
	} else if compiled.test.SubexpIndex(readingFileGroup) < 0 {
		return compiled, fmt.Errorf("has no file expression, and its test expression has no (?P<file>...) group, so nothing names a failing test's file")
	}
	if strings.TrimSpace(r.Group) != "" {
		if compiled.group, err = compileReadingExpression("group", r.Group, readingDirectoryGroup); err != nil {
			return compiled, err
		}
	} else if compiled.stripPrefix != "" {
		return compiled, fmt.Errorf("has a strip_prefix but no group expression for it to apply to")
	}
	return compiled, nil
}

func compileReadingExpression(field, expression, group string) (*regexp.Regexp, error) {
	compiled, err := regexp.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("has a %s expression that is not a regular expression: %v", field, err)
	}
	if compiled.SubexpIndex(group) < 0 {
		return nil, fmt.Errorf("has a %s expression with no (?P<%s>...) group", field, group)
	}
	return compiled, nil
}

// testReader reads a check's output, line by line, through one reading.
type testReader struct {
	reading compiledReading
	broken  string
	// pending are the failing tests named since the last group line, and
	// current is the one a file line names the file of.
	pending []FailingTest
	current int
	tests   []FailingTest
	seen    map[FailingTest]bool
	more    int
	unread  string
}

func newTestReader(reading TestReading) *testReader {
	reader := &testReader{current: -1, seen: map[FailingTest]bool{}}
	compiled, err := reading.compile()
	if err != nil {
		reader.broken = "the reading " + err.Error()
	}
	reader.reading = compiled
	return reader
}

// add reads a piece of output made of whole lines, which is how the process
// runner hands a check's output over: one line at a time.
func (t *testReader) add(text string) {
	if t.broken != "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		t.line(line)
	}
}

func (t *testReader) line(line string) {
	line = strings.TrimRight(line, "\r")
	if len(line) > maxReadingLine {
		line = line[:maxReadingLine]
	}
	reading := t.reading
	if match := reading.test.FindStringSubmatch(line); match != nil {
		name := strings.TrimSpace(match[reading.test.SubexpIndex(readingTestGroup)])
		file := ""
		if index := reading.test.SubexpIndex(readingFileGroup); index >= 0 {
			file = strings.TrimSpace(match[index])
		}
		if name == "" {
			t.fail("a line opening a failing test named none: " + line)
			return
		}
		for index, test := range t.pending {
			if test.Name == name {
				t.current = index
				if test.File == "" && file != "" {
					t.pending[index].File = file
				}
				return
			}
		}
		t.pending = append(t.pending, FailingTest{Name: name, File: file})
		t.current = len(t.pending) - 1
		if reading.group == nil {
			t.flush("")
		}
		return
	}
	if reading.file != nil && t.current >= 0 && t.pending[t.current].File == "" {
		if match := reading.file.FindStringSubmatch(line); match != nil {
			t.pending[t.current].File = strings.TrimSpace(match[reading.file.SubexpIndex(readingFileGroup)])
			return
		}
	}
	if reading.group != nil {
		if match := reading.group.FindStringSubmatch(line); match != nil {
			directory := strings.TrimSpace(match[reading.group.SubexpIndex(readingDirectoryGroup)])
			if len(t.pending) == 0 {
				t.fail(fmt.Sprintf("%s failed without naming a failing test", directory))
				return
			}
			t.flush(directory)
		}
	}
}

// flush records the pending tests, their files in directory where a group
// named one.
func (t *testReader) flush(directory string) {
	pending := t.pending
	t.pending, t.current = nil, -1
	if directory != "" && t.reading.stripPrefix != "" {
		switch {
		case directory == t.reading.stripPrefix:
			directory = "."
		case strings.HasPrefix(directory, t.reading.stripPrefix+"/"):
			directory = strings.TrimPrefix(directory, t.reading.stripPrefix+"/")
		default:
			t.fail(fmt.Sprintf("%s does not begin with %s, so it is not a directory in the repository", directory, t.reading.stripPrefix))
			return
		}
	}
	for _, test := range pending {
		if test.File == "" {
			t.fail(test.Name + " was named failing without the file it is in")
			continue
		}
		file := test.File
		if directory != "" {
			file = path.Join(directory, file)
		}
		file = path.Clean(strings.ReplaceAll(file, "\\", "/"))
		if path.IsAbs(file) || file == ".." || strings.HasPrefix(file, "../") {
			t.fail(fmt.Sprintf("%s was named failing in %s, which is not a file in the repository", test.Name, file))
			continue
		}
		named := FailingTest{Name: cutRecordText(test.Name, maxFailingTestName), File: cutRecordText(file, maxFailingTestFile)}
		if t.seen[named] {
			continue
		}
		t.seen[named] = true
		if len(t.tests) >= MaxFailingTests {
			t.more++
			continue
		}
		t.tests = append(t.tests, named)
	}
}

// fail keeps the first reason the output could not be read.
func (t *testReader) fail(reason string) {
	if t.unread == "" {
		t.unread = cutRecordText(reason, maxFailingTestUnread)
	}
}

// finish is what the output read as, once the check has ended.
func (t *testReader) finish() FailingTests {
	if t.broken != "" {
		return FailingTests{Unread: cutRecordText(t.broken, maxFailingTestUnread)}
	}
	if len(t.pending) > 0 {
		names := make([]string, 0, len(t.pending))
		for _, test := range t.pending {
			names = append(names, test.Name)
		}
		t.fail(fmt.Sprintf("%s were named failing with no line after them saying where, so the output may be cut short", strings.Join(names, ", ")))
	}
	if t.unread == "" && len(t.tests) == 0 {
		t.unread = "the output names no failing test"
	}
	if t.unread != "" {
		return FailingTests{Unread: t.unread}
	}
	return FailingTests{Tests: append([]FailingTest(nil), t.tests...), More: t.more}
}

// ReadFailingTests reads a failed check's whole output through a reading.
func ReadFailingTests(reading TestReading, output string) FailingTests {
	reader := newTestReader(reading)
	reader.add(output)
	return reader.finish()
}

// cutRecordText keeps the start of a value within a limit, on a rune boundary.
func cutRecordText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && (value[cut]&0xC0) == 0x80 {
		cut--
	}
	return value[:cut]
}
