// Package dolttooltest stands in for dolt's published release in tests, so no
// test reaches the network.
package dolttooltest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/dolttool"
)

// Release is an archive laid out as dolt publishes one.
func Release(t *testing.T, goos, goarch, program string) []byte {
	t.Helper()
	var built bytes.Buffer
	compressed := gzip.NewWriter(&built)
	entries := tar.NewWriter(compressed)
	header := &tar.Header{Name: "dolt-" + goos + "-" + goarch + "/bin/dolt", Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(program))}
	if err := entries.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entries, program); err != nil {
		t.Fatal(err)
	}
	if err := entries.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return built.Bytes()
}

// Serving answers every request with content, and counts them.
type Serving struct {
	Content  []byte
	Requests []string
}

// RoundTrip implements http.RoundTripper.
func (s *Serving) RoundTrip(request *http.Request) (*http.Response, error) {
	s.Requests = append(s.Requests, request.URL.String())
	return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(s.Content)), Request: request}, nil
}

// Installer installs content as dolt for one platform, pinned to its checksum.
func Installer(goos, goarch string, content []byte) (dolttool.Installer, *Serving) {
	sum := sha256.Sum256(content)
	serving := &Serving{Content: content}
	return dolttool.Installer{GOOS: goos, GOARCH: goarch, Checksum: hex.EncodeToString(sum[:]), Transport: serving}, serving
}

// Install puts a stand-in dolt in the state directory's tools folder.
func Install(t *testing.T, stateRoot string) {
	t.Helper()
	installer, _ := Installer("darwin", "arm64", Release(t, "darwin", "arm64", "dolt"))
	if err := installer.Install(context.Background(), stateRoot); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
}
