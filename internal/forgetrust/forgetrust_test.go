package forgetrust

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestProbeReadsAnAnswerAboutTheCertificateAsAbleToVerify(t *testing.T) {
	for name, err := range map[string]error{
		"trusted":           nil,
		"unknown authority": x509.UnknownAuthorityError{},
		"expired":           x509.CertificateInvalidError{Reason: x509.Expired},
		"wrong name":        x509.HostnameError{Host: "yoyo.invalid"},
		"unrecognized":      errors.New("x509: OSStatus -67901"),
	} {
		if got := probe(func() error { return err }); got != nil {
			t.Errorf("%s: probe = %v, want nil", name, got)
		}
	}
}

func TestProbeReportsAVerifierThatCannotBeAsked(t *testing.T) {
	got := probe(func() error { return errors.New("x509: OSStatus -26276") })
	var unverifiable Unverifiable
	if !errors.As(got, &unverifiable) {
		t.Fatalf("probe = %v, want Unverifiable", got)
	}
	for _, want := range []string{"cannot verify any certificate", "x509: OSStatus -26276", "sandbox"} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("probe says %q, want it to contain %q", got, want)
		}
	}
}

func TestFailureRecognizesGhsCertificateErrors(t *testing.T) {
	for _, text := range []string{
		`list open pull requests failed with exit code 1: Post "https://api.github.com/graphql": tls: failed to verify certificate: x509: OSStatus -26276`,
		"x509: certificate signed by unknown authority",
	} {
		if !Failure(fmt.Errorf("gh: %s", text)) {
			t.Errorf("Failure(%q) = false, want true", text)
		}
	}
	for _, err := range []error{nil, errors.New("no pull requests found for branch \"x\""), errors.New("HTTP 502")} {
		if Failure(err) {
			t.Errorf("Failure(%v) = true, want false", err)
		}
	}
}
