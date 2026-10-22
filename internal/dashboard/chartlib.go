package dashboard

// The backlog page draws with Chart.js at one exact version, checked into the
// assets beside a record of it, as the observability-and-dashboard design's
// "the chart library" rules: served by the dashboard itself, so the policy
// stays script-src 'self' and nothing is fetched from a CDN.
//
// The library is the one script here that this project did not write, so two
// files go in together and neither goes in alone:
//
//   - assets/chart.umd.js, the file the chart.js npm package ships as
//     package/dist/chart.umd.js, byte for byte;
//   - assets/chartjs.md, its record: a "Version:" line with the exact version
//     (three numbers, no range), a "Source:" line saying where the file was
//     taken from, a "SHA-256:" line with the file's checksum in hex, and the
//     package's MIT license text below them.
//
// TestTheVendoredChartLibraryMatchesItsRecord holds the two to each other.
// Changing the version is replacing both in one reviewed change. Until the
// library is checked in, the backlog page loads no chart script and shows its
// figures as a table, saying the chart library is not in this build.

import (
	"bufio"
	"bytes"
	"io/fs"
	"strings"
)

const (
	// chartLibraryAsset is the vendored library, and chartLibraryRecord the
	// record beside it.
	chartLibraryAsset  = "assets/chart.umd.js"
	chartLibraryRecord = "assets/chartjs.md"
)

// chartLibraryPresent says whether this build carries the chart library, which
// decides whether the backlog page loads it.
func chartLibraryPresent() bool {
	_, err := fs.Stat(assets, chartLibraryAsset)
	return err == nil
}

// chartRecord is what the record says of the vendored library.
type chartRecord struct {
	Version string
	Source  string
	SHA256  string
	License string
}

// readChartRecord reads the record's three lines and keeps the rest as the
// license text.
func readChartRecord(content []byte) chartRecord {
	var record chartRecord
	var license strings.Builder
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "Version:"):
			record.Version = strings.TrimSpace(strings.TrimPrefix(line, "Version:"))
		case strings.HasPrefix(line, "Source:"):
			record.Source = strings.TrimSpace(strings.TrimPrefix(line, "Source:"))
		case strings.HasPrefix(line, "SHA-256:"):
			record.SHA256 = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "SHA-256:")))
		default:
			license.WriteString(line)
			license.WriteString("\n")
		}
	}
	record.License = license.String()
	return record
}
