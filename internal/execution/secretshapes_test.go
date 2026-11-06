package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The sample secrets below are made up, in the formats each provider issues,
// and every one is put together from pieces while the test runs: a sample
// written out whole is one the forge's secret scanner refuses a push over, so
// no file here may carry one.

// sampleKey is a made-up key: the prefix a provider gives one, then n letters
// from fill.
func sampleKey(prefix, fill string, n int) string {
	return prefix + strings.Repeat(fill, n/len(fill)+1)[:n]
}

// sampleJWT is a made-up JSON web token: a header and a payload that are each
// base64url JSON, and a signature.
func sampleJWT(payload string) string {
	return strings.Join([]string{"ey" + "JhbGciOiJSUzI1NiJ9", "ey" + "J" + payload, sampleKey("", "c2lnbmF0dXJl", 20)}, ".")
}

var (
	anthropicAPIKey     = sampleKey("sk-"+"ant-"+"api03-", "Q2xhdWRlS2V5", 60)
	anthropicInvalidKey = sampleKey("sk-"+"ant-"+"api03-", "SW52YWxpZA", 48)
	anthropicAccess     = sampleKey("sk-"+"ant-"+"oat01-", "T2F1dGhB", 48)
	anthropicRefresh    = sampleKey("sk-"+"ant-"+"ort01-", "UmVmcmVzaA", 48)
	anthropicSession    = sampleKey("sk-"+"ant-"+"sid01-", "U2Vzc2lvbg", 48)
	openAIProjectKey    = sampleKey("sk-"+"proj-", "T3BlbkFJ", 48)
	openAIKey           = sampleKey("sk-", "QWJjRGVm", 48)
	gitHubToken         = sampleKey("gh"+"p_", "R2l0SHVi", 36)
	gitHubFineGrained   = sampleKey("github"+"_pat_", "R2l0SHVi", 60)
	chatGPTAccessToken  = sampleJWT("zdWIiOiJ1c2VyLWV4YW1wbGUifQ")
	chatGPTIDToken      = sampleJWT("lbWFpbCI6ImV4YW1wbGUifQ")
	codexRefreshToken   = sampleKey("rt"+"_", "UmVmcmVzaA", 24)
	headerKey           = sampleKey("Example", "KeyNotGiven", 30)
	gatewayToken        = sampleKey("gateway-", "0a1b2c3d", 24)
	callbackToken       = sampleKey("", "QWNjZXNz", 24)
	accountID           = "6f1c0b3e-" + "9a2d-4e5f-8a7b-" + "1c2d3e4f5a6b"
	proxyCredentials    = "dXNlcjpl" + "eGFtcGxlLXBhc3N3b3Jk"
)

// providerFailureOutput is what Claude Code and Codex print when they fail,
// sign-in and authentication failures first, each line carrying a secret the
// harness never held: none of these values is in its environment, so only the
// shapes can remove them.
func providerFailureOutput() []struct {
	name   string
	line   string
	secret string
	// kept is what a reader still needs from the line and has to survive.
	kept string
} {
	return []struct {
		name   string
		line   string
		secret string
		kept   string
	}{
		// Claude Code.
		{
			name:   "claude: API key in an environment echo",
			line:   "ANTHROPIC_API_KEY=" + anthropicAPIKey,
			secret: anthropicAPIKey,
			kept:   "ANTHROPIC_API_KEY=",
		},
		{
			name:   "claude: invalid API key echoed in an authentication error",
			line:   `API Error: 401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key: ` + anthropicInvalidKey + `"},"request_id":"req_011CUa1b2c3d4e5f"}`,
			secret: anthropicInvalidKey,
			kept:   "authentication_error",
		},
		{
			name:   "claude: request headers dumped by the HTTP client",
			line:   `headers: { 'x-api-key': '` + headerKey + `', 'anthropic-version': '2023-06-01' }`,
			secret: headerKey,
			kept:   "anthropic-version",
		},
		{
			name:   "claude: OAuth access token in an authorization header",
			line:   `[DEBUG] POST https://api.anthropic.com/v1/messages "Authorization": "Bearer ` + anthropicAccess + `"`,
			secret: anthropicAccess,
			kept:   "Bearer",
		},
		{
			name:   "claude: OAuth refresh failing with the refresh token",
			line:   `OAuth token refresh failed: {"refresh_token":"` + anthropicRefresh + `","error":"invalid_grant"} · Please run /login`,
			secret: anthropicRefresh,
			kept:   "Please run /login",
		},
		{
			name:   "claude: session cookie",
			line:   "Cookie: sessionKey=" + anthropicSession + "; lastActiveOrg=0f1e2d3c",
			secret: anthropicSession,
			kept:   "Cookie:",
		},
		{
			name:   "claude: auth token in the environment-style setting",
			line:   "Invalid bearer token: ANTHROPIC_AUTH_TOKEN=" + gatewayToken,
			secret: gatewayToken,
			kept:   "Invalid bearer token",
		},
		// Codex.
		{
			name:   "codex: OpenAI project key",
			line:   "ERROR: unexpected status 401 Unauthorized: Incorrect API key provided: " + openAIProjectKey,
			secret: openAIProjectKey,
			kept:   "401 Unauthorized",
		},
		{
			name:   "codex: OpenAI key in an environment echo",
			line:   "OPENAI_API_KEY=" + openAIKey,
			secret: openAIKey,
			kept:   "OPENAI_API_KEY=",
		},
		{
			name:   "codex: ChatGPT access token as a bearer header",
			line:   "2026-11-05T15:04:05.000Z TRACE codex_core::client: request headers: authorization: Bearer " + chatGPTAccessToken,
			secret: chatGPTAccessToken,
			kept:   "request headers",
		},
		{
			name:   "codex: ChatGPT account id header",
			line:   `request headers: {"chatgpt-account-id": "` + accountID + `", "originator": "codex_cli_rs"}`,
			secret: accountID,
			kept:   "codex_cli_rs",
		},
		{
			name:   "codex: login tokens from its auth file",
			line:   `failed to refresh token: {"tokens":{"id_token":"` + chatGPTIDToken + `","refresh_token":"` + codexRefreshToken + `"}}`,
			secret: chatGPTIDToken,
			kept:   "failed to refresh token",
		},
		{
			name:   "codex: refresh token that is not a JWT",
			line:   `failed to refresh token: {"tokens":{"refresh_token":"` + codexRefreshToken + `"}}`,
			secret: codexRefreshToken,
			kept:   "failed to refresh token",
		},
		{
			name:   "codex: OAuth callback carrying a token in its query",
			line:   "Error logging in: callback http://localhost:1455/auth/callback?access_token=" + callbackToken + "&state=abc",
			secret: callbackToken,
			kept:   "Error logging in",
		},
		{
			name:   "codex: basic credentials for a proxy",
			line:   "stream error: Proxy-Authorization: Basic " + proxyCredentials,
			secret: proxyCredentials,
			kept:   "Proxy-Authorization: Basic",
		},
		// Either: a session's own tools.
		{
			name:   "a GitHub token a tool printed",
			line:   "fatal: Authentication failed for 'https://" + gitHubToken + "@github.com/example/repo.git/'",
			secret: gitHubToken,
			kept:   "Authentication failed",
		},
		{
			name:   "a fine-grained GitHub token a tool printed",
			line:   "gh: HTTP 401: Bad credentials, using " + gitHubFineGrained,
			secret: gitHubFineGrained,
			kept:   "Bad credentials",
		},
	}
}

func TestRedactorRemovesSecretsProvidersPrintWhenTheyFail(t *testing.T) {
	t.Parallel()

	for _, test := range providerFailureOutput() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := NewRedactor().WithSecretShapes().Redact(test.line)
			if strings.Contains(got, test.secret) {
				t.Fatalf("the secret survived redaction:\n%s", got)
			}
			if !strings.Contains(got, redactedMarker) {
				t.Fatalf("nothing says a secret was removed:\n%s", got)
			}
			if !strings.Contains(got, test.kept) {
				t.Fatalf("redaction removed %q, which a reader needs:\n%s", test.kept, got)
			}
		})
	}
}

// What providers print when they fail without a secret in it is what a reader
// of the failure needs, so redaction leaves it exactly as it was.
func TestRedactorLeavesOrdinaryFailureOutputAlone(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"Invalid API key · Please run /login",
		"OAuth token has expired. Please obtain a new token or refresh your existing token.",
		"No conversation found with session ID: 01a106a4-5b6c-4d7e-8f90-a1b2c3d4e5f6",
		"Your access token could not be refreshed because your refresh token was already used. Please log out and sign in again.",
		`ERROR: unexpected status 401 Unauthorized: {"error":{"message":"Incorrect API key provided: sk-proj-********************abcd.","code":"invalid_api_key"}}`,
		`{"type":"result","session_id":"8198e890-0145-412b-a89a-d77e30f778d1","usage":{"input_tokens":1234,"output_tokens":56},"apiKeySource":"none"}`,
		"stream disconnected before completion: error sending request for url (https://chatgpt.com/backend-api/codex/responses)",
		`"api_key": null, "token": "short"`,
		"merge commit 412243ec3f0a9b1c2d3e4f5a6b7c8d9e0f1a2b3c",
	} {
		if got := NewRedactor().WithSecretShapes().Redact(line); got != line {
			t.Errorf("Redact() changed output with no secret in it:\n got %s\nwant %s", got, line)
		}
	}
}

// A value the redactor was given and a secret found by its shape are both
// removed from one line, and a value already replaced is not replaced again.
func TestRedactorRemovesGivenValuesAndShapesTogether(t *testing.T) {
	t.Parallel()

	got := NewRedactor("given-secret-value").WithSecretShapes().Redact("given-secret-value Authorization: Bearer given-secret-value " + anthropicAPIKey)
	want := "[REDACTED] Authorization: Bearer [REDACTED] [REDACTED]"
	if got != want {
		t.Fatalf("Redact() = %q, want %q", got, want)
	}
}

// The process runner applies the shapes to what a process writes to standard
// error, which is where the last lines on a failed run's tracker notes are taken
// from, with no secret value given to it at all.
func TestProcessRunnerRedactsSecretsAProviderPrintsToStandardError(t *testing.T) {
	t.Parallel()

	output := providerFailureOutput()
	var lines []string
	for _, test := range output {
		lines = append(lines, test.line)
	}
	command := helperCommand("stderr-lines", strings.Join(lines, "\n"))
	command.Redactor = NewRedactor().WithSecretShapes()
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil || result.Status != ProcessFailed {
		t.Fatalf("Run = %#v, %v", result, err)
	}
	for _, test := range output {
		if strings.Contains(result.Stderr, test.secret) {
			t.Errorf("%s: the secret reached the process result's standard error:\n%s", test.name, result.Stderr)
		}
	}
	if strings.Count(result.Stderr, redactedMarker) < len(output) {
		t.Fatalf("standard error does not carry every line, redacted:\n%s", result.Stderr)
	}
}

// A Redactor is applied to every line of a provider's JSON output, not only to
// standard error, and one line that is no longer JSON fails the whole provider
// call. So text a session reads that holds a credential-looking value right
// before a quote, which arrives with that quote escaped, is redacted without
// taking the escape with it, and a line carrying a provider's failure output is
// still JSON once the secret in it is gone.
func TestRedactorKeepsAProviderStreamLineValidJSON(t *testing.T) {
	t.Parallel()

	streamLine := func(t *testing.T, text string) string {
		t.Helper()
		line, err := json.Marshal(map[string]any{
			"type":    "user",
			"message": map[string]any{"content": []map[string]any{{"type": "tool_result", "content": text}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(line)
	}

	for _, text := range []string{
		`env := []string{"GH_TOKEN=fixture-token"}`,
		`curl -H "Authorization: Bearer abcdefgh12" https://api.github.com/user`,
		`curl -H "Authorization: Bearer $TOKEN" -H "x-api-key: $ANTHROPIC_API_KEY"`,
		`{"api_key":"abcdefgh12345678","password":"hunter2hunter2"}`,
		"OPENAI_API_KEY=\"abcdefgh12345678\"\tnext\u0026more",
	} {
		line := streamLine(t, text)
		got := NewRedactor().WithSecretShapes().Redact(line)
		if !json.Valid([]byte(got)) {
			t.Errorf("redaction left a stream line that is not JSON:\n before %s\n  after %s", line, got)
		}
	}

	for _, test := range providerFailureOutput() {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := NewRedactor().WithSecretShapes().Redact(streamLine(t, test.line))
			if !json.Valid([]byte(got)) {
				t.Fatalf("redaction left a stream line that is not JSON:\n%s", got)
			}
			var decoded struct {
				Message struct {
					Content []struct {
						Content string `json:"content"`
					} `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal([]byte(got), &decoded); err != nil {
				t.Fatal(err)
			}
			if text := decoded.Message.Content[0].Content; strings.Contains(text, test.secret) {
				t.Fatalf("the secret survived redaction of the escaped line:\n%s", text)
			}
		})
	}
}

// A redactor that did not ask for the shapes, such as the empty one Git and the
// tracker run with, leaves secret-shaped text exactly as it was: what they print
// is data the harness reads back, and a rewritten file, diff, or note is no
// longer what was stored.
func TestRedactorWithoutShapesLeavesSecretShapedTextAlone(t *testing.T) {
	t.Parallel()

	output := providerFailureOutput()
	for _, test := range output {
		for _, redactor := range []Redactor{{}, NewRedactor(), NewRedactor("a-value-not-in-the-line")} {
			if got := redactor.Redact(test.line); got != test.line {
				t.Errorf("%s: a redactor without shapes changed the line:\n got %s\nwant %s", test.name, got, test.line)
			}
		}
	}

	var lines []string
	for _, test := range output {
		lines = append(lines, test.line)
	}
	want := strings.Join(lines, "\n") + "\n"
	command := helperCommand("stderr-lines", strings.Join(lines, "\n"))
	command.Redactor = NewRedactor()
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil || result.Status != ProcessFailed {
		t.Fatalf("Run = %#v, %v", result, err)
	}
	if result.Stderr != want {
		t.Fatalf("a process run with an empty redactor did not return its output byte for byte:\n got %q\nwant %q", result.Stderr, want)
	}
}
