package backend

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// What an invocation was sent, as the adapter that sends it states it.
//
// A developer's or reviewer's invocation keeps the whole text it was sent, so a
// person can read afterwards exactly what the provider was given (the design is
// docs/designs/what-each-invocation-was-sent.md). The adapter is where that text
// is final: it adds what the role's contract does not carry itself, such as a
// reviewer's note on its tools and the skills and instruction files the project
// names. So the adapter states what it is about to send, in order and in named
// parts, and the invocation is started only once that has been recorded. The
// text the provider receives is the parts joined, and never text put together a
// second time beside them.

// SentPart is one named piece of what an invocation was sent.
type SentPart struct {
	// Name says what the piece is, in the words a person reading the record
	// would use: "system instructions", "first message".
	Name string
	// Origin says where the piece came from, so a reader can find its source.
	Origin string
	// Text is the piece exactly as sent. A piece that is followed by another in
	// the same message carries the separator between them at its end.
	Text string
}

// Where a part came from is one of three things, and is written the same way
// wherever it is made, so a person reading a record can find the source:
// a file in a repository with the commit it was read at, a tracker item with
// the time it was read, or the harness's own build.

// OriginHarnessBuild is the origin of text the harness wrote into its program.
func OriginHarnessBuild() string {
	return "the harness build " + buildinfo.Commit()
}

// OriginRepositoryFile is the origin of text read from a file in a repository.
// Where the commit is not known, what pins the file instead is said in detail.
func OriginRepositoryFile(path, commit string) string {
	if strings.TrimSpace(commit) == "" {
		return "repository file " + path
	}
	return fmt.Sprintf("repository file %s at commit %s", path, commit)
}

// OriginTrackerItem is the origin of text read from a tracker item.
func OriginTrackerItem(item string, readAt time.Time) string {
	return fmt.Sprintf("tracker item %s, read at %s", item, readAt.UTC().Format(time.RFC3339))
}

// JoinedText is the text of parts joined.
func JoinedText(parts []SentPart) string {
	return SentMessage{Parts: parts}.Text()
}

// PartsOr returns parts when they are the named pieces of exactly whole, and
// otherwise a single part holding whole, so what is recorded is always the text
// that is sent whatever a caller said about how it was made. whole may be empty,
// which has no parts.
func PartsOr(parts []SentPart, whole string, fallback SentPart) []SentPart {
	if whole == "" {
		return nil
	}
	if len(parts) > 0 && JoinedText(parts) == whole {
		return append([]SentPart(nil), parts...)
	}
	fallback.Text = whole
	return []SentPart{fallback}
}

// Roles a sent message is spoken in.
const (
	SentSystem = "system"
	SentUser   = "user"
)

// SentMessage is one message the provider was given, as its parts in order.
type SentMessage struct {
	Role  string
	Parts []SentPart
}

// Text is the message exactly as sent: its parts, joined.
func (m SentMessage) Text() string {
	var text strings.Builder
	for _, part := range m.Parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

// SentInvocation is everything an adapter is about to send for one invocation:
// its messages in order, and the settings that shape what the provider does
// with them.
type SentInvocation struct {
	// Backend is the adapter's name, and ProviderVersion what the provider's own
	// program said its version is, empty where it would not say.
	Backend         string
	ProviderVersion string
	Model           string
	Effort          string
	// Tools is the tool list the invocation was started with, empty for a
	// provider that scopes tools by a permission profile instead, which is then
	// named in Posture.
	Tools            []string
	Posture          string
	WorkingDirectory string
	// Resumed is the provider session the invocation continues, empty for a
	// fresh one.
	Resumed  string
	Messages []SentMessage
}

// InvocationRecorder keeps what an invocation was sent before the provider is
// started. A request names the recorder for its own invocation, so the record
// is the run's and the role's it was made for.
type InvocationRecorder interface {
	RecordInvocation(sent SentInvocation) error
}

// InvocationLog makes the recorder for one run's invocations in one role. The
// run store is the one implementation (runstate.InvocationStore); commit is the
// commit the working directory is at, and now the time an invocation started.
type InvocationLog interface {
	Recorder(runID string, role domain.AgentRole, commit string, now func() time.Time) InvocationRecorder
}

// RecordInvocation hands what an adapter is about to send to the request's
// recorder, and is the point an adapter must pass before it starts the
// provider. A request naming no recorder has nothing to record, which is every
// role whose invocations are not kept; one whose recorder fails is refused, so
// an invocation that was not kept is never started.
func RecordInvocation(request RunRequest, sent SentInvocation) error {
	if request.Recorder == nil {
		return nil
	}
	if err := request.Recorder.RecordInvocation(sent); err != nil {
		return &InvocationNotRecordedError{Cause: err}
	}
	return nil
}

// InvocationNotRecordedError says an invocation was not started because what it
// was to be sent could not be written down first.
type InvocationNotRecordedError struct{ Cause error }

func (e *InvocationNotRecordedError) Error() string {
	return "the provider was not started because what it was to be sent could not be recorded first: " + e.Cause.Error()
}

func (e *InvocationNotRecordedError) Unwrap() error { return e.Cause }

// IsInvocationNotRecorded reports an error that is a refusal to start for want
// of a record.
func IsInvocationNotRecorded(err error) bool {
	var refused *InvocationNotRecordedError
	return errors.As(err, &refused)
}
