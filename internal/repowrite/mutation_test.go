package repowrite

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestInPlaceMutationsRefuseHardLinkReplacement(t *testing.T) {
	t.Parallel()
	for _, operation := range []struct {
		name   string
		mutate func(Root) error
	}{
		{"append", func(root Root) error {
			file, err := root.OpenAppend("docs/target", 0o600, 0o700)
			if err != nil {
				return err
			}
			_, err = file.WriteString("changed")
			return errors.Join(err, file.Close())
		}},
		{"truncate", func(root Root) error {
			_, err := root.Truncate("docs/target", 0)
			return err
		}},
	} {
		t.Run(operation.name, func(t *testing.T) {
			t.Parallel()
			root, outside := repository(t)
			target := filepath.Join(root.Path(), "docs", "target")
			sentinel := filepath.Join(outside, "sentinel")
			writeFile(t, target, "original")
			writeFile(t, sentinel, "keep")
			before := treeSnapshot(t, outside)
			called := false
			root.beforeMutation = func() {
				called = true
				replaced := make(chan error)
				go func() {
					if err := os.Rename(target, target+"-held"); err != nil {
						replaced <- err
						return
					}
					replaced <- os.Link(sentinel, target)
				}()
				if err := <-replaced; err != nil {
					t.Fatal(err)
				}
			}
			err := operation.mutate(root)
			if !called {
				t.Fatal("mutation did not reach the replacement barrier")
			}
			if err == nil || !strings.Contains(err.Error(), "hard link") {
				t.Errorf("mutation error = %v, want refusal of the shared inode", err)
			}
			if after := treeSnapshot(t, outside); !reflect.DeepEqual(before, after) {
				t.Errorf("mutation escaped through a hard link: before = %v, after = %v", before, after)
			}
			if got := readFile(t, target+"-held"); got != "original" {
				t.Errorf("original inode changed: %q", got)
			}
		})
	}
}

// Independent callers creating one missing parent must all open the same
// append inode. Tracker writes use this descriptor for an advisory lock before
// they append notes, so a failed open loses that writer's update altogether.
func TestConcurrentAppendCreationOpensOneSharedFile(t *testing.T) {
	t.Parallel()
	root, _ := repository(t)
	const writers = 32
	for round := range 32 {
		relative := fmt.Sprintf("logs/%d/shared", round)
		start := make(chan struct{})
		results := make(chan error, writers)
		var finished sync.WaitGroup
		for range writers {
			finished.Add(1)
			go func() {
				defer finished.Done()
				<-start
				caller, err := NewRoot(root.Path())
				var file *os.File
				if err == nil {
					file, err = caller.OpenAppend(relative, 0o600, 0o700)
				}
				if err == nil {
					_, err = file.WriteString("x")
					err = errors.Join(err, file.Close())
				}
				results <- err
			}()
		}
		close(start)
		finished.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("round %d: concurrent append failed: %v", round, err)
			}
		}
		if content := readFile(t, filepath.Join(root.Path(), relative)); len(content) != writers {
			t.Fatalf("round %d: file has %d appended bytes, want %d", round, len(content), writers)
		}
	}
}

func TestConcurrentReadWriteCreationOpensOneSharedFile(t *testing.T) {
	t.Parallel()
	root, _ := repository(t)
	const writers = 32
	for round := range 32 {
		relative := fmt.Sprintf("logs/%d/shared", round)
		start := make(chan struct{})
		results := make(chan struct {
			info fs.FileInfo
			err  error
		}, writers)
		var finished sync.WaitGroup
		for range writers {
			finished.Add(1)
			go func() {
				defer finished.Done()
				<-start
				caller, err := OpenPinnedRoot(root.Path())
				var file *os.File
				if err == nil {
					defer caller.Close()
					file, err = caller.OpenReadWrite(relative)
				}
				var info fs.FileInfo
				if err == nil {
					info, err = file.Stat()
					err = errors.Join(err, file.Close())
				}
				results <- struct {
					info fs.FileInfo
					err  error
				}{info, err}
			}()
		}
		close(start)
		finished.Wait()
		close(results)
		shared, err := os.Stat(filepath.Join(root.Path(), relative))
		if err != nil {
			t.Fatal(err)
		}
		for result := range results {
			if result.err != nil {
				t.Fatalf("round %d: concurrent read-write open failed: %v", round, result.err)
			}
			if !os.SameFile(shared, result.info) {
				t.Fatalf("round %d: callers opened different lock file inodes", round)
			}
		}
	}
}

// Every public mutation is stopped at the former resolve-to-write gap. Replacing
// the root, a parent, or the target there must never change the external tree.
func TestRootMutationsRemainConfinedAfterReplacement(t *testing.T) {
	t.Parallel()
	operations := []struct {
		name      string
		directory bool
		mutate    func(Root) error
	}{
		{"create", false, func(r Root) error { _, _, err := r.CreateFile("docs/target", []byte("changed")); return err }},
		{"replace", false, func(r Root) error { _, err := r.WriteFile("docs/target", []byte("changed")); return err }},
		{"append", false, func(r Root) error {
			file, err := r.OpenAppend("docs/target", 0o600, 0o700)
			if err != nil {
				return err
			}
			_, err = file.WriteString("changed")
			return errors.Join(err, file.Close())
		}},
		{"truncate", false, func(r Root) error { _, err := r.Truncate("docs/target", 0); return err }},
		{"mkdir", true, func(r Root) error { _, err := r.MakeDirectory("docs/target/new", 0o700); return err }},
		{"remove directory", true, func(r Root) error { _, err := r.RemoveDirectory("docs/target"); return err }},
		{"remove file", false, func(r Root) error { _, err := r.RemoveFile("docs/target"); return err }},
	}
	for _, operation := range operations {
		for _, replacement := range []string{"root", "parent", "target"} {
			t.Run(operation.name+"/"+replacement, func(t *testing.T) {
				t.Parallel()
				root, outside := repository(t)
				makeDirectory(t, filepath.Join(root.Path(), "docs"))
				for _, prefix := range []string{root.Path(), outside, filepath.Join(outside, "docs")} {
					if operation.directory {
						writeFile(t, filepath.Join(prefix, "target", "sentinel"), "keep")
					} else {
						writeFile(t, filepath.Join(prefix, "target"), "keep")
					}
				}
				// The normal target belongs under docs, as do root replacements.
				if operation.directory {
					writeFile(t, filepath.Join(root.Path(), "docs", "target", "sentinel"), "keep")
				} else if operation.name != "create" {
					writeFile(t, filepath.Join(root.Path(), "docs", "target"), "keep")
				}
				before := treeSnapshot(t, outside)
				called := false
				root.beforeMutation = func() {
					called = true
					victim, target := root.Path(), outside
					switch replacement {
					case "parent":
						victim = filepath.Join(root.Path(), "docs")
					case "target":
						victim = filepath.Join(root.Path(), "docs", "target")
						target = filepath.Join(outside, "target")
					}
					replaceWhileMutationWaits(t, victim, target)
				}
				// A refusal is fine, but an accepted operation must be equally safe.
				_ = operation.mutate(root)
				if !called {
					t.Fatal("mutation did not reach the replacement barrier")
				}
				if after := treeSnapshot(t, outside); !reflect.DeepEqual(before, after) {
					t.Fatalf("mutation escaped: before = %v, after = %v", before, after)
				}
			})
		}
	}
}

func replaceWhileMutationWaits(t *testing.T, victim, target string) {
	t.Helper()
	t.Cleanup(func() { os.RemoveAll(victim + "-held") })
	replaced := make(chan error)
	go func() {
		if _, err := os.Lstat(victim); err == nil {
			if err := os.Rename(victim, victim+"-held"); err != nil {
				replaced <- err
				return
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			replaced <- err
			return
		}
		replaced <- os.Symlink(target, victim)
	}()
	if err := <-replaced; err != nil {
		t.Fatal(err)
	}
}

func TestPinnedPublicationAndCleanupUseTheHeldParent(t *testing.T) {
	t.Parallel()
	root, outside := repository(t)
	pinned, err := OpenPinnedRoot(root.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer pinned.Close()
	writer, err := pinned.FileWriter("docs/target", 0o644, false)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := os.Rename(filepath.Join(root.Path(), "docs"), filepath.Join(root.Path(), "held")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root.Path(), "docs")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("complete")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, filepath.Join(root.Path(), "held", "target")); content != "complete" {
		t.Fatalf("held target = %q", content)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("publication escaped: %v, %v", entries, err)
	}
	if entries, err := os.ReadDir(filepath.Join(root.Path(), "held")); err != nil || len(entries) != 1 {
		t.Fatalf("temporary file was not cleaned up: %v, %v", entries, err)
	}
}

func TestMutationsPreserveContainedLinksAndRefuseDanglingLinks(t *testing.T) {
	t.Parallel()
	root, _ := repository(t)
	writeFile(t, filepath.Join(root.Path(), "real", "file"), "before")
	link(t, "real", filepath.Join(root.Path(), "alias"))
	if _, _, err := root.CreateFile("alias/new", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := root.MakeDirectory("alias/directory", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Truncate("alias/file", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := root.RemoveFile("alias/new"); err != nil {
		t.Fatal(err)
	}
	if _, err := root.RemoveDirectory("alias/directory"); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, filepath.Join(root.Path(), "real", "file")); content != "be" {
		t.Fatalf("truncated file = %q", content)
	}
	link(t, "missing", filepath.Join(root.Path(), "dangling"))
	if _, err := root.WriteFile("dangling/new", []byte("new")); err == nil {
		t.Fatal("write treated a dangling link as a missing directory")
	}
	if _, err := os.Stat(filepath.Join(root.Path(), "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dangling link target was created: %v", err)
	}
}

func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			result[relative] = "directory"
		} else {
			result[relative] = readFile(t, name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
