package execution

import "regexp"

// RedactedMarker is what every redaction leaves in place of the secret. It is
// exported so a caller holding text a role wrote can tell the role where a
// value was replaced, rather than letting the change pass silently.
const RedactedMarker = "[REDACTED]"

const redactedMarker = RedactedMarker

// secretShape is one kind of secret recognized by how it looks rather than by
// its value. keep is the replacement template: it keeps whatever names the
// secret, such as a header's name, and puts the marker where the value was.
//
// ordinary, where it is set, is asked about the pattern's second group, the
// value, and a match whose value it accepts is left as it was.
type secretShape struct {
	pattern  *regexp.Regexp
	keep     string
	ordinary func(value string) bool
}

// find returns the submatch indexes of every match of the shape in text that
// is a secret, leaving out any whose value is ordinary.
func (s secretShape) find(text string) [][]int {
	matches := s.pattern.FindAllStringSubmatchIndex(text, -1)
	if s.ordinary == nil {
		return matches
	}
	kept := matches[:0]
	for _, match := range matches {
		if len(match) >= 6 && match[4] >= 0 && s.ordinary(text[match[4]:match[5]]) {
			continue
		}
		kept = append(kept, match)
	}
	return kept
}

// replace puts the marker in place of every secret of this shape in text.
func (s secretShape) replace(text string) string {
	matches := s.find(text)
	if len(matches) == 0 {
		return text
	}
	out := make([]byte, 0, len(text))
	last := 0
	for _, match := range matches {
		out = append(out, text[last:match[0]]...)
		out = s.pattern.ExpandString(out, s.keep, text, match)
		last = match[1]
	}
	return string(append(out, text[last:]...))
}

// ordinaryWord reports a value made only of lowercase letters, in one word or
// several joined by single hyphens, underscores, or dots: keychain, file,
// macos-keychain. That is how a configuration value naming a store, a mode, or
// a kind of thing reads, and documents and examples have to be able to show
// one, such as "credential: keychain", exactly as written. A token, key, or
// generated password is not one: of random strings eight characters long, one
// in a thousand is all lowercase letters with no digit or capital, and fewer
// the longer they are. What this lets through is a password a person chose as
// lowercase words alone, which is a weaker secret than the ordinary text that
// masking it would rewrite.
func ordinaryWord(value string) bool {
	if value == "" {
		return false
	}
	separated := true
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			separated = false
		case r == '-' || r == '_' || r == '.':
			if separated {
				return false
			}
			separated = true
		default:
			return false
		}
	}
	return !separated
}

// secretShapes are the secrets a provider can print that the harness never
// held. A Redactor's values come from the harness's own environment, but Claude
// Code and Codex keep their logins in stores of their own — the keychain,
// ~/.codex/auth.json — so an API key, an OAuth access or refresh token, or a
// request's authorization header that one of them prints when it fails is a
// value no Redactor was given. The last lines a failed session wrote to
// standard error go onto the item's tracker notes, which are published with the
// repository, so these are removed by shape wherever a Redactor asks for it
// (WithSecretShapes): the provider adapters' output, and the failure text the
// pipeline writes onto those notes.
//
// Each shape is specific enough that ordinary output survives it: a session id,
// a commit, or a token count is not one. The value class of each excludes
// brackets, so a value already replaced is never replaced again, and excludes
// the backslash: a Redactor is applied to every line of a provider's JSON
// output as well as to standard error, and a value that took the backslash of
// an escaped quote with it would leave the line unreadable JSON, which fails the
// whole provider call. A name may be followed by an escaped quote for the same
// reason, so a field quoted inside a JSON string is still recognized.
//
// For the same reason a backslash is only ever taken as the whole of an escape:
// one before a quote, or an escaped line break or tab (\n, \r, \t) between a
// name and its value, as in a YAML file a session reads where "password:" ends
// its line. Taking a backslash alone would leave it before the marker, and
// `\[` is not a JSON escape. Skipping escaped line breaks also means a value on
// the line after its name is still found and removed.
//
// The shapes recognized by a prefix alone begin where shapeStart says.
var secretShapes = []secretShape{credentialHeaderShape, bearerShape, namedCredentialShape, providerKeyShape, webTokenShape, gitHubTokenShape}

var (
	// An authorization header, in any of the forms a client prints one: HTTP,
	// JSON, a JavaScript object, or an environment-style assignment. The scheme
	// is kept so a reader can still tell a bearer token from basic credentials,
	// and a value already replaced is matched as itself so the scheme is not
	// taken for the value.
	credentialHeaderShape = secretShape{
		pattern: regexp.MustCompile(`(?i)((?:proxy-)?authorization(?:\\?["'])?\s*[:=](?:\s|\\[nrt])*(?:\\?["'])?(?:(?:bearer|basic|token|digest)(?:\s|\\[nrt])+)?)(?:\[REDACTED\]|[^\s"',;\\\[\]]+)`),
		keep:    "${1}" + redactedMarker,
	}
	// A bearer token wherever it appears without its header.
	bearerShape = secretShape{
		pattern: regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
		keep:    "${1}" + redactedMarker,
	}
	// A named credential field: an API key, any token, a secret, a password, a
	// cookie or session key, or the account a ChatGPT login is bound to. The
	// name may carry a prefix — x-api-key, OPENAI_API_KEY, refresh_token — and is
	// kept; a value shorter than eight characters, such as null or a count, is
	// not a credential and is left, and neither is an ordinary lowercase word
	// such as the store in "credential: keychain" (ordinaryWord).
	namedCredentialShape = secretShape{
		pattern:  regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])(?:[A-Za-z0-9]+[_-])*(?:api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|token|secret|password|passwd|credentials?|session[_-]?key|cookie|account[_-]?id)(?:\\?["'])?\s*[:=](?:\s|\\[nrt])*(?:\\?["'])?)([^\s"',;&\\\[\]]{8,})`),
		keep:     "${1}" + redactedMarker,
		ordinary: ordinaryWord,
	}
	// Anthropic keys and tokens: API keys (sk-ant-api03-), OAuth access and
	// refresh tokens (sk-ant-oat01-, sk-ant-ort01-), and session keys
	// (sk-ant-sid01-). OpenAI keys: sk-, sk-proj-, sk-svcacct-.
	providerKeyShape = secretShape{
		pattern: regexp.MustCompile(shapeStart + `sk-[A-Za-z0-9_-]{20,}`),
		keep:    "${1}" + redactedMarker,
	}
	// A JSON web token, which is what a ChatGPT login's access and id tokens
	// are: three base64url parts, the first two of them JSON objects.
	webTokenShape = secretShape{
		pattern: regexp.MustCompile(shapeStart + `eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		keep:    "${1}" + redactedMarker,
	}
	// GitHub tokens, which a session's own tools can print.
	gitHubTokenShape = secretShape{
		pattern: regexp.MustCompile(shapeStart + `(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})`),
		keep:    "${1}" + redactedMarker,
	}
)

// shapeStart is where a shape recognized by its prefix alone — a provider key,
// a JSON web token, a GitHub token — may begin: the start of the text, a
// character that cannot be part of a word, or an escaped line break or tab
// (\n, \r, \t). A word boundary alone is not enough: on a raw stream line a
// secret that begins a line of the text follows the letter of the escape, as
// in `\nsk-…`, where there is no boundary. It is the first group of each such
// pattern and is kept in front of the marker, so the escape survives whole and
// the line is still JSON. A word that merely ends in n, r, or t right before
// the prefix is not an escape and does not start one.
const shapeStart = `(^|[^A-Za-z0-9_]|\\[nrt])`

// redactSecretShapes replaces every secret recognized by its shape.
func redactSecretShapes(value string) string {
	for _, shape := range secretShapes {
		value = shape.replace(value)
	}
	return value
}
