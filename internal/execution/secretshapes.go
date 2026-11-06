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
// repository, so these are removed by shape wherever a Redactor is applied.
//
// Each shape is specific enough that ordinary output survives it: a session id,
// a commit, or a token count is not one. The value class of each excludes
// brackets, so a value already replaced is never replaced again.
var secretShapes = []secretShape{
	// An authorization header, in any of the forms a client prints one: HTTP,
	// JSON, a JavaScript object, or an environment-style assignment. The scheme
	// is kept so a reader can still tell a bearer token from basic credentials,
	// and a value already replaced is matched as itself so the scheme is not
	// taken for the value.
	{
		pattern: regexp.MustCompile(`(?i)((?:proxy-)?authorization["']?\s*[:=]\s*["']?(?:(?:bearer|basic|token|digest)\s+)?)(?:\[REDACTED\]|[^\s"',;\[\]]+)`),
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
		pattern: regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9])(?:[A-Za-z0-9]+[_-])*(?:api[_-]?key|access[_-]?key|secret[_-]?key|private[_-]?key|token|secret|password|passwd|credentials?|session[_-]?key|cookie|account[_-]?id)["']?\s*[:=]\s*["']?)[^\s"',;&\[\]]{8,}`),
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
