package cli

// A long-running part of the product saying, as it starts, which configuration
// keys its build reads.
//
// The part goes on running the build it was started from while landings move
// the configuration on, and a key its build does not know makes every read it
// makes of the file fail. What it records here is what lets any later build —
// `yoyo doctor`, `yoyo config validate`, `yoyo status` — name the part, its
// build, and the keys, rather than the part showing a person the decoder's
// error and nothing else.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordConfigReader writes this process's account under the product. It
// never stops the part: a part that could not say what it reads is a part the
// comparison says nothing about, which is every part before this existed, and
// refusing to start over it would be the reporting failing the work.
func recordConfigReader(resolved config.Resolved, service string, stderr io.Writer) {
	if err := writeConfigReader(resolved, service); err != nil {
		fmt.Fprintf(stderr, "the %s service could not record which configuration keys its build reads, so nothing will say if a later configuration carries one it cannot: %v\n", service, err)
	}
}

func writeConfigReader(resolved config.Resolved, service string) error {
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return err
	}
	return writeConfigReaderAt(resolved, service, stateRoot)
}

func writeConfigReaderAt(resolved config.Resolved, service, stateRoot string) error {
	store, err := runstate.NewConfigReaderStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return err
	}
	path, err := filepath.Abs(resolved.Path)
	if err != nil {
		return fmt.Errorf("resolve the configuration's path: %w", err)
	}
	return store.Record(runstate.ConfigReader{
		Service:    service,
		PID:        os.Getpid(),
		Build:      buildinfo.Commit(),
		ConfigPath: path,
		StartedAt:  time.Now().UTC(),
		Keys:       config.SchemaKeys(),
	})
}

// configMismatches is every running part that cannot read something in the
// configuration it reads, from the records the parts wrote. A product with no
// state root to read them from has nothing to compare.
func configMismatches(resolved config.Resolved) ([]runstate.ConfigMismatch, error) {
	stateRoot, err := productStateRoot(resolved)
	if err != nil {
		return nil, err
	}
	store, err := runstate.NewConfigReaderStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, err
	}
	return store.Mismatches()
}

// describeConfigMismatches says each mismatch on a line of its own, for the
// stream an aside belongs on, and nothing where there is none.
func describeConfigMismatches(mismatches []runstate.ConfigMismatch, problem error) string {
	var lines []string
	for _, mismatch := range mismatches {
		lines = append(lines, "warning: "+mismatch.Says()+"; "+runstate.ConfigMismatchRemedy(mismatch.Service))
	}
	if problem != nil {
		lines = append(lines, "warning: whether every running part of the product can read this configuration was not fully checked: "+problem.Error())
	}
	return strings.Join(lines, "\n")
}

// landingConfigReaders is the store a landing asks, or nothing where the
// product has no state root to read it from — returned as an untyped nil, so
// the pipeline's optional field reads as unwired rather than as a nil store.
func landingConfigReaders(stateRoot string, productID domain.ProductID) orchestrator.ConfigReaders {
	store, err := runstate.NewConfigReaderStore(stateRoot, productID)
	if err != nil {
		return nil
	}
	return store
}
