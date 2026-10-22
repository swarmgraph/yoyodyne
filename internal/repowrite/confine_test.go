package repowrite

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// These are the races of the repository-confined-writes design's table, run
// against every writer this package offers. Each move or link happens at an
// exact point between the primitive's steps, through SetTestHook, so each row
// runs the same way every time. The tests that set a hook do not run in
// parallel: the hook is seen by every confined write in the process.

// confinedWriter is one writer, writing in the directory "a/docs" of a root
// that already holds a/docs/target ("original\n") and a/docs/other.
type confinedWriter struct {
	name  string
	write func(t *testing.T, root Root) error
}

func confinedWriters() []confinedWriter {
	pinned := func(t *testing.T, root Root, act func(*PinnedRoot) error) error {
		t.Helper()
		opened, err := OpenPinnedRoot(root.Path())
		if err != nil {
			t.Fatalf("OpenPinnedRoot() error = %v", err)
		}
		defer opened.Close()
		return act(opened)
	}
	return []confinedWriter{
		{"write a document", func(t *testing.T, root Root) error {
			_, err := root.WriteFile("a/docs/target", []byte("new\n"))
			return err
		}},
		{"create a document", func(t *testing.T, root Root) error {
			_, _, err := root.CreateFile("a/docs/fresh", []byte("new\n"))
			return err
		}},
		{"replace a state record", func(t *testing.T, root Root) error {
			return root.ReplaceFile("a/docs/target", []byte("new\n"), 0o600, 0o700)
		}},
		{"append", func(t *testing.T, root Root) error {
			return root.Append("a/docs/target", []byte("new\n"), 0o600, 0o700)
		}},
		{"open an appender", func(t *testing.T, root Root) error {
			appender, err := root.OpenAppender("a/docs/target", 0o600, 0o700)
			if err == nil {
				appender.Close()
			}
			return err
		}},
		{"cut", func(t *testing.T, root Root) error {
			_, err := root.Truncate("a/docs/target", 3)
			return err
		}},
		{"lock", func(t *testing.T, root Root) error {
			file, err := root.OpenLock("a/docs/target.lock", 0o600, 0o700)
			if err == nil {
				file.Close()
			}
			return err
		}},
		{"make a directory", func(t *testing.T, root Root) error {
			_, err := root.MakeDirectory("a/docs/made", 0o700)
			return err
		}},
		{"move", func(t *testing.T, root Root) error {
			_, err := Move(root, "a/docs/target", root, "a/docs/moved", 0o700)
			return err
		}},
		{"pinned file writer", func(t *testing.T, root Root) error {
			return pinned(t, root, func(p *PinnedRoot) error {
				return p.WriteFile("a/docs/target", []byte("new\n"), 0o644, false)
			})
		}},
		{"pinned create", func(t *testing.T, root Root) error {
			return pinned(t, root, func(p *PinnedRoot) error {
				return p.CreateFile("a/docs/fresh", []byte("new\n"), 0o600)
			})
		}},
		{"pinned lock", func(t *testing.T, root Root) error {
			return pinned(t, root, func(p *PinnedRoot) error {
				file, err := p.OpenLock("a/docs/target.lock", 0o600)
				if err == nil {
					file.Close()
				}
				return err
			})
		}},
		{"remove a file", func(t *testing.T, root Root) error {
			_, err := root.RemoveFile("a/docs/other")
			return err
		}},
		{"remove a directory", func(t *testing.T, root Root) error {
			_, err := root.RemoveDirectory("a/docs/sub")
			return err
		}},
		{"pinned remove", func(t *testing.T, root Root) error {
			return pinned(t, root, func(p *PinnedRoot) error { return p.Remove("a/docs/other") })
		}},
		{"pinned symlink", func(t *testing.T, root Root) error {
			return pinned(t, root, func(p *PinnedRoot) error {
				return p.Symlink("target", "a/docs/link")
			})
		}},
	}
}

// prepareDocs builds a/docs under the root, and returns its path.
func prepareDocs(t *testing.T, root Root) string {
	t.Helper()
	docs := filepath.Join(root.Path(), "a", "docs")
	writeFile(t, filepath.Join(docs, "target"), "original\n")
	writeFile(t, filepath.Join(docs, "other"), "other\n")
	writeFile(t, filepath.Join(docs, "sub", "inside"), "inside\n")
	return docs
}

// listing is everything under a directory, by slash path, with each file's
// content and each directory and link marked as such.
func listing(t *testing.T, directory string) map[string]string {
	t.Helper()
	found := map[string]string{}
	err := filepath.WalkDir(directory, func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, candidate)
		if err != nil || relative == "." {
			return err
		}
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(candidate)
			if err != nil {
				return err
			}
			found[filepath.ToSlash(relative)] = "link to " + target
		case entry.IsDir():
			found[filepath.ToSlash(relative)] = "directory"
		default:
			content, err := os.ReadFile(candidate)
			if err != nil {
				return err
			}
			found[filepath.ToSlash(relative)] = string(content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", directory, err)
	}
	return found
}

func describeListing(found map[string]string) string {
	var names []string
	for name, content := range found {
		names = append(names, name+"="+strings.TrimSpace(content))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func assertSameListing(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if describeListing(before) != describeListing(after) {
		t.Fatalf("%s changed:\n before: %s\n after:  %s", what, describeListing(before), describeListing(after))
	}
}

// hookOnce runs act the first time a confined change reaches point in
// directory, and returns whether it ran.
func hookOnce(t *testing.T, point Point, directory string, act func(HookEvent)) *bool {
	t.Helper()
	ran := new(bool)
	restore := SetTestHook(func(event HookEvent) {
		if *ran || event.Point != point || event.Directory != directory {
			return
		}
		*ran = true
		act(event)
	})
	t.Cleanup(restore)
	return ran
}

func wantRefusal(t *testing.T, err error, reason Reason) {
	t.Helper()
	var refused *RefusalError
	if !errors.As(err, &refused) || refused.Reason != reason {
		t.Fatalf("error = %v, want a refusal because %s", err, reason)
	}
	if refused.Operation == "" || refused.Root == "" || refused.Path == "" {
		t.Fatalf("the refusal %q does not name its operation, root and path", refused)
	}
}

// A held directory, or an ancestor of it, moved outside the root at either
// side of the change: the change is refused, and whatever it did is undone, so
// the directory that is now outside holds exactly what it held before.
func TestAHeldDirectoryMovedOutsideTheRootIsRefusedAndLeftAsItWas(t *testing.T) {
	for _, row := range []struct {
		name     string
		point    Point
		ancestor bool
	}{
		{"moved between the first check and the change", PointBeforeChange, false},
		{"moved between the change and the second check", PointAfterChange, false},
		{"an ancestor moved before the change", PointBeforeChange, true},
	} {
		for _, writer := range confinedWriters() {
			t.Run(row.name+"/"+writer.name, func(t *testing.T) {
				root, outside := repository(t)
				docs := prepareDocs(t, root)
				before := listing(t, docs)
				moved, from := filepath.Join(outside, "docs"), docs
				if row.ancestor {
					moved, from = filepath.Join(outside, "a"), filepath.Dir(docs)
				}
				ran := hookOnce(t, row.point, docs, func(HookEvent) {
					if err := os.Rename(from, moved); err != nil {
						t.Errorf("move the held directory out: %v", err)
					}
				})
				err := writer.write(t, root)
				if !*ran {
					t.Fatalf("%s never reached %s in %s (error %v)", writer.name, row.point, docs, err)
				}
				wantRefusal(t, err, ReasonMoved)
				after := moved
				if row.ancestor {
					after = filepath.Join(moved, "docs")
				}
				assertSameListing(t, "the directory moved outside the root", before, listing(t, after))
				if left := listing(t, root.Path()); len(left) != 0 && !(row.ancestor == false && len(left) == 1 && left["a"] == "directory") {
					t.Fatalf("the root holds %s after a refused change", describeListing(left))
				}
			})
		}
	}
}

// A new file given a second link from outside the root before it is published
// is not published: the content it carries was written before the link
// appeared, and the name it was for is left as it was.
func TestANewFileLinkedFromOutsideBeforePublicationIsNotPublished(t *testing.T) {
	for _, writer := range confinedWriters() {
		if !strings.Contains(writer.name, "document") && !strings.Contains(writer.name, "record") &&
			writer.name != "cut" && writer.name != "pinned file writer" && writer.name != "pinned create" {
			continue
		}
		t.Run(writer.name, func(t *testing.T) {
			root, outside := repository(t)
			docs := prepareDocs(t, root)
			before := listing(t, docs)
			ran := hookOnce(t, PointBeforePublish, docs, func(HookEvent) {
				entries, err := os.ReadDir(docs)
				if err != nil {
					t.Errorf("read the held directory: %v", err)
					return
				}
				for _, entry := range entries {
					if IsTemporaryFile(entry.Name()) {
						if err := os.Link(filepath.Join(docs, entry.Name()), filepath.Join(outside, "stolen")); err != nil {
							t.Errorf("link the new file from outside: %v", err)
						}
					}
				}
			})
			err := writer.write(t, root)
			if !*ran {
				t.Fatalf("%s never reached publication (error %v)", writer.name, err)
			}
			wantRefusal(t, err, ReasonLinked)
			assertSameListing(t, "the directory written in", before, listing(t, docs))
		})
	}
}

// A file that already has a link from outside the root is replaced or cut by
// publishing a new file, so the outside link keeps exactly what it had.
func TestAFileLinkedFromOutsideIsReplacedWithoutChangingTheOutsideLink(t *testing.T) {
	for _, writer := range confinedWriters() {
		replaces := map[string]bool{"write a document": true, "replace a state record": true, "cut": true, "pinned file writer": true}
		if !replaces[writer.name] {
			continue
		}
		t.Run(writer.name, func(t *testing.T) {
			t.Parallel()
			root, outside := repository(t)
			docs := prepareDocs(t, root)
			kept := filepath.Join(outside, "kept")
			if err := os.Link(filepath.Join(docs, "target"), kept); err != nil {
				t.Fatalf("link the target from outside: %v", err)
			}
			if err := writer.write(t, root); err != nil {
				t.Fatalf("%s error = %v", writer.name, err)
			}
			if content := readFile(t, kept); content != "original\n" {
				t.Fatalf("the outside link reads %q after %s", content, writer.name)
			}
			if content := readFile(t, filepath.Join(docs, "target")); content == "original\n" {
				t.Fatalf("%s left the target as it was", writer.name)
			}
			if left := listing(t, docs); len(left) != 4 {
				t.Fatalf("the directory holds %s after the replacement", describeListing(left))
			}
		})
	}
}

// Appending and locking never touch a file that already has a second link.
func TestAppendingOrLockingAFileWithASecondLinkIsRefused(t *testing.T) {
	for _, writer := range confinedWriters() {
		if writer.name != "append" && writer.name != "open an appender" && writer.name != "lock" && writer.name != "pinned lock" {
			continue
		}
		t.Run(writer.name, func(t *testing.T) {
			t.Parallel()
			root, outside := repository(t)
			docs := prepareDocs(t, root)
			writeFile(t, filepath.Join(docs, "target.lock"), "")
			for _, name := range []string{"target", "target.lock"} {
				if err := os.Link(filepath.Join(docs, name), filepath.Join(outside, name)); err != nil {
					t.Fatalf("link %s from outside: %v", name, err)
				}
			}
			before := listing(t, outside)
			wantRefusal(t, writer.write(t, root), ReasonLinked)
			assertSameListing(t, "outside the root", before, listing(t, outside))
		})
	}
}

// A link added from outside between two append batches stops the append: the
// batch written before the link appeared is what the link sees, and nothing
// after it is written.
func TestALinkAddedBetweenAppendBatchesStopsTheAppend(t *testing.T) {
	root, outside := repository(t)
	docs := prepareDocs(t, root)
	stolen := filepath.Join(outside, "stolen")
	ran := hookOnce(t, PointBetweenBatches, docs, func(HookEvent) {
		if err := os.Link(filepath.Join(docs, "target"), stolen); err != nil {
			t.Errorf("link the log from outside: %v", err)
		}
	})
	content := bytes.Repeat([]byte("x"), 3*appendBatchBytes)
	appender, err := root.OpenAppender("a/docs/target", 0o600, 0o700)
	if err != nil {
		t.Fatalf("OpenAppender() error = %v", err)
	}
	defer appender.Close()
	written, err := appender.Write(content)
	if !*ran {
		t.Fatal("the append never reached a second batch")
	}
	wantRefusal(t, err, ReasonLinked)
	if written != appendBatchBytes {
		t.Fatalf("Write() = %d bytes, want the one batch written before the link appeared", written)
	}
	if got := readFile(t, stolen); got != "original\n"+string(content[:appendBatchBytes]) {
		t.Fatalf("the outside link sees %d bytes, want the original and one batch", len(got))
	}
	if _, err := appender.Write([]byte("more")); err == nil {
		t.Fatal("an append that was stopped accepted another write")
	}
	if got := readFile(t, stolen); len(got) != len("original\n")+appendBatchBytes {
		t.Fatalf("a stopped append still wrote: the outside link sees %d bytes", len(got))
	}
}

// A removal removes only the file it checked: one swapped in at the name
// before the removal is put back and the removal refused.
func TestARemovalTargetSwappedBeforeItIsRemovedIsPutBack(t *testing.T) {
	for _, removal := range []struct {
		name   string
		remove func(Root) error
		dir    bool
	}{
		{"remove a file", func(root Root) error { _, err := root.RemoveFile("a/docs/target"); return err }, false},
		{"remove a directory", func(root Root) error { _, err := root.RemoveDirectory("a/docs/target"); return err }, true},
		{"pinned remove", func(root Root) error {
			pinned, err := OpenPinnedRoot(root.Path())
			if err != nil {
				return err
			}
			defer pinned.Close()
			return pinned.Remove("a/docs/target")
		}, false},
		{"pinned remove everything", func(root Root) error {
			pinned, err := OpenPinnedRoot(root.Path())
			if err != nil {
				return err
			}
			defer pinned.Close()
			return pinned.RemoveAll("a/docs/target")
		}, true},
	} {
		t.Run(removal.name, func(t *testing.T) {
			root, _ := repository(t)
			docs := filepath.Join(root.Path(), "a", "docs")
			if removal.dir {
				writeFile(t, filepath.Join(docs, "target", "inside"), "checked\n")
				writeFile(t, filepath.Join(docs, "other", "inside"), "swapped in\n")
			} else {
				writeFile(t, filepath.Join(docs, "target"), "checked\n")
				writeFile(t, filepath.Join(docs, "other"), "swapped in\n")
			}
			ran := hookOnce(t, PointBeforeRemove, docs, func(HookEvent) {
				if removal.dir {
					if err := os.RemoveAll(filepath.Join(docs, "target")); err != nil {
						t.Errorf("take the checked directory away: %v", err)
					}
				}
				if err := os.Rename(filepath.Join(docs, "other"), filepath.Join(docs, "target")); err != nil {
					t.Errorf("swap another file in: %v", err)
				}
			})
			err := removal.remove(root)
			if !*ran {
				t.Fatalf("the removal never reached the point before it removes (error %v)", err)
			}
			wantRefusal(t, err, ReasonChanged)
			swapped := filepath.Join(docs, "target")
			if removal.dir {
				swapped = filepath.Join(swapped, "inside")
			}
			if content := readFile(t, swapped); content != "swapped in\n" {
				t.Fatalf("what was swapped in reads %q", content)
			}
			if _, err := os.Lstat(filepath.Join(root.Path(), removingDirectory)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("the holding directory was left behind: %v", err)
			}
		})
	}
}

// The lookup rows: a link given as an absolute path is refused even when it
// points inside the root, a link that climbs out is refused, a link at the
// last part of the target is never written through whether or not what it
// names exists, and missing directories under a contained ancestor are made.
func TestLookupsRefuseWhatTheDesignRefusesAndKeepContainedPathsWorking(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name    string
		prepare func(t *testing.T, root Root, outside string)
		path    string
		reason  Reason
	}{
		{"an absolute link pointing inside the root", func(t *testing.T, root Root, _ string) {
			makeDirectory(t, filepath.Join(root.Path(), "elsewhere"))
			link(t, filepath.Join(root.Path(), "elsewhere"), filepath.Join(root.Path(), "docs"))
		}, "docs/target", ReasonAbsoluteLink},
		{"a relative link climbing out", func(t *testing.T, root Root, _ string) {
			link(t, "../outside", filepath.Join(root.Path(), "docs"))
		}, "docs/target", ReasonLinkLeavesRoot},
		{"a link at the target that points at nothing", func(t *testing.T, root Root, _ string) {
			makeDirectory(t, filepath.Join(root.Path(), "docs"))
			link(t, "missing", filepath.Join(root.Path(), "docs", "target"))
		}, "docs/target", ReasonFinalLink},
		{"a link at the target that points inside", func(t *testing.T, root Root, _ string) {
			writeFile(t, filepath.Join(root.Path(), "docs", "real"), "real\n")
			link(t, "real", filepath.Join(root.Path(), "docs", "target"))
		}, "docs/target", ReasonFinalLink},
		{"missing directories under a contained link", func(t *testing.T, root Root, _ string) {
			makeDirectory(t, filepath.Join(root.Path(), "elsewhere"))
			link(t, "elsewhere", filepath.Join(root.Path(), "docs"))
		}, "docs/deep/nested/target", ""},
	} {
		for _, writer := range []struct {
			name  string
			write func(Root, string) error
		}{
			{"write", func(root Root, path string) error { _, err := root.WriteFile(path, []byte("new\n")); return err }},
			{"create", func(root Root, path string) error { _, _, err := root.CreateFile(path, []byte("new\n")); return err }},
			{"append", func(root Root, path string) error { return root.Append(path, []byte("new\n"), 0o600, 0o700) }},
			{"lock", func(root Root, path string) error {
				file, err := root.OpenLock(path, 0o600, 0o700)
				if err == nil {
					file.Close()
				}
				return err
			}},
		} {
			t.Run(row.name+"/"+writer.name, func(t *testing.T) {
				t.Parallel()
				root, outside := repository(t)
				row.prepare(t, root, outside)
				before := listing(t, outside)
				err := writer.write(root, row.path)
				assertSameListing(t, "outside the root", before, listing(t, outside))
				if row.reason == "" {
					if err != nil {
						t.Fatalf("%s refused a contained path: %v", writer.name, err)
					}
					if _, err := os.Stat(filepath.Join(root.Path(), "elsewhere", "deep", "nested", "target")); err != nil {
						t.Fatalf("the contained path was not written where the link leads: %v", err)
					}
					return
				}
				wantRefusal(t, err, row.reason)
			})
		}
	}
}

// A root on a filesystem the design does not name refuses every write rather
// than falling back to one that cannot keep the guarantee.
func TestARootOnAnUnsupportedFilesystemIsRefused(t *testing.T) {
	t.Parallel()
	unsupported := map[string]string{"darwin": "/dev", "linux": "/proc"}[runtime.GOOS]
	if unsupported == "" {
		t.Skipf("no unsupported filesystem is known on %s", runtime.GOOS)
	}
	root, err := NewRoot(unsupported)
	if err != nil {
		t.Fatalf("NewRoot(%s) error = %v", unsupported, err)
	}
	_, err = root.MakeDirectory("yoyodyne-never-made", 0o700)
	wantRefusal(t, err, ReasonFilesystem)
}

// Writers replacing and removing files in one root at once share the root's
// holding directory, which each of them creates when it needs it and takes
// away when it is done. None of them may fail because another took it away.
func TestConcurrentReplacementsAndRemovalsInOneRootAllSucceed(t *testing.T) {
	t.Parallel()
	root, _ := repository(t)
	errs := make(chan error, 16*20)
	done := make(chan struct{})
	for writer := range 16 {
		go func() {
			defer func() { done <- struct{}{} }()
			for round := range 20 {
				name := filepath.Join("records", "writer-"+string(rune('a'+writer))+".json")
				if err := root.ReplaceFile(name, []byte{byte(round)}, 0o600, 0o700); err != nil {
					errs <- err
				}
				if round%5 == 4 {
					if _, err := root.RemoveFile(name); err != nil {
						errs <- err
					}
				}
			}
		}()
	}
	for range 16 {
		<-done
	}
	close(errs)
	for err := range errs {
		t.Errorf("a concurrent write failed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root.Path(), removingDirectory)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the holding directory was left behind: %v", err)
	}
}
