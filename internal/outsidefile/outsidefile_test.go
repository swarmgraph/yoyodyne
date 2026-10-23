package outsidefile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const recordPath = "internal/dashboard/assets/chart.umd.js.yoyodyne-source.yaml"

func declared() Record {
	return Record{File: "chart.umd.js", URL: "https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js", Version: "4.4.1", License: "MIT", LicenseFile: "chart.js-LICENSE.md", For: "yoyodyne-ifd.432.33"}
}

func TestParseRecordReadsADeclarationAndRefusesWhatIsNotOne(t *testing.T) {
	content := "# Chart.js for the dashboard\nfile: chart.umd.js\nurl: https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js\nversion: 4.4.1\nlicense: MIT\nlicense_file: chart.js-LICENSE.md\nfor: yoyodyne-ifd.432.33\n"
	record, err := ParseRecord(recordPath, []byte(content))
	if err != nil {
		t.Fatalf("ParseRecord() error = %v", err)
	}
	if record != declared() {
		t.Fatalf("ParseRecord() = %+v, want %+v", record, declared())
	}
	for name, test := range map[string]struct{ path, content, says string }{
		"unknown field":         {recordPath, content + "extra: 1\n", "cannot be read"},
		"file for another name": {recordPath, strings.Replace(content, "file: chart.umd.js", "file: other.js", 1), `declares "chart.umd.js"`},
		"no version":            {recordPath, strings.Replace(content, "version: 4.4.1\n", "", 1), "not one exact version"},
		"license not spdx":      {recordPath, strings.Replace(content, "license: MIT", "license: the MIT one", 1), "not an SPDX identifier"},
		"license elsewhere":     {recordPath, strings.Replace(content, "license_file: chart.js-LICENSE.md", "license_file: ../LICENSE", 1), "not a file in the record's directory"},
		"for no item":           {recordPath, strings.Replace(content, "for: yoyodyne-ifd.432.33", "for: soon", 1), "not a work item id"},
		"bad checksum":          {recordPath, content + "sha256: abc\n", "not 64 lowercase"},
		"half the fields":       {recordPath, content + "size: 10\n", "written together or not at all"},
		"not a record name":     {"internal/dashboard/assets/chart.umd.js", content, "ends in"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRecord(test.path, []byte(test.content))
			if err == nil || !IsRecordError(err) || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("ParseRecord() error = %v, want a record error saying %q", err, test.says)
			}
		})
	}
}

func TestCheckAddressRefusesEveryAddressTheRulesRuleOutBeforeAnyRequest(t *testing.T) {
	policy := Policy{Hosts: []string{"cdn.jsdelivr.net"}}
	if _, err := policy.CheckAddress(recordPath, declared().URL, "4.4.1"); err != nil {
		t.Fatalf("CheckAddress(listed https address) error = %v", err)
	}
	for name, test := range map[string]struct {
		policy  Policy
		address string
		says    string
	}{
		"empty host list":  {Policy{}, declared().URL, "lists no host"},
		"host not listed":  {policy, "https://unpkg.com/chart.js@4.4.1/dist/chart.umd.js", "not listed"},
		"plain http":       {policy, "http://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js", "does not use https"},
		"query string":     {policy, declared().URL + "?x=1", "query string"},
		"empty query":      {policy, declared().URL + "?", "query string"},
		"fragment":         {policy, declared().URL + "#top", "fragment"},
		"user name":        {policy, "https://someone@cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js", "user name or password"},
		"version not path": {policy, "https://cdn.jsdelivr.net/npm/chart.js@latest/dist/chart.umd.js", "does not carry the declared version"},
		"other port":       {policy, "https://cdn.jsdelivr.net:8443/npm/chart.js@4.4.1/dist/chart.umd.js", "not listed"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := test.policy.CheckAddress(recordPath, test.address, "4.4.1")
			if err == nil || !IsRecordError(err) || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("CheckAddress(%s) error = %v, want a record error saying %q", test.address, err, test.says)
			}
		})
	}
}

func TestLimitDefaultsAndIsNeverAboveTheCeiling(t *testing.T) {
	for _, test := range []struct{ set, want int64 }{{0, DefaultMaxBytes}, {1 << 20, 1 << 20}, {200 << 20, CeilingBytes}} {
		if got := (Policy{MaxBytes: test.set}).Limit(); got != test.want {
			t.Errorf("Limit() with max_bytes %d = %d, want %d", test.set, got, test.want)
		}
	}
}

// server is a TLS server whose handler the test writes, with a policy listing
// it and a fetcher that trusts it.
func server(t *testing.T, handler http.HandlerFunc) (*httptest.Server, Policy, Fetcher) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	return srv, Policy{Hosts: []string{host}}, Fetcher{Transport: srv.Client().Transport, Now: func() time.Time { return time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC) }}
}

func recordAt(srv *httptest.Server, path string) Record {
	record := declared()
	record.URL = srv.URL + path
	return record
}

func TestFetchBringsInAFileFromAListedHostAndRecordsWhereItCameFrom(t *testing.T) {
	var seen http.Header
	srv, policy, fetcher := server(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/npm/chart.js@4.4.1/dist/chart.umd.js" {
			http.Redirect(w, r, "/final/chart.js@4.4.1/chart.umd.js", http.StatusFound)
			return
		}
		seen = r.Header.Clone()
		_, _ = w.Write([]byte("/*! Chart.js v4.4.1 */"))
	})
	fetched, err := fetcher.Fetch(context.Background(), recordPath, recordAt(srv, "/npm/chart.js@4.4.1/dist/chart.umd.js"), policy)
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if string(fetched.Content) != "/*! Chart.js v4.4.1 */" || fetched.FinalURL != srv.URL+"/final/chart.js@4.4.1/chart.umd.js" || fetched.Size != int64(len(fetched.Content)) || fetched.SHA256 != Checksum(fetched.Content) || fetched.Given {
		t.Fatalf("Fetch() = %+v", fetched)
	}
	for _, header := range []string{"Authorization", "Cookie"} {
		if seen.Get(header) != "" {
			t.Errorf("the request carried %s", header)
		}
	}
	// A checksum the record gives is what the file is held to.
	given := recordAt(srv, "/npm/chart.js@4.4.1/dist/chart.umd.js")
	given.SHA256 = fetched.SHA256
	if again, err := fetcher.Fetch(context.Background(), recordPath, given, policy); err != nil || !again.Given {
		t.Fatalf("Fetch() with the given checksum = %+v, %v", again, err)
	}
}

func TestFetchRefusesWhatIsAboutTheRecordAndWaitsOutTheNetwork(t *testing.T) {
	srv, policy, fetcher := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/big/"):
			_, _ = w.Write(make([]byte, 2048))
		case strings.HasPrefix(r.URL.Path, "/away/"):
			http.Redirect(w, r, "https://elsewhere.example/chart.js@4.4.1/x.js", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/plain/"):
			http.Redirect(w, r, "http://"+r.Host+"/x/chart.js@4.4.1/x.js", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/loop/"):
			http.Redirect(w, r, r.URL.Path+"x", http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/missing/"):
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/down/"):
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = w.Write([]byte("content"))
		}
	})
	small := policy
	small.MaxBytes = 1024
	mismatch := recordAt(srv, "/ok/chart.js@4.4.1/x.js")
	mismatch.SHA256 = strings.Repeat("0", 64)
	for name, test := range map[string]struct {
		record Record
		policy Policy
		says   string
	}{
		"over the size limit":   {recordAt(srv, "/big/chart.js@4.4.1/x.js"), small, "over the 1024-byte limit"},
		"redirect off the list": {recordAt(srv, "/away/chart.js@4.4.1/x.js"), policy, "not listed"},
		"redirect to http":      {recordAt(srv, "/plain/chart.js@4.4.1/x.js"), policy, "does not use https"},
		"too many redirects":    {recordAt(srv, "/loop/chart.js@4.4.1/x.js"), policy, "more than 5 times"},
		"no file there":         {recordAt(srv, "/missing/chart.js@4.4.1/x.js"), policy, "no file at"},
		"checksum mismatch":     {mismatch, policy, "the record gives " + strings.Repeat("0", 64)},
		"empty host list":       {recordAt(srv, "/ok/chart.js@4.4.1/x.js"), Policy{}, "lists no host"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := fetcher.Fetch(context.Background(), recordPath, test.record, test.policy)
			if err == nil || !IsRecordError(err) || !strings.Contains(err.Error(), test.says) {
				t.Fatalf("Fetch() error = %v, want a record error saying %q", err, test.says)
			}
		})
	}
	_, err := fetcher.Fetch(context.Background(), recordPath, recordAt(srv, "/down/chart.js@4.4.1/x.js"), policy)
	var network *NetworkError
	if !errors.As(err, &network) || IsRecordError(err) {
		t.Fatalf("Fetch() of a server error = %v, want a network error", err)
	}
	closed := recordAt(srv, "/ok/chart.js@4.4.1/x.js")
	srv.Close()
	if _, err := fetcher.Fetch(context.Background(), recordPath, closed, policy); !errors.As(err, &network) {
		t.Fatalf("Fetch() from a server that is gone = %v, want a network error", err)
	}
}

func TestFetchSendsNoRequestForARefusedAddress(t *testing.T) {
	requests := 0
	srv, policy, fetcher := server(t, func(w http.ResponseWriter, r *http.Request) { requests++ })
	for _, address := range []string{srv.URL + "/chart.js@4.4.1/x.js?q=1", srv.URL + "/latest/x.js", strings.Replace(srv.URL, "https", "http", 1) + "/chart.js@4.4.1/x.js"} {
		record := declared()
		record.URL = address
		if _, err := fetcher.Fetch(context.Background(), recordPath, record, policy); !IsRecordError(err) {
			t.Fatalf("Fetch(%s) error = %v, want a record error", address, err)
		}
	}
	if requests != 0 {
		t.Fatalf("%d requests were sent for refused addresses", requests)
	}
}

func TestWithHarnessFieldsChangesOnlyTheHarnessFields(t *testing.T) {
	content := "# kept\nfile: chart.umd.js\nurl: https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js\nversion: 4.4.1\nlicense: MIT\nlicense_file: chart.js-LICENSE.md\nfor: yoyodyne-ifd.432.33\n"
	fetched := Fetched{SHA256: strings.Repeat("a", 64), Size: 12, FinalURL: "https://cdn.jsdelivr.net/npm/chart.js@4.4.1/dist/chart.umd.js", At: time.Date(2026, 10, 23, 9, 0, 0, 0, time.UTC)}
	written, err := WithHarnessFields(recordPath, []byte(content), fetched)
	if err != nil {
		t.Fatalf("WithHarnessFields() error = %v", err)
	}
	if !strings.HasPrefix(string(written), "# kept\n") {
		t.Errorf("the declarer's comment was not kept:\n%s", written)
	}
	record, err := ParseRecord(recordPath, written)
	if err != nil {
		t.Fatal(err)
	}
	if record.Declared() != declared() || record.SHA256 != fetched.SHA256 || record.Size != 12 || record.FetchedAt != "2026-10-23T09:00:00Z" {
		t.Fatalf("WithHarnessFields() wrote %+v", record)
	}
	base, _ := ParseRecord(recordPath, []byte(content))
	if problems := landingProblems(recordPath, &base, record); len(problems) != 0 {
		t.Fatalf("the landing's own record was refused: %v", problems)
	}
}

// tree is a Reader over a map of paths.
func tree(files map[string]string) Reader {
	return func(path string, _ int64) ([]byte, bool, error) {
		content, ok := files[path]
		return []byte(content), ok, nil
	}
}

func recordText(record Record) string {
	lines := []string{"file: " + record.File, "url: " + record.URL, "version: " + record.Version, "license: " + record.License, "license_file: " + record.LicenseFile, "for: " + record.For}
	if record.SHA256 != "" {
		lines = append(lines, "sha256: "+record.SHA256)
	}
	if record.Fetched() {
		lines = append(lines, "size: "+strconv.FormatInt(record.Size, 10), "fetched_from: "+record.FetchedFrom, "fetched_at: "+record.FetchedAt)
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestCompareNamesAPinnedFileChangedByHandAndHarnessFieldsWrittenByAnyOtherChange(t *testing.T) {
	pinned := "chart"
	fetched := declared()
	fetched.SHA256, fetched.Size, fetched.FetchedFrom, fetched.FetchedAt = Checksum([]byte(pinned)), 5, fetched.URL, "2026-10-23T09:00:00Z"
	file := PinnedPath(recordPath)
	onMain := map[string]string{recordPath: recordText(fetched), file: pinned}
	compare := func(candidate map[string]string, landing string) []Problem {
		return Compare(Comparison{Records: []string{recordPath}, Candidate: tree(candidate), Base: tree(onMain), Landing: landing})
	}
	if problems := compare(onMain, ""); len(problems) != 0 {
		t.Fatalf("an unchanged pinned file was refused: %v", problems)
	}
	if problems := compare(map[string]string{recordPath: recordText(fetched)}, ""); len(problems) != 0 {
		t.Fatalf("a missing pinned file was refused: %v", problems)
	}
	byHand := compare(map[string]string{recordPath: recordText(fetched), file: "chart, edited"}, "")
	if len(byHand) != 1 || byHand[0].Path != file || !strings.Contains(byHand[0].Says, "never changed by hand") {
		t.Fatalf("Compare() of a pinned file changed by hand = %v", byHand)
	}
	// Harness fields written by a change that is not the landing.
	notYet := map[string]string{recordPath: recordText(declared())}
	written := Compare(Comparison{Records: []string{recordPath}, Candidate: tree(map[string]string{recordPath: recordText(fetched), file: pinned}), Base: tree(notYet)})
	if len(written) != 1 || written[0].Path != recordPath || !strings.Contains(written[0].Says, "size, fetched_from, fetched_at, sha256") {
		t.Fatalf("Compare() of harness fields written by another change = %v", written)
	}
	// The same change is the landing's to make.
	if problems := Compare(Comparison{Records: []string{recordPath}, Candidate: tree(map[string]string{recordPath: recordText(fetched), file: pinned}), Base: tree(notYet), Landing: recordPath}); len(problems) != 0 {
		t.Fatalf("the landing's own change was refused: %v", problems)
	}
	// A pinned file added by hand beside a record not yet fetched.
	added := Compare(Comparison{Records: []string{recordPath}, Candidate: tree(map[string]string{recordPath: recordText(declared()), file: pinned}), Base: tree(notYet)})
	if len(added) != 1 || added[0].Path != file || !strings.Contains(added[0].Says, "carries no checksum") {
		t.Fatalf("Compare() of a pinned file added by hand = %v", added)
	}
	// Changing the version clears the harness's fields, and may give a checksum.
	bumped := declared()
	bumped.URL, bumped.Version, bumped.SHA256 = strings.ReplaceAll(bumped.URL, "4.4.1", "4.5.0"), "4.5.0", strings.Repeat("b", 64)
	if problems := compare(map[string]string{recordPath: recordText(bumped)}, ""); len(problems) != 0 {
		t.Fatalf("a version change that cleared the harness's fields was refused: %v", problems)
	}
	stale := fetched
	stale.URL, stale.Version = bumped.URL, bumped.Version
	if problems := compare(map[string]string{recordPath: recordText(stale), file: pinned}, ""); len(problems) != 1 || !strings.Contains(problems[0].Says, "keeps the harness's fields") {
		t.Fatalf("Compare() of a version change keeping the old fetch = %v", problems)
	}
	// The landing changes nothing but the harness's fields.
	moved := fetched
	moved.For = "yoyodyne-ifd.1"
	if problems := Compare(Comparison{Records: []string{recordPath}, Candidate: tree(map[string]string{recordPath: recordText(moved), file: pinned}), Base: tree(notYet), Landing: recordPath}); len(problems) != 1 || !strings.Contains(problems[0].Says, "other than the harness's own") {
		t.Fatalf("Compare() of a landing that changed a declared field = %v", problems)
	}
}
