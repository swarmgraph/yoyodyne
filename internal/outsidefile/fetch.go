package outsidefile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const (
	// DefaultMaxBytes is the size limit where a project sets none.
	DefaultMaxBytes int64 = 5 << 20
	// CeilingBytes is the size limit whatever a project sets.
	CeilingBytes int64 = 50 << 20
	// MaxRedirects is how many redirects one fetch follows.
	MaxRedirects = 5
	// RetryAfter is the least a record waits after a fetch the network stopped
	// before it is tried again.
	RetryAfter = 10 * time.Minute
	// fetchTimeout bounds one fetch as a whole.
	fetchTimeout = 5 * time.Minute
)

// Policy is what a project's configuration says about outside files: the hosts
// it allows and its size limit. It narrows the capability and never widens it:
// every rule in CheckAddress and Fetch holds whatever it says.
type Policy struct {
	// Hosts are the hosts a file may come from, each a host name, or a host
	// name and port where the address names one. With none, nothing is fetched.
	Hosts []string
	// MaxBytes is the project's size limit; zero is DefaultMaxBytes.
	MaxBytes int64
}

// Limit is the size limit in force: the project's, or the default, and never
// more than CeilingBytes.
func (p Policy) Limit() int64 {
	limit := p.MaxBytes
	if limit <= 0 {
		limit = DefaultMaxBytes
	}
	return min(limit, CeilingBytes)
}

// Enabled reports whether the policy lets anything be fetched at all.
func (p Policy) Enabled() bool { return len(p.Hosts) > 0 }

func (p Policy) allows(host string) bool {
	return slices.Contains(p.Hosts, strings.ToLower(host))
}

// CheckAddress is every rule an address is held to before any request is sent:
// https, a listed host, no user name, password, query string or fragment, and
// the declared version in its path.
func (p Policy) CheckAddress(recordPath, address, version string) (*url.URL, error) {
	if !p.Enabled() {
		return nil, recordError(recordPath, "the project lists no host under outside_files.hosts, so nothing is fetched")
	}
	parsed, err := p.checkTarget(recordPath, address)
	if err != nil {
		return nil, err
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return nil, recordError(recordPath, "the address %s carries a query string", address)
	}
	if parsed.Fragment != "" || strings.Contains(address, "#") {
		return nil, recordError(recordPath, "the address %s carries a fragment", address)
	}
	if version == "" || !strings.Contains(parsed.EscapedPath(), version) {
		return nil, recordError(recordPath, "the address %s does not carry the declared version %s in its path", address, version)
	}
	return parsed, nil
}

// checkTarget is the part of CheckAddress a redirect is held to as well.
func (p Policy) checkTarget(recordPath, address string) (*url.URL, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, recordError(recordPath, "the address %q cannot be read: %v", address, err)
	}
	if parsed.Scheme != "https" {
		return nil, recordError(recordPath, "the address %s does not use https", address)
	}
	if parsed.User != nil {
		return nil, recordError(recordPath, "the address %s carries a user name or password", address)
	}
	if parsed.Host == "" {
		return nil, recordError(recordPath, "the address %s names no host", address)
	}
	if !p.allows(parsed.Host) {
		return nil, recordError(recordPath, "the host %s is not listed under outside_files.hosts", parsed.Host)
	}
	return parsed, nil
}

// NetworkError is a fetch the network stopped: a connection that failed, a
// timeout, or a server error. It is waited out and counts for nothing.
type NetworkError struct {
	Address string
	Cause   error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("fetching %s did not finish: %v", e.Address, e.Cause)
}

func (e *NetworkError) Unwrap() error { return e.Cause }

// Fetched is what one fetch brought in.
type Fetched struct {
	Content []byte
	// FinalURL is the address the file finally came from, after any redirects.
	FinalURL string
	SHA256   string
	Size     int64
	// Given reports that the record supplied the checksum the file matched;
	// false is a file trusted on its first fetch.
	Given bool
	At    time.Time
}

// Fetcher fetches a record's file in the harness's own process.
type Fetcher struct {
	// Transport carries the requests. Nil is a fresh transport of the default
	// kind; tests supply one that trusts their server.
	Transport http.RoundTripper
	Now       func() time.Time
}

// Fetch fetches the file a record declares, under every rule of the policy,
// and checks it against the record's checksum where it gives one. It never
// unpacks, runs, or interprets what it fetched.
func (f Fetcher) Fetch(ctx context.Context, recordPath string, record Record, policy Policy) (Fetched, error) {
	address, err := policy.CheckAddress(recordPath, record.URL, record.Version)
	if err != nil {
		return Fetched{}, err
	}
	transport := f.Transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport).Clone()
	}
	var refusedRedirect error
	client := &http.Client{
		Transport: transport,
		// No cookie jar, so nothing set by one response is sent with the next.
		Jar: nil,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) > MaxRedirects {
				refusedRedirect = recordError(recordPath, "the address %s redirected more than %d times", record.URL, MaxRedirects)
				return refusedRedirect
			}
			if _, err := policy.checkTarget(recordPath, request.URL.String()); err != nil {
				refusedRedirect = recordError(recordPath, "the address %s redirected to %s, which is refused: %s", record.URL, request.URL.Redacted(), err.(*RecordError).Reason)
				return refusedRedirect
			}
			// The request carries no credentials to begin with; this keeps any a
			// transport might add from following a redirect.
			request.Header.Del("Authorization")
			request.Header.Del("Cookie")
			return nil
		},
	}
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address.String(), nil)
	if err != nil {
		return Fetched{}, recordError(recordPath, "the address %s cannot be requested: %v", record.URL, err)
	}
	request.Header.Set("User-Agent", "yoyodyne-outside-files")
	response, err := client.Do(request)
	if refusedRedirect != nil {
		if response != nil {
			response.Body.Close()
		}
		return Fetched{}, refusedRedirect
	}
	if err != nil {
		return Fetched{}, &NetworkError{Address: record.URL, Cause: err}
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusRequestTimeout:
		return Fetched{}, &NetworkError{Address: record.URL, Cause: fmt.Errorf("the server answered %s", response.Status)}
	case response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone:
		return Fetched{}, recordError(recordPath, "there is no file at %s (the server answered %s)", record.URL, response.Status)
	case response.StatusCode != http.StatusOK:
		return Fetched{}, recordError(recordPath, "the server answered %s for %s", response.Status, record.URL)
	}
	limit := policy.Limit()
	if response.ContentLength > limit {
		return Fetched{}, recordError(recordPath, "the file at %s is %d bytes, over the %d-byte limit", record.URL, response.ContentLength, limit)
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return Fetched{}, &NetworkError{Address: record.URL, Cause: err}
	}
	if int64(len(content)) > limit {
		return Fetched{}, recordError(recordPath, "the file at %s is over the %d-byte limit", record.URL, limit)
	}
	if len(content) == 0 {
		return Fetched{}, recordError(recordPath, "the file at %s is empty", record.URL)
	}
	sum := Checksum(content)
	if record.SHA256 != "" && record.SHA256 != sum {
		return Fetched{}, recordError(recordPath, "the file at %s has checksum %s, and the record gives %s", record.URL, sum, record.SHA256)
	}
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	return Fetched{
		Content:  content,
		FinalURL: response.Request.URL.String(),
		SHA256:   sum,
		Size:     int64(len(content)),
		Given:    record.SHA256 != "",
		At:       now().UTC().Truncate(time.Second),
	}, nil
}

// Checksum is the hexadecimal SHA-256 of content, as a record carries it.
func Checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}
