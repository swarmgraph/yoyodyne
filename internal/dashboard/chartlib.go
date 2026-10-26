package dashboard

// The backlog page draws with Chart.js at one exact version, kept in the
// assets, as the observability-and-dashboard design's "the chart library"
// rules: served by the dashboard itself, so the policy stays script-src 'self'
// and the browser fetches nothing from a CDN.
//
// The library is a pinned outside file (docs/designs/pinned-outside-files.md):
// nobody checks it in by hand. Its record, assets/chart.umd.js.yoyodyne-source.yaml,
// says the exact version, the address, and the license, and the harness fetches
// assets/chart.umd.js and its license, assets/chart.js-LICENSE.md, from the
// address each record gives and writes the file's checksum into the record.
// This file reads what those records say and nothing else:
//
//   - the version and the checksum come from the library's record;
//   - the license text comes from the license file that record names.
//
// The library is loaded only where it matches its record: its checksum is the
// one recorded, it names the recorded version, and its MIT license is beside
// it. TestTheChartLibraryMatchesItsPinnedRecord holds the build's own assets to
// that. Until the harness has fetched the library, the backlog page loads no
// chart script and shows its figures as a table, saying the chart library is
// not in this build. Changing the version is changing the record's address and
// version in a reviewed change, and the harness fetches the new file.

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/outsidefile"
)

const (
	// chartLibraryAsset is the pinned library, and chartLibraryRecord the
	// harness's record of it.
	chartLibraryAsset  = "assets/chart.umd.js"
	chartLibraryRecord = chartLibraryAsset + outsidefile.RecordSuffix
)

// chartLibraryLicense is text the MIT license file has to carry.
var chartLibraryLicense = []string{"MIT License", "Permission is hereby granted, free of charge", "Chart.js"}

// errNoChartLibrary is a build the harness has not yet fetched the library
// into.
var errNoChartLibrary = errors.New("the chart library is not in this build")

// chartLibrary is what the library's record says of it, read with the library.
type chartLibrary struct {
	Record  outsidefile.Record
	License string
}

// chartLibraryIn reads the library and its record from files. It is
// errNoChartLibrary where the library is absent, and any other error is the
// library and its record disagreeing, which leaves the page without a chart.
func chartLibraryIn(files fs.FS) (chartLibrary, error) {
	library, libraryErr := fs.ReadFile(files, chartLibraryAsset)
	content, recordErr := fs.ReadFile(files, chartLibraryRecord)
	switch {
	case libraryErr != nil && errors.Is(libraryErr, fs.ErrNotExist):
		return chartLibrary{}, errNoChartLibrary
	case libraryErr != nil:
		return chartLibrary{}, libraryErr
	case recordErr != nil:
		return chartLibrary{}, fmt.Errorf("%s is in this build without %s, the record of its version, checksum, and license: %w", chartLibraryAsset, chartLibraryRecord, recordErr)
	}
	record, err := outsidefile.ParseRecord(chartLibraryRecord, content)
	if err != nil {
		return chartLibrary{}, err
	}
	if record.SHA256 == "" {
		return chartLibrary{}, fmt.Errorf("%s records no checksum for %s", chartLibraryRecord, chartLibraryAsset)
	}
	if got := outsidefile.Checksum(library); got != record.SHA256 {
		return chartLibrary{}, fmt.Errorf("%s has SHA-256 %s; %s records %s", chartLibraryAsset, got, chartLibraryRecord, record.SHA256)
	}
	if !strings.Contains(string(library), record.Version) {
		return chartLibrary{}, fmt.Errorf("%s does not name version %s, which %s says it is", chartLibraryAsset, record.Version, chartLibraryRecord)
	}
	if record.License != "MIT" {
		return chartLibrary{}, fmt.Errorf("%s records license %q, not MIT", chartLibraryRecord, record.License)
	}
	licensePath := record.LicensePath(chartLibraryRecord)
	license, err := fs.ReadFile(files, licensePath)
	if err != nil {
		return chartLibrary{}, fmt.Errorf("%s is in this build without %s, the license its record names: %w", chartLibraryAsset, licensePath, err)
	}
	for _, expected := range chartLibraryLicense {
		if !strings.Contains(string(license), expected) {
			return chartLibrary{}, fmt.Errorf("%s lacks %q from the license text", licensePath, expected)
		}
	}
	return chartLibrary{Record: record, License: string(license)}, nil
}

// chartLibraryLoads says whether the backlog page loads the library from
// files: it is there and matches its record.
func chartLibraryLoads(files fs.FS) bool {
	_, err := chartLibraryIn(files)
	return err == nil
}
