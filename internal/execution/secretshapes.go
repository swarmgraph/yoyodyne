package execution

import "regexp"

// redactedMarker is what every redaction leaves in place of the secret.
const redactedMarker = "[REDACTED]"

// secretShape is one kind of secret recognized by how it looks rather than by
// its value. keep is the replacement template: it keeps whatever names the
// secret, such as a header's name, and puts the marker where the value was.
type secretShape struct {
	pattern *regexp.Regexp
	keep    string
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
var secretShapes = []secretShape{
	// An authorization header, in any of the forms a client prints one: HTTP,
	// JSON, a JavaScript object, or an environment-style assignment. The scheme
	// is kept so a reader can still tell a bearer token from basic credentials,
	// and a value already replaced is matched as itself so the scheme is not
	// taken for the value.
	{
		pattern: regexp.MustCompile(`(?i)((?:proxy-)?authorization(?:\\?["'])?\s*[:=](?:\s|\\[nrt])*(?:\\?["'])?(?:(?:bearer|basic|token|digest)(?:\s|\\[nrt])+)?)(?:\[REDACTED\]|[^\s"',;\\\[\]]+)`),
		keep:    "${1}" + redactedMarker,
	},
	// A bearer token wherever it appears without its header.
	{
		pattern: regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/=-]{8,}`),
		keep:    "${1}" + redactedMarker,
	},
	// A named credential field: an API key, any token, a secret, a password, a
	// cookie or session key, or the account a ChatGPT login is bound to. The
	// name may carry a prefix — x-api-key, OPENAI_API_KEY, refresh_token — and is
	// kept; a value shorter than eight characters, such as null or a count, is
	// not a credential and is left.
	{
		pattern: regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])(?:[A-Za-z0-9]+[_-])*(?:api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|token|secret|password|passwd|credentials?|session[_-]?key|cookie|account[_-]?id)(?:\\?["'])?\s*[:=](?:\s|\\[nrt])*(?:\\?["'])?)[^\s"',;&\\\[\]]{8,}`),
		keep:    "${1}" + redactedMarker,
	},
	// Anthropic keys and tokens: API keys (sk-ant-api03-), OAuth access and
	// refresh tokens (sk-ant-oat01-, sk-ant-ort01-), and session keys
	// (sk-ant-sid01-). OpenAI keys: sk-, sk-proj-, sk-svcacct-.
	{
		pattern: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`),
		keep:    redactedMarker,
	},
	// A JSON web token, which is what a ChatGPT login's access and id tokens
	// are: three base64url parts, the first two of them JSON objects.
	{
		pattern: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
		keep:    redactedMarker,
	},
	// GitHub tokens, which a session's own tools can print.
	{
		pattern: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})`),
		keep:    redactedMarker,
	},
}

// redactSecretShapes replaces every secret recognized by its shape.
func redactSecretShapes(value string) string {
	for _, shape := range secretShapes {
		value = shape.pattern.ReplaceAllString(value, shape.keep)
	}
	return value
}
