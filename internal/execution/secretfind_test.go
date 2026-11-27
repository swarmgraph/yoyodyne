package execution

import (
	"strings"
	"testing"
)

// The sample secrets come from secretshapes_test.go, each put together from
// pieces while the test runs.

func TestFindCompleteSecretsFindsEveryWholeSecret(t *testing.T) {
	bearer := sampleKey("", "QmVhcmVy4Tz9", 40)
	// A legacy OpenAI key, with the digits every issued one carries.
	openAILegacy := sampleKey("sk-", "QWJj7RGVm2", 48)
	cases := []struct {
		name  string
		text  string
		kind  SecretKind
		value string
	}{
		{"Anthropic API key", "error: invalid x-api-key " + anthropicAPIKey + " (401)", SecretAnthropicKey, anthropicAPIKey},
		{"Anthropic access token", "token " + anthropicAccess + " expired", SecretAnthropicKey, anthropicAccess},
		{"OpenAI project key", "OPENAI key: " + openAIProjectKey, SecretOpenAIKey, openAIProjectKey},
		{"OpenAI key", "using " + openAILegacy + ".", SecretOpenAIKey, openAILegacy},
		{"JSON web token", "id_token=" + chatGPTIDToken, SecretWebToken, chatGPTIDToken},
		{"GitHub token", "push with " + gitHubToken, SecretGitHubToken, gitHubToken},
		{"GitHub fine-grained token", "GH " + gitHubFineGrained, SecretGitHubToken, gitHubFineGrained},
		{"authorization header", "Authorization: Basic " + bearer, SecretHeaderValue, bearer},
		{"bearer token", "sent Bearer " + bearer + " to the API", SecretBearerToken, bearer},
		{"after an escaped line break", `stderr:\n` + anthropicAPIKey, SecretAnthropicKey, anthropicAPIKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := FindCompleteSecrets(tc.text)
			if len(found) != 1 {
				t.Fatalf("found %d secrets in %q, want 1", len(found), tc.text)
			}
			if found[0].Kind != tc.kind {
				t.Fatalf("kind = %q, want %q", found[0].Kind, tc.kind)
			}
			if got := tc.text[found[0].Start:found[0].End]; got != tc.value {
				t.Fatalf("found value at the wrong place: %d characters, want %d", len(got), len(tc.value))
			}
		})
	}
}

func TestFindCompleteSecretsCountsAHeaderCarryingATokenOnce(t *testing.T) {
	text := "Authorization: Bearer " + chatGPTAccessToken
	found := FindCompleteSecrets(text)
	if len(found) != 1 || found[0].Kind != SecretWebToken {
		t.Fatalf("found %+v, want the one JSON web token", found)
	}
}

func TestFindCompleteSecretsLeavesPrefixesAndFragmentsAlone(t *testing.T) {
	texts := []string{
		"search the notes for " + "sk-" + "ant-" + ", " + "sk-" + "proj-" + ", " + "ey" + "J" + " or " + "Bear" + "er",
		"sk-" + "ant-",
		"sk-" + "proj-",
		"ey" + "J",
		"Bear" + "er",
		"Bear" + "er token",
		"a " + "Bear" + "er authorization header",
		"Authorization: " + "Bear" + "er",
		"Authorization: " + "Bear" + "er " + RedactedMarker,
		"the key began " + "sk-" + "ant-" + "api03-" + "abc",
		"a short fragment '" + anthropicAPIKey[:30] + "'",
		"ey" + "JhbGciOi" + " is how a token's header begins",
		"gh" + "p_" + "short",
		"password: correct-horse-battery-staple-99",
		"sk-" + "ant-api-keys-should-never-appear-in-a-note-at-all",
	}
	for _, text := range texts {
		if found := FindCompleteSecrets(text); len(found) != 0 {
			t.Errorf("found %+v in %q, want nothing", found, text)
		}
	}
}

func TestFindCompleteSecretsReturnsMatchesInOrder(t *testing.T) {
	text := strings.Join([]string{gitHubToken, anthropicAPIKey, chatGPTIDToken}, " and ")
	found := FindCompleteSecrets(text)
	want := []SecretKind{SecretGitHubToken, SecretAnthropicKey, SecretWebToken}
	if len(found) != len(want) {
		t.Fatalf("found %d, want %d", len(found), len(want))
	}
	for index, kind := range want {
		if found[index].Kind != kind {
			t.Errorf("match %d is %q, want %q", index, found[index].Kind, kind)
		}
	}
}
