package execution

import (
	"sort"
	"strings"
	"unicode"
)

// SecretKind names a kind of secret FindCompleteSecrets finds, in words a
// person reads in a finding.
type SecretKind string

const (
	SecretAnthropicKey SecretKind = "Anthropic key or token"
	SecretOpenAIKey    SecretKind = "OpenAI key"
	SecretWebToken     SecretKind = "JSON web token"
	SecretGitHubToken  SecretKind = "GitHub token"
	SecretBearerToken  SecretKind = "bearer token"
	SecretHeaderValue  SecretKind = "authorization header value"
)

// FoundSecret is one complete secret in a text: its kind and where its value
// lies, so a caller can tell one value from another without ever saying it.
type FoundSecret struct {
	Kind       SecretKind
	Start, End int
}

// completeShape is a shape the scrubbing removes (secretShapes), with what a
// match of it must also be to count as a whole secret rather than text that
// merely looks like the start of one.
type completeShape struct {
	shape    secretShape
	kind     func(value string) SecretKind
	complete func(value string) bool
}

// Minimum lengths a whole value is held to where the scrubbing's own shape
// accepts less. The scrubbing errs towards removing too much, since what it
// removes is only output; a finding that names an item as holding a key is
// something a person acts on, so only a value as long as a real one counts.
// Every key Anthropic and OpenAI issue runs to more than forty characters after
// "sk-", and a bearer token shorter than twenty is a word in a sentence.
const (
	providerKeyBody = 40
	headerTokenBody = 20
)

// completeShapes are checked in this order, and a match overlapping one already
// found is not counted again: a header carrying a JSON web token is the token,
// found once under its own kind.
var completeShapes = []completeShape{
	{
		shape: providerKeyShape,
		kind: func(value string) SecretKind {
			if strings.HasPrefix(value, "sk-ant-") {
				return SecretAnthropicKey
			}
			return SecretOpenAIKey
		},
		complete: func(value string) bool {
			return len(value)-len("sk-") >= providerKeyBody && hasLetterAndDigit(value[len("sk-"):])
		},
	},
	{shape: webTokenShape, kind: fixedKind(SecretWebToken), complete: always},
	{shape: gitHubTokenShape, kind: fixedKind(SecretGitHubToken), complete: always},
	{shape: credentialHeaderShape, kind: fixedKind(SecretHeaderValue), complete: wholeHeaderToken},
	{shape: bearerShape, kind: fixedKind(SecretBearerToken), complete: wholeHeaderToken},
}

// FindCompleteSecrets returns every whole secret in text of a kind the shared
// scrubbing recognizes by its shape: an Anthropic or OpenAI key of full length,
// a JSON web token of three parts, a GitHub token, or a bearer token or
// authorization header followed by a full token. A prefix on its own — "sk-ant-",
// "eyJ", "Bearer" — or a short fragment after one is never found, so text that
// talks about these secrets is not mistaken for text that holds one. A named
// field such as "password: …" is not looked for: the scrubbing removes it from
// output, but a value of eight characters after a word is too common in prose
// to say a credential was published.
//
// The matches are in the order they appear in text.
func FindCompleteSecrets(text string) []FoundSecret {
	var found []FoundSecret
	for _, complete := range completeShapes {
		for _, match := range complete.shape.find(text) {
			// Group one is what the scrubbing keeps in front of the marker: the
			// character before a key, or a header's name and scheme.
			start, end := match[3], match[1]
			value := text[start:end]
			if value == redactedMarker || !complete.complete(value) || overlaps(found, start, end) {
				continue
			}
			found = append(found, FoundSecret{Kind: complete.kind(value), Start: start, End: end})
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Start < found[j].Start })
	return found
}

func fixedKind(kind SecretKind) func(string) SecretKind {
	return func(string) SecretKind { return kind }
}

func always(string) bool { return true }

// wholeHeaderToken reports a header's or bearer's value long enough to be a
// token, mixing letters and digits as every issued token does, so a scheme
// followed by an ordinary word is not one.
func wholeHeaderToken(value string) bool {
	return len(value) >= headerTokenBody && hasLetterAndDigit(value)
}

func hasLetterAndDigit(value string) bool {
	letter, digit := false, false
	for _, r := range value {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	return letter && digit
}

func overlaps(found []FoundSecret, start, end int) bool {
	for _, earlier := range found {
		if start < earlier.End && earlier.Start < end {
			return true
		}
	}
	return false
}
