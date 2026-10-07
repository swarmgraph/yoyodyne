package runstate

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

// The machine an execution was launched on is named by an identifier the
// harness generates the first time it needs one and keeps in the run state
// directory, rather than by the host name. macOS gives a machine a new host
// name when it joins another network, so an execution recorded under the name
// the machine had then would read, after the change, as one started somewhere
// else, and recovery would wait on a person for an execution this machine can
// see perfectly well.
//
// Executions recorded before the identifier existed name only a host. The file
// also keeps every host name this machine has been seen under since, so one of
// those is still recognised here after the name changes again.

// machineFileName is the file, in the run state directory, that names this
// machine. It does not end in .json, so nothing reading the directory for runs
// mistakes it for one.
const machineFileName = "machine.identity"

// maxMachineHosts bounds how many earlier host names the file keeps; the
// oldest is let go first.
const maxMachineHosts = 32

var machineIDPattern = regexp.MustCompile(`^machine-[0-9a-f]{32}$`)

// machineRecord is the whole of the machine file.
type machineRecord struct {
	Machine string   `json:"machine"`
	Hosts   []string `json:"hosts,omitempty"`
}

// thisMachine is this machine as the run state directory names it, together
// with its host name now. A host name the operating system will not give is
// left empty, and nothing is added to the file for it.
type thisMachine struct {
	machineRecord
	Host string
}

// launchedHere says whether an execution was launched on this machine: by its
// machine identifier where it records one, and otherwise by a host name this
// machine has been seen under.
func (m thisMachine) launchedHere(identity ExecutionIdentity) bool {
	if identity.Machine != "" {
		return identity.Machine == m.Machine
	}
	return identity.Host != "" && slices.Contains(m.Hosts, identity.Host)
}

// describe names this machine in a sentence a person reads.
func (m thisMachine) describe() string {
	if m.Host == "" {
		return m.Machine
	}
	return fmt.Sprintf("%s (%s)", m.Host, m.Machine)
}

// machine reads this machine's identity from the run state directory, making
// it the first time, and records the current host name there if it is new.
// Two processes making it at once agree on one: the file is created only where
// its name is unused, and whichever loses reads the winner's.
func (s *Store) machine() (thisMachine, error) {
	hostname := s.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	host, hostErr := hostname()
	if hostErr != nil {
		host = ""
	}
	host = strings.TrimSpace(host)
	stateRoot, anchor, err := confinedStateRoot(s.root)
	if err != nil {
		return thisMachine{}, err
	}
	root, err := pinStateRoot(stateRoot, anchor)
	if err != nil {
		return thisMachine{}, err
	}
	defer root.Close()
	for tries := 0; ; tries++ {
		source, err := root.ReadFile(machineFileName)
		if errors.Is(err, os.ErrNotExist) {
			created := machineRecord{Machine: newMachineID()}
			if host != "" {
				created.Hosts = []string{host}
			}
			content, err := json.Marshal(created)
			if err != nil {
				return thisMachine{}, err
			}
			err = root.CreateFile(machineFileName, content, 0o600)
			if errors.Is(err, os.ErrExist) && tries < 2 {
				continue
			}
			if err != nil {
				return thisMachine{}, fmt.Errorf("record this machine's identity: %w", err)
			}
			return thisMachine{machineRecord: created, Host: host}, nil
		}
		if err != nil {
			return thisMachine{}, fmt.Errorf("read this machine's identity: %w", err)
		}
		record, err := parseMachineRecord(source)
		if err != nil {
			return thisMachine{}, fmt.Errorf("read this machine's identity from %s: %w", machineFileName, err)
		}
		if host != "" && !slices.Contains(record.Hosts, host) {
			record.Hosts = append(record.Hosts, host)
			if len(record.Hosts) > maxMachineHosts {
				record.Hosts = record.Hosts[len(record.Hosts)-maxMachineHosts:]
			}
			content, err := json.Marshal(record)
			if err != nil {
				return thisMachine{}, err
			}
			// Another process adding a name at the same moment may have its own
			// write replace this one. The name is recognised for as long as this
			// call's answer is used, and is added again the next time it is asked.
			if err := root.WriteFile(machineFileName, content, 0o600, false); err != nil {
				return thisMachine{}, fmt.Errorf("record this machine's host name %s: %w", host, err)
			}
		}
		return thisMachine{machineRecord: record, Host: host}, nil
	}
}

func newMachineID() string {
	bytes := make([]byte, 16)
	// crypto/rand.Read does not fail on any platform this builds for.
	_, _ = rand.Read(bytes)
	return "machine-" + hex.EncodeToString(bytes)
}

// parseMachineRecord reads the machine file. A field this build does not know
// is passed over rather than refused, so a file a later build wrote still names
// the machine here.
func parseMachineRecord(source []byte) (machineRecord, error) {
	var record machineRecord
	if err := json.Unmarshal(source, &record); err != nil {
		return machineRecord{}, err
	}
	if !machineIDPattern.MatchString(record.Machine) {
		return machineRecord{}, fmt.Errorf("machine identifier %q is not one this harness makes", record.Machine)
	}
	return record, nil
}
