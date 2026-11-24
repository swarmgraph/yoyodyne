// Package dolttool installs the one dolt release the harness uses, into the
// harness's own tools folder, and says whether it is there.
//
// bd's server mode needs a dolt program. The harness relies on the copy its own
// setup installed and on no other, so a dolt on PATH is never looked at
// (docs/designs/tracker-store-size.md, condition 4). Nothing here starts dolt:
// its version command asks the network for the newest release and writes files,
// so what is installed is told from its content and from the record the install
// left beside it.
package dolttool

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Version is the dolt the harness installs: the one measured with bd 1.1.2. A
// later one needs a fresh measurement and a revision of the design.
const Version = "2.7.6"

const (
	// ToolsDirectoryName is the harness's tools folder under its state directory.
	ToolsDirectoryName = "tools"

	programName = "dolt"
	recordName  = "dolt.json"

	downloadTimeout = 15 * time.Minute
	maxArchiveBytes = 256 << 20
	maxProgramBytes = 512 << 20
	maxRecordBytes  = 4 << 10
)

// checksums are the SHA-256 checksums of Version's release archives, keyed by
// operating system and architecture as Go spells them, such as "darwin/arm64".
// A platform with none is one nothing is downloaded for: a checksum read from
// the place the archive comes from would change whenever the archive did.
//
// They are the digests in GitHub's release record for dolthub/dolt, tag
// v2.7.6, which ChecksumsCommand prints.
var checksums = map[string]string{
	"darwin/amd64": "2074df3091030d50c6bc910c180d0fe7f20fedf48a9479b59b71e520ecf05ad3",
	"darwin/arm64": "229a99be0c0413a0fbed8c9bf65d7624578fbaafe8a653614a6ba8d0535a3726",
	"linux/amd64":  "02479bf57f3c46c46c4f6977e948d56e88f5ee5f2a73da8226e7ebc31039639e",
	"linux/arm64":  "143ed0be2948be091fe61cbebb3de4501dc1e924dd0829ecbc447db2a3c8e42d",
}

// ChecksumsCommand prints the checksums published for Version's archives, for
// whoever fills in the table above.
const ChecksumsCommand = `gh api repos/dolthub/dolt/releases/tags/v` + Version + ` --jq '.assets[] | [.name, .digest] | @tsv'`

// Checksum is the checksum this build holds for a platform's archive, and empty
// where it holds none.
func Checksum(goos, goarch string) string { return checksums[goos+"/"+goarch] }

// Path is where the harness's dolt is, under the state directory.
func Path(stateRoot string) string {
	return filepath.Join(stateRoot, ToolsDirectoryName, programName)
}

// State is what the tools folder holds.
type State string

const (
	// Installed is the wanted version, unchanged since setup installed it.
	Installed State = "installed"
	// Missing is no dolt that setup installed.
	Missing State = "missing"
	// OtherVersion is a dolt setup installed at another version.
	OtherVersion State = "other version"
	// Changed is a file that is not what setup installed.
	Changed State = "changed"
)

// Inspection is what Inspect found.
type Inspection struct {
	State State
	// Version is the version the install recorded, where one was read.
	Version string
	// Detail is what was seen, in words.
	Detail string
}

// record is written beside the program once it is in place.
type record struct {
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archive_sha256"`
	ProgramSHA256 string `json:"program_sha256"`
}

// Inspect says whether the wanted dolt is in the tools folder. It only reads,
// and follows no link: a link into another installation is a copy somebody else
// changes.
func Inspect(stateRoot string) (Inspection, error) {
	path := Path(stateRoot)
	directory, err := os.Lstat(filepath.Dir(path))
	if err == nil && !directory.IsDir() {
		return Inspection{State: Changed, Detail: filepath.Dir(path) + " is not a directory"}, nil
	}
	var program fs.FileInfo
	if err == nil {
		program, err = os.Lstat(path)
	}
	if errors.Is(err, fs.ErrNotExist) {
		return Inspection{State: Missing, Detail: "there is no file at " + path}, nil
	}
	if err != nil {
		return Inspection{}, err
	}
	if !program.Mode().IsRegular() || program.Mode().Perm()&0o100 == 0 {
		return Inspection{State: Changed, Detail: path + " is not a program file setup installed"}, nil
	}

	found, err := readRecord(filepath.Join(filepath.Dir(path), recordName))
	if errors.Is(err, fs.ErrNotExist) {
		return Inspection{State: Changed, Detail: "setup left no record of installing " + path}, nil
	}
	if err != nil {
		return Inspection{State: Changed, Detail: err.Error()}, nil
	}
	sum, err := fileChecksum(path)
	if err != nil {
		return Inspection{}, err
	}
	switch {
	case sum != found.ProgramSHA256:
		return Inspection{State: Changed, Version: found.Version, Detail: fmt.Sprintf("%s has checksum %s, and setup installed one with %s", path, sum, found.ProgramSHA256)}, nil
	case found.Version != Version:
		return Inspection{State: OtherVersion, Version: found.Version, Detail: path}, nil
	}
	return Inspection{State: Installed, Version: found.Version, Detail: path}, nil
}

func readRecord(path string) (record, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return record{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxRecordBytes {
		return record{}, fmt.Errorf("%s is not a record setup wrote", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return record{}, err
	}
	var read record
	if err := json.Unmarshal(content, &read); err != nil {
		return record{}, fmt.Errorf("read %s: %w", path, err)
	}
	return read, nil
}

func fileChecksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func checksum(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Installer installs dolt for one platform.
type Installer struct {
	GOOS, GOARCH string
	// Checksum is the checksum the archive has to have. Empty installs nothing.
	Checksum string
	// Transport carries the download. Nil is the default transport.
	Transport http.RoundTripper
}

// For is the installer for a platform, holding the checksum this build carries.
func For(goos, goarch string) Installer {
	return Installer{GOOS: goos, GOARCH: goarch, Checksum: Checksum(goos, goarch)}
}

// URL is the published archive this installs from.
func (i Installer) URL() string {
	return fmt.Sprintf("https://github.com/dolthub/dolt/releases/download/v%s/%s.tar.gz", Version, i.archiveName())
}

func (i Installer) archiveName() string { return fmt.Sprintf("dolt-%s-%s", i.GOOS, i.GOARCH) }

// Install downloads the archive, refuses it unless it has the pinned checksum,
// and puts the program in the tools folder. A refusal writes nothing. The
// record is written last, so an install stopped in between reads as changed
// and is made again.
func (i Installer) Install(ctx context.Context, stateRoot string) error {
	if i.Checksum == "" {
		return fmt.Errorf("this build holds no checksum for dolt %s on %s/%s, so nothing is downloaded", Version, i.GOOS, i.GOARCH)
	}
	archive, err := i.download(ctx)
	if err != nil {
		return err
	}
	if sum := checksum(archive); sum != i.Checksum {
		return fmt.Errorf("the file at %s has checksum %s, and dolt %s has %s; nothing was installed", i.URL(), sum, Version, i.Checksum)
	}
	program, err := i.extract(archive)
	if err != nil {
		return err
	}
	content, err := json.MarshalIndent(record{Version: Version, ArchiveSHA256: i.Checksum, ProgramSHA256: checksum(program)}, "", "  ")
	if err != nil {
		return err
	}

	root, err := runstate.PinStateRoot(stateRoot)
	if err != nil {
		return fmt.Errorf("open the state directory: %w", err)
	}
	defer root.Close()
	if err := root.MakeDirectory(ToolsDirectoryName, 0o700); err != nil {
		return fmt.Errorf("create the tools folder: %w", err)
	}
	tools, err := root.OpenDirectory(ToolsDirectoryName)
	if err != nil {
		return fmt.Errorf("open the tools folder: %w", err)
	}
	defer tools.Close()
	for _, file := range []struct {
		name    string
		content []byte
		mode    fs.FileMode
	}{
		{programName, program, 0o700},
		{recordName, append(content, '\n'), 0o600},
	} {
		if err := tools.Unchanged(); err != nil {
			return err
		}
		if err := tools.WriteFile(file.name, file.content, file.mode, false); err != nil {
			return fmt.Errorf("write %s in the tools folder: %w", file.name, err)
		}
	}
	return errors.Join(tools.Unchanged(), tools.Sync())
}

func (i Installer) download(ctx context.Context) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()
	client := &http.Client{
		Transport: i.Transport,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if request.URL.Scheme != "https" || len(via) > 5 {
				return errors.New("the download was redirected somewhere it is not followed to")
			}
			return nil
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, i.URL(), nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download dolt %s: %w", Version, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download dolt %s: the server answered %s for %s", Version, response.Status, i.URL())
	}
	archive, err := readLimited(response.Body, maxArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("download dolt %s: %w", Version, err)
	}
	return archive, nil
}

// extract is the program in a release archive, whose content is
// dolt-<os>-<arch>/bin/dolt and a license.
func (i Installer) extract(archive []byte) ([]byte, error) {
	compressed, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("unpack dolt %s: %w", Version, err)
	}
	wanted := i.archiveName() + "/bin/" + programName
	entries := tar.NewReader(compressed)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("the archive of dolt %s holds no file %s", Version, wanted)
		}
		if err != nil {
			return nil, fmt.Errorf("unpack dolt %s: %w", Version, err)
		}
		if header.Name != wanted || header.Typeflag != tar.TypeReg {
			continue
		}
		program, err := readLimited(entries, maxProgramBytes)
		if err != nil {
			return nil, fmt.Errorf("unpack dolt %s: %w", Version, err)
		}
		return program, nil
	}
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("it is over the %d-byte limit", limit)
	}
	return content, nil
}
