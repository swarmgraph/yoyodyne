package runstate

import (
	"context"
	"errors"
	"os"
)

// LockFile takes an exclusive lock on an open file, waiting for whoever holds it
// until the context ends, for a record of this product's state kept outside this
// package. The function returned drops the lock and closes the file. A lock that
// could not be taken leaves the file closed.
func LockFile(ctx context.Context, file *os.File) (func() error, error) {
	if err := lockStateFile(ctx, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return func() error { return releaseStateFile(file) }, nil
}
