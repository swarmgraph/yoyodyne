package runstate

import (
	"errors"
	"fmt"
	"strings"
)

// ReviewBoundRefusal is the size check's refusal of a change the reviewer's copy
// would keep source or test files of out, handed back to the developer once
// (docs/decisions/review-size-limits.md, section 2). It is a repair input like
// a path refusal: the size check is decided after the protected-path check and
// before the developer's execution record and the checks, so recording one
// clears the inputs those later gates would have produced, and a change that
// passes the size check clears it.
//
// Files are the lines the refusal already writes onto the item, each naming one
// file kept out, its size, and the limit it was compared against. Omitted counts
// the files past the ones named.
type ReviewBoundRefusal struct {
	Files   []string `json:"files"`
	Omitted int      `json:"omitted,omitempty"`
}

// Validate refuses a refusal that names nothing kept out.
func (r ReviewBoundRefusal) Validate() error {
	if len(r.Files) == 0 {
		return errors.New("a review-bound refusal names at least one file kept out")
	}
	for index, file := range r.Files {
		if strings.TrimSpace(file) == "" {
			return fmt.Errorf("files[%d] is empty", index)
		}
	}
	if r.Omitted < 0 {
		return errors.New("omitted cannot be negative")
	}
	return nil
}
