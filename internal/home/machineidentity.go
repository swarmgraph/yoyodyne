package home

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// MachineIdentityFileName is the file at the top of a home that names the
// machine the home is on, for the factory identity that claims, shared records,
// and pull request descriptions carry
// (docs/designs/working-alongside-other-contributors.md, section 5). It is
// machine-wide, like the operator's pause, so every project in the home shares
// it and the migration moves it to the top of the new home.
//
// It is not the identifier run state keeps among one product's runs to tell an
// execution launched here from one launched elsewhere: that one is read by
// recovery against records already written, and changing what it reads would
// make those records look foreign.
const MachineIdentityFileName = "machine-identity.json"

var machineIdentityPattern = regexp.MustCompile(`^machine-[0-9a-f]{32}$`)

// machineIdentityRecord is the whole of the machine identity file.
type machineIdentityRecord struct {
	Machine   string    `json:"machine"`
	CreatedAt time.Time `json:"created_at"`
}

// MachineIdentity is the identifier of the machine a home is on, made the first
// time it is asked for and read back from the home on every later call. It is
// never made again while the file exists: one that cannot be read is an error
// naming the file, rather than a new identifier that would make this machine
// read as another factory to every contributor that has seen the first.
//
// Two processes asking at once agree on one identifier, because the file is
// created only where its name is unused and the one that loses reads the
// winner's.
func MachineIdentity(root string) (string, error) {
	if !filepath.IsAbs(root) {
		return "", errors.New("the machine home must be an absolute path")
	}
	target := filepath.Join(filepath.Clean(root), MachineIdentityFileName)
	if machine, err := readMachineIdentity(target); err == nil || !errors.Is(err, os.ErrNotExist) {
		return machine, err
	}
	written, err := ensureRoot(root)
	if err != nil {
		return "", fmt.Errorf("open the machine home %s: %w", root, err)
	}
	content, err := json.MarshalIndent(machineIdentityRecord{Machine: newMachineIdentity(), CreatedAt: time.Now().UTC()}, "", "  ")
	if err != nil {
		return "", err
	}
	if _, _, err := written.CreateFile(MachineIdentityFileName, append(content, '\n')); err != nil {
		return "", fmt.Errorf("record this machine's identity in %s: %w", target, err)
	}
	// Read back whether or not this call was the one that created it, so the
	// answer is the identifier the file holds.
	return readMachineIdentity(target)
}

func readMachineIdentity(target string) (string, error) {
	source, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return "", fmt.Errorf("read this machine's identity from %s: %w", target, err)
	}
	var record machineIdentityRecord
	if err := json.Unmarshal(source, &record); err != nil {
		return "", fmt.Errorf("read this machine's identity from %s: %w", target, err)
	}
	machine := strings.TrimSpace(record.Machine)
	if !machineIdentityPattern.MatchString(machine) {
		return "", fmt.Errorf("read this machine's identity from %s: %q is not an identifier this harness makes", target, record.Machine)
	}
	return machine, nil
}

func newMachineIdentity() string {
	bytes := make([]byte, 16)
	// crypto/rand.Read does not fail on any platform this builds for.
	_, _ = rand.Read(bytes)
	return "machine-" + hex.EncodeToString(bytes)
}
