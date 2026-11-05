package runstate

// Filing the record of heavy check places that builds before this one kept at
// the top of the home under the projects it belongs to. The top of the home
// holds only what no single project owns (docs/designs/machine-home.md), and
// every permit and every measurement in that record names the product whose
// run it was, so each goes to that product's part. Nothing there is removed
// until all of it has landed.

import (
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// topLevelRecord is what a session read of the top-level record: the record of
// places and the measurements beside it, and whether both could be read. One
// that could not be read is left where it is and filed nowhere.
type topLevelRecord struct {
	record       HeavyCheckRecord
	measurements HeavyCheckMeasurements
	readable     bool
}

// products is every product the top-level record names.
func (t topLevelRecord) products() []string {
	var products []string
	for _, permit := range t.record.Permits {
		products = append(products, permit.Product)
	}
	for _, measurement := range t.measurements.Measurements {
		products = append(products, measurement.Product)
	}
	return products
}

// readTopLevel reads the top-level record under its lock.
func readTopLevel(root *repowrite.PinnedRoot) topLevelRecord {
	record, err := readHeavyCheckRecord(root, false)
	if err != nil {
		return topLevelRecord{}
	}
	measurements, err := readHeavyCheckMeasurements(root, false)
	if err != nil {
		return topLevelRecord{}
	}
	return topLevelRecord{record: record, measurements: measurements, readable: true}
}

// adoptTopLevel files the top-level record under each product's part, with
// every lock held: each permit goes into its product's part with its file moved
// beside it, so a check still running keeps its place and the lock its
// processes hold on that file, and each measurement goes into its product's
// measurements. Only once every part is written are the top-level files
// removed, and the directory with them. A failure part way leaves the top-level
// record where it is, and the next request for a place files what is left: a
// permit or measurement a part already holds is not filed twice.
func (h *heavyCheckSession) adoptTopLevel(top topLevelRecord) {
	if !top.readable {
		return
	}
	from, err := repowrite.NewRoot(h.store.topLevel())
	if err != nil {
		return
	}
	complete := true
	for _, permit := range top.record.Permits {
		if _, filed := h.owner[permit.ID]; filed {
			continue
		}
		part := h.parts[permit.Product]
		if part == nil {
			complete = false
			continue
		}
		if exists, _ := h.topLevel.root.Exists(permit.Hold); exists && permit.Hold != "" {
			to, err := repowrite.NewRoot(h.store.Directory(permit.Product))
			if err != nil {
				complete = false
				continue
			}
			if _, err := repowrite.Move(from, permit.Hold, to, permit.Hold, 0o700); err != nil {
				complete = false
				continue
			}
		}
		h.owner[permit.ID] = permit.Product
		h.record.Permits = append(h.record.Permits, permit)
		for check, limit := range top.record.Limits {
			if _, known := part.limits[check]; !known {
				part.limits[check] = limit
			}
		}
	}
	if err := h.save(); err != nil {
		return
	}
	byProduct := map[string][]HeavyCheckMeasurement{}
	for _, measurement := range top.measurements.Measurements {
		byProduct[measurement.Product] = append(byProduct[measurement.Product], measurement)
	}
	for product, measurements := range byProduct {
		part := h.parts[product]
		if part == nil {
			complete = false
			continue
		}
		record, err := readHeavyCheckMeasurements(part.root, true)
		if err != nil {
			complete = false
			continue
		}
		filed := map[string]bool{}
		for _, measurement := range record.Measurements {
			filed[measurement.Permit] = true
		}
		var adopted []HeavyCheckMeasurement
		for _, measurement := range measurements {
			if !filed[measurement.Permit] {
				adopted = append(adopted, measurement)
			}
		}
		if len(adopted) == 0 {
			continue
		}
		// The top-level launches came before any this part holds.
		record.Measurements = append(adopted, record.Measurements...)
		record.SchemaVersion = HeavyCheckMeasurementSchemaVersion
		if err := writeHeavyCheckMeasurements(part.root, record); err != nil {
			complete = false
			continue
		}
		if err := part.root.Sync(); err != nil {
			complete = false
		}
	}
	if !complete {
		return
	}
	for _, name := range []string{heavyCheckRecordName, heavyCheckMeasurementsName, heavyCheckLockName} {
		_ = h.topLevel.root.Remove(name)
	}
	// The directory goes only where nothing else is left in it: anything there
	// that is not the record's is not this filing's to remove.
	if parent, err := repowrite.NewRoot(filepath.Dir(h.store.topLevel())); err == nil {
		if entries, err := h.topLevel.root.ReadDirectory("."); err == nil && len(entries) == 0 {
			_, _ = parent.RemoveDirectory(HeavyCheckDirectory)
		}
	}
}
