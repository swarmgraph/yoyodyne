package config

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/usage"
)

// How an account is paid for is stated per account and read back by alias; an
// account that does not say is not recorded rather than guessed at.
func TestAnAccountSaysHowItIsBilled(t *testing.T) {
	t.Parallel()

	cfg := mustDecodeConfig(t, accountConfig(`accounts:
  work:
    billing: api_key
  personal:
    billing: subscription
  spare: {}
`))
	for alias, want := range map[string]usage.BillingMode{
		"work":     usage.BillingAPIKey,
		"personal": usage.BillingSubscription,
		"spare":    usage.BillingNotRecorded,
		"nobody":   usage.BillingNotRecorded,
	} {
		if got := cfg.AccountBilling(alias); got != want {
			t.Fatalf("AccountBilling(%q) = %q, want %q", alias, got, want)
		}
	}
}

func TestABillingModeNobodyKnowsIsRefused(t *testing.T) {
	t.Parallel()

	_, err := DecodeResolved(strings.NewReader(accountConfig(`accounts:
  work:
    billing: monthly
`)))
	if err == nil || !strings.Contains(err.Error(), "accounts.work.billing") {
		t.Fatalf("err = %v", err)
	}
}
