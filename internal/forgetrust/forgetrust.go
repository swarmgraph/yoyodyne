// Package forgetrust says whether this process can verify the forge's
// certificate, and recognizes a forge command that failed because it could
// not.
//
// On macOS, the forge's command-line client (gh, a Go program) and every other
// Go program verify a certificate by asking the operating system's trust
// service. A process started inside a macOS sandbox that does not let it reach
// that service -- an AI coding session's shell is one -- cannot ask, and every
// verification fails with "x509: OSStatus -26276", whatever the certificate.
// The sandbox is inherited by everything the process starts and cannot be left,
// so a supervisor started from such a shell hands it to the scheduler, the
// dashboard, and every gh command they run: GitHub is unreadable from all of
// them while the same commands work at an ordinary terminal. The same sandbox
// also hides the machine's user directory, which is where a push's
// "No user exists for uid 501" comes from.
//
// Nothing in the environment fixes it: Go on macOS ignores SSL_CERT_FILE and
// SSL_CERT_DIR and always asks the trust service. The only fix is to start the
// product from outside the sandbox, which is what Probe lets `yoyo start` and
// the supervisor say.
package forgetrust

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"strings"
	"time"
)

// Probe reports whether this process can verify a certificate at all. It
// verifies a certificate it makes for the purpose against the system's roots.
// The trust service's answer about it, trusted or not, is a process that can
// verify. The trust service failing to answer at all, which macOS reports as
// OSStatus -26276, is a process that cannot, and the error is what the
// verifier said. Any other failure is not taken for that: refusing to start a
// product over an answer this does not recognize would cost more than the
// check saves. It reaches no network.
//
// It probes only on macOS, the one platform where verification is the
// operating system's service a sandbox can refuse; elsewhere it returns nil.
func Probe() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	return probe(verifySelfMade)
}

// probe is Probe with the verification supplied, so the reading of its answer
// is tested on every platform.
func probe(verify func() error) error {
	err := verify()
	if err == nil || Answered(err) || !strings.Contains(err.Error(), unreachableTrustService) {
		return nil
	}
	return Unverifiable{Err: err}
}

// unreachableTrustService is what Go's verifier says on macOS when the
// operating system's trust service could not be asked.
const unreachableTrustService = "OSStatus -26276"

// Answered reports whether a verification error is the verifier's answer about
// the certificate -- unknown authority, expired, wrong name -- rather than a
// verifier that could not be asked.
func Answered(err error) bool {
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	var hostname x509.HostnameError
	return errors.As(err, &unknown) || errors.As(err, &invalid) || errors.As(err, &hostname)
}

// Unverifiable is a process that cannot verify any certificate.
type Unverifiable struct {
	Err error
}

func (u Unverifiable) Error() string {
	return fmt.Sprintf("this process cannot verify any certificate (%v), so it cannot read GitHub; "+
		"on macOS this is what a process started inside a sandbox, such as an AI session's shell, is given, "+
		"and every program it starts inherits it", u.Err)
}

func (u Unverifiable) Unwrap() error { return u.Err }

func verifySelfMade() error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "yoyo certificate self-check"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"yoyo.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		return err
	}
	_, err = certificate.Verify(x509.VerifyOptions{DNSName: "yoyo.invalid"})
	return err
}

// certificateFailureMarks are what a Go client, gh among them, prints when it
// could not verify the server's certificate.
var certificateFailureMarks = []string{
	"tls: failed to verify certificate",
	"x509: osstatus",
	"x509: certificate signed by unknown authority",
	"certificate verify failed",
}

// Failure reports whether a forge command's error says it could not verify the
// forge's certificate. Such a failure says nothing about the pull request it was
// asking about: it is never an answer of "not merged" or "no such request".
func Failure(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, mark := range certificateFailureMarks {
		if strings.Contains(text, mark) {
			return true
		}
	}
	return false
}
