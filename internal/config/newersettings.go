package config

// What a build does with a setting it does not know.
//
// The decoder refuses a key it does not know, so a misspelled setting fails
// the whole file rather than being quietly ignored. A landing that adds a
// setting and sets it in the same change puts a key in the file that every
// part still running an older build has never heard of, and to the decoder
// that looks exactly like a misspelling: every part refuses the whole file
// until it moves onto the new build, and new work stops.
//
// The two are told apart by asking the repository the configuration is kept
// in whether this build is behind it. A build made from a commit the
// repository has since moved past may be missing settings that landed after
// it, so it leaves out each setting it does not know, says which once, and
// reads the rest. A build made from the repository's own commit, a build from
// a commit the repository does not have, and a build that recorded no commit
// at all cannot say a key is newer than they are, so they refuse it as a
// misspelling, as before. A misspelling therefore still fails wherever the
// current build reads the file — every check, `yoyo config validate`, and
// every part once it moves onto that build.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// LeavesOutNewerSettings is true of every build that carries this file: it
// reads a configuration with settings added after it, leaving those out. A
// running part records it so a later build can say whether that part is still
// reading the configuration or refusing it.
const LeavesOutNewerSettings = true

// olderBuild says whether this build is older than the configuration it is
// reading, and if it is, which build it is. A nil olderBuild is a reader that
// cannot ask, such as a configuration decoded from a stream, and is never
// older.
type olderBuild func() (build string, older bool)

// newerSettingsLog is where a build says, once per setting, which settings it
// left out: a long-running part's standard error is its log.
var newerSettingsLog io.Writer = os.Stderr

var (
	newerSettingsSaid   = map[string]bool{}
	newerSettingsSaidMu sync.Mutex
)

var fullCommit = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// newerSettingsGitTimeout bounds each Git question; a repository that does not
// answer in time leaves the build unable to say it is older, so it refuses.
const newerSettingsGitTimeout = 10 * time.Second

// olderThanRepository is the olderBuild for a configuration kept at path, read
// by this build.
func olderThanRepository(path string) olderBuild {
	return olderThanRepositoryAs(path, buildinfo.Commit())
}

// olderThanRepositoryAs is olderThanRepository for a build made from build:
// it is older when the repository holding the file has build in its history
// and has moved past it.
func olderThanRepositoryAs(path, build string) olderBuild {
	return func() (string, bool) {
		build := strings.TrimSpace(build)
		if !fullCommit.MatchString(build) {
			return build, false
		}
		directory := filepath.Dir(path)
		head, err := newerSettingsGit(directory, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || strings.TrimSpace(head) == build {
			return build, false
		}
		if _, err := newerSettingsGit(directory, "merge-base", "--is-ancestor", build, "HEAD"); err != nil {
			return build, false
		}
		return build, true
	}
}

func newerSettingsGit(directory string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), newerSettingsGitTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	command.Env = execution.GitEnvironment(nil)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := command.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}

// withoutNewerSettings is the configuration source with every key this build
// does not know taken out, and the keys it took out, as dotted paths in the
// file's own names. It returns nothing taken out where every key is known,
// where the build is not older than the file, or where the source cannot be
// read as YAML; the caller then refuses the file as it always has.
func withoutNewerSettings(source []byte, older olderBuild) ([]byte, []string, string) {
	if older == nil {
		return nil, nil, ""
	}
	unknown, err := UnreadableKeys(source, SchemaKeys())
	if err != nil || len(unknown) == 0 {
		return nil, nil, ""
	}
	build, isOlder := older()
	if !isOlder {
		return nil, nil, ""
	}
	// Only the first document is pruned and kept, so a source with a second
	// one is left for the strict decode to refuse.
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	var root, extra yaml.Node
	if err := decoder.Decode(&root); err != nil {
		return nil, nil, ""
	}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, nil, ""
	}
	pruneUnknownKeys(documentContent(&root), nil, schemaPatterns(SchemaKeys()))
	pruned, err := yaml.Marshal(&root)
	if err != nil {
		return nil, nil, ""
	}
	return pruned, unknown, build
}

// pruneUnknownKeys removes, in place, each mapping entry whose key the schema
// does not have at that place. A key reached only through a merge is left
// where it is, so the strict decode that follows still refuses it.
func pruneUnknownKeys(node *yaml.Node, shape []string, patterns [][]string) {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	switch node.Kind {
	case yaml.MappingNode:
		kept := make([]*yaml.Node, 0, len(node.Content))
		for index := 0; index+1 < len(node.Content); index += 2 {
			key, value := node.Content[index], node.Content[index+1]
			if key.Tag == "!!merge" || key.Value == "<<" {
				kept = append(kept, key, value)
				continue
			}
			child, known := matchKey(shape, key.Value, patterns)
			if !known {
				continue
			}
			if hasChildren(child, patterns) {
				pruneUnknownKeys(value, child, patterns)
			}
			kept = append(kept, key, value)
		}
		node.Content = kept
	case yaml.SequenceNode:
		item := appendPath(shape, SchemaItem)
		if !matches(item, patterns) || !hasChildren(item, patterns) {
			return
		}
		for _, entry := range node.Content {
			pruneUnknownKeys(entry, item, patterns)
		}
	}
}

// sayNewerSettings writes, once for each setting in this process, that the
// build left it out and why.
func sayNewerSettings(log io.Writer, path, build string, keys []string) {
	newerSettingsSaidMu.Lock()
	defer newerSettingsSaidMu.Unlock()
	var fresh []string
	for _, key := range keys {
		said := path + "\x00" + key
		if newerSettingsSaid[said] {
			continue
		}
		newerSettingsSaid[said] = true
		fresh = append(fresh, key)
	}
	if len(fresh) == 0 {
		return
	}
	where := path
	if where == "" {
		where = "the configuration"
	}
	fmt.Fprintf(log,
		"warning: %s sets %s, which this build (%s) does not know; the repository has moved past this build, so these are taken to be settings added since it was built, and this part goes on without them until it moves onto a newer build\n",
		where, strings.Join(fresh, ", "), shortCommit(build))
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
