// Package composition is what this repository is made of, held against the
// checks a run actually declares.
//
// The four checks this project declares — `make fmtcheck`, `make test`,
// `make race`, `make vet` — are Go commands. A change made entirely of shell,
// Markdown, Makefile, or workflow YAML passes all four with nothing having read
// a line of it, which is not a gate that happened to be weak: it is a gate that
// never ran. yoyodyne-ifd.156 was exactly that change, and the suites proving
// the release path it touched ran only as separate CI steps, after the run that
// changed it had already been reviewed and integrated.
//
// So the composition is written down. Every file the repository carries belongs
// to a content class here, and every class records either the declared checks
// that exercise it and what those checks do with it, or why nothing does. The
// value is not the sentences: it is that a file no class recognizes fails, so
// the next content class cannot arrive the way shell did — silently, covered by
// nothing, and discovered by whoever it breaks. The class naming a declared
// check the configuration no longer lists fails for the same reason from the
// other side, because coverage removed somewhere else is exactly what leaves a
// claim here still reading true.
//
// What this package cannot do is add a check to the declared list. That list
// lives in `.yoyodyne/config.yaml`, which is the operator's; what it can do is
// make the gates run under `make test`, which is already declared, and hold the
// two to each other.
package composition

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Class is one kind of content this repository carries and what a run's gates
// do with it.
type Class struct {
	// ID names the class in what a failure reports.
	ID string
	// Extensions are the suffixes that recognize this class, lower-cased and
	// with their dot, and Names the whole base names that recognize content
	// carrying no suffix at all.
	Extensions []string
	Names      []string
	// Checks are the declared check entries that exercise this class, written
	// exactly as the configuration writes them. They are compared against that
	// list rather than described, so a check renamed or dropped there fails here
	// instead of leaving this class saying it is covered.
	Checks []string
	// Exercised is what those checks do with this content, in enough detail that
	// somebody can go and find it. Unexercised is why nothing does, for a class
	// no check reaches. Exactly one of the two is filled in.
	Exercised   string
	Unexercised string
}

// ShellClass is the class content with no recognizing suffix or name falls to
// when its first line hands it to a shell. It is named because that is how the
// tools in `bin` and the hooks the tracker installs are shell without saying so
// anywhere a suffix would show it.
const ShellClass = "shell"

// Classes is this repository's composition: what it is made of, and what a run
// does about each part.
//
// An entry is added when the repository grows content the ones here do not
// recognize, which is not a judgement call — the audit below names the file and
// refuses until somebody writes the entry. Deciding whether the new class is
// worth a gate is the judgement, and recording "nothing exercises this, and
// here is why" is a legitimate answer to it: what is not legitimate is the
// class going unwritten, because then nobody was ever asked.
var Classes = []Class{
	{
		ID:         "go",
		Extensions: []string{".go"},
		Checks:     []string{"make fmtcheck", "make test", "make race", "make vet"},
		Exercised:  "compiled and run by `go test`, again under the race detector, vetted, and held to gofmt. This is the class the declared checks were written for and the only one they cover on their own. Read once more by internal/doclink, for the documentation fragments source cites — the anchor `yoyo init` writes into every generated configuration is one of them, and prose is not what carries it.",
	},
	{
		ID:        "go-module",
		Names:     []string{"go.mod", "go.sum"},
		Checks:    []string{"make test", "make vet"},
		Exercised: "read by the toolchain before it does anything else, so a module file that does not parse, names a version that is not there, or fails its checksum stops every Go check above rather than only this one.",
	},
	{
		ID:         "markdown",
		Extensions: []string{".md"},
		Checks:     []string{"make test"},
		Exercised:  "every internal link and `#fragment` resolved by internal/doclink; the goals held to one line each and the governed artifacts held to their frontmatter and their chain by internal/cli's repository tests; the artifact homes held to the coined-term register by internal/terms; CLAUDE.md and AGENTS.md held identical, and free of the section `bd setup` writes, by composition's own repository test.",
	},
	{
		ID:         ShellClass,
		Extensions: []string{".sh"},
		Checks:     []string{"make test"},
		Exercised:  "parsed by a shell in composition's own repository test, so no shell file reaches a reviewer unparsed; executed where it has a suite — the release verb, the notes writer and the release page body, and the install script — each run from a Go test rather than from a CI step after integration, and the adoption walkthrough for the one refusal of it that needs no tracker; and read by internal/doclink for the documentation fragments it cites.",
	},
	{
		ID:        "makefile",
		Names:     []string{"Makefile"},
		Checks:    []string{"make fmtcheck", "make test", "make race", "make vet"},
		Exercised: "every declared check is an invocation of it, so a Makefile that does not parse, or a target one of them names and it no longer has, fails all four before anything else runs.",
	},
	{
		ID:         "yaml",
		Extensions: []string{".yaml", ".yml"},
		Checks:     []string{"make test"},
		Exercised:  "decoded in composition's repository test, and the workflows held to the shape a workflow needs — a trigger, jobs, and each job with a runner and steps — because workflow YAML on a tag trigger otherwise first executes during a real publication. The workflows are held to installing tools at versions this repository names rather than at whatever an upstream released this morning, which is the other way a workflow fails without this repository having changed. The project configuration and the built-in bundle are loaded through internal/config on top of that, and internal/doclink reads all of it for the documentation fragments a comment cites.",
	},
	{
		ID:         "json",
		Extensions: []string{".json"},
		Checks:     []string{"make test"},
		Exercised:  "decoded in composition's repository test. What each file means belongs to the tool that reads it — Claude Code, Codex, the tracker — but a file none of them can parse is this repository's defect whoever owns the schema.",
	},
	{
		ID:         "web-asset",
		Extensions: []string{".html", ".css", ".js"},
		Checks:     []string{"make test"},
		Exercised:  "the dashboard's page, compiled into the binary by internal/dashboard. The HTML is html/template source, parsed when the package initializes, so a template that does not parse fails every test in it; and the package's tests serve each asset and hold the page to its own content-security policy — no inline script, no inline style, nothing from another origin — and hold every value that reaches it to being escaped. The script and the stylesheet are served whole and checked by nothing that reads their syntax: this module vendors no JavaScript or CSS parser, and what a browser makes of them is seen in a browser.",
	},
	{
		ID:         "path-list",
		Extensions: []string{".paths"},
		Checks:     []string{"make test"},
		Exercised:  "the lists of what a path check vouches for, which the harness reads to decide whether a change runs that check. internal/checks reads this repository's own through the same parser the harness uses, so a line that is not a pattern fails there rather than at the check stage of whatever run touches it, and holds the adoption walk's list to covering the README and the program it documents and to leaving out what is neither.",
	},
	{
		ID:          "jsonl",
		Extensions:  []string{".jsonl"},
		Unexercised: "the tracker's derived exports. They are rewritten wholesale by `bd` from a store that is authoritative elsewhere, so a defect in one is fixed in the store rather than in the file, and a gate here would fail on churn nobody authored.",
	},
	{
		ID:          "toml",
		Extensions:  []string{".toml"},
		Unexercised: "Codex's own configuration, which Codex validates when it starts. This module vendors no TOML parser, and taking a dependency to decode one file its owner already reads is more than the coverage is worth.",
	},
	{
		ID:          "image",
		Extensions:  []string{".png"},
		Unexercised: "the Slack avatars and the app icon. What can be wrong with one is how it looks, which no deterministic check reads.",
	},
	{
		ID:          "licence",
		Names:       []string{"LICENSE"},
		Unexercised: "the licence text. It is changed by a person deciding to change it, and there is nothing about it a check could hold.",
	},
	{
		ID:          "git-config",
		Names:       []string{".gitignore"},
		Unexercised: "git's own ignore rules. Git is the only thing that reads them and no check here runs it against them; what a wrong rule does is leave a file tracked or missing, which is what the census below notices.",
	},
}

// Problem is one way this repository and the ledger disagree.
type Problem struct {
	// Path is the file the problem is about, where it is about one, and Class
	// the content class, where it is about the ledger rather than a file.
	Path   string `json:"path,omitempty"`
	Class  string `json:"class,omitempty"`
	Reason string `json:"reason"`
}

func (p Problem) String() string {
	switch {
	case p.Path != "":
		return fmt.Sprintf("%s: %s", p.Path, p.Reason)
	case p.Class != "":
		return fmt.Sprintf("the %q content class %s", p.Class, p.Reason)
	default:
		return p.Reason
	}
}

// skippedDirectories are the directory names the census does not descend into,
// wherever they appear. Fixtures are written to be malformed on purpose — that
// is what makes them prove a checker works — so holding them to the rules real
// content is held to would fail on exactly the files that demonstrate the rules.
// Release archives are build output that happens not to be ignored while a cut
// is in flight.
var skippedDirectories = map[string]bool{"testdata": true, "dist": true}

// Files is the census: every file this repository carries, repository-relative
// and in sorted order.
//
// It is git's own answer rather than a directory walk, and it is the tracked
// files plus the untracked ones git is not ignoring. Both halves matter. A walk
// would find build output, a scratch file somebody left, and whatever a tool
// wrote into an ignored directory, and a gate failing on those is a gate people
// learn to run with a flag. Tracked files alone would miss a run's own new
// files, which are untracked at the moment its checks run — and a run
// introducing a content class nobody has written down is the case this whole
// package exists for.
func Files(root string) ([]string, error) {
	listing := exec.Command("git", "-C", root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	recorded, err := listing.Output()
	if err != nil {
		return nil, fmt.Errorf("list what %s carries: %w", root, err)
	}
	var files []string
	for _, path := range strings.Split(string(recorded), "\x00") {
		if path == "" || skipped(path) {
			continue
		}
		// A file staged and since deleted is listed and is not there. It is not
		// content the repository carries now, and reading it to classify it would
		// fail the whole census over a file nobody has.
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			continue
		}
		files = append(files, path)
	}
	sort.Strings(files)
	return slices.Compact(files), nil
}

func skipped(path string) bool {
	for _, element := range strings.Split(path, "/") {
		if skippedDirectories[element] {
			return true
		}
	}
	return false
}

// Extension is the suffix a path is recognized by, lower-cased and with its
// dot, or empty where it has none.
//
// It is not filepath.Ext, which reads ".gitignore" as an extension of that name
// and would make every dotfile a content class of its own.
func Extension(path string) string {
	base := filepath.Base(path)
	dot := strings.LastIndex(base, ".")
	if dot <= 0 {
		return ""
	}
	return strings.ToLower(base[dot:])
}

// Classify assigns every file to the class that recognizes it and reports the
// ones no class does.
//
// A file is recognized by its suffix, or — where it has none — by its whole
// name, or failing both by a shebang handing it to a shell. The shebang is last
// and is only consulted for content nothing else claimed, so it never overrides
// a name or a suffix; it is here because the shell this repository carries in
// `bin` and under `.beads/hooks` says what it is in its first line and nowhere
// in its name.
func Classify(root string, ledger []Class, files []string) (map[string][]string, []string, error) {
	members := make(map[string][]string, len(ledger))
	var unclassified []string
	for _, path := range files {
		id, err := classOf(root, ledger, path)
		if err != nil {
			return nil, nil, err
		}
		if id == "" {
			unclassified = append(unclassified, path)
			continue
		}
		members[id] = append(members[id], path)
	}
	return members, unclassified, nil
}

// Exercised reports whether the checks a project declares read any of the paths
// a change touched. It is the mechanical line the submission-evidence bar is
// drawn on: a change the checks would read has to carry the developer's own
// record of running one, and a change to content nothing reads does not.
//
// It answers from the ledger above rather than from a list of file extensions
// written beside the bar, so the line moves when this repository's coverage
// moves — a class that gains a check, or loses the last one and has to be
// rewritten as unexercised, changes what is asked of a developer without
// anything here being edited.
//
// Two things make it safe to ask of a project this ledger was not written for,
// and both fail towards asking for evidence rather than excusing it. The ledger
// is consulted at all only where every check it names is one the project
// declares, which is this repository and nothing else; and a path no class
// recognizes, or one that cannot be read, is exercised as far as this is
// concerned. So a project with its own checks is asked for evidence on every
// change, which is the bar this repository held before the leeway existed.
//
// It answers with no error for that last reason. Every way a path could fail to
// be classified — a file the census cannot open, content with no suffix and no
// shebang — already means the same thing here, and a caller that had to decide
// what to do about the error would be deciding the same question twice.
func Exercised(root string, declared []string, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	if !Describes(declared) {
		return true
	}
	for _, path := range paths {
		class, recognized := classOfChanged(root, Classes, path)
		if !recognized || len(class.Checks) > 0 {
			return true
		}
	}
	return false
}

// Describes reports whether this ledger is an account of a project declaring
// these checks. It asks that every check any class names is declared, and not
// the reverse: a project that declares a check no class names has coverage this
// ledger does not describe, which is a claim to weaken nothing rather than a
// reason to distrust the classes.
func Describes(declared []string) bool {
	if len(Classes) == 0 {
		return false
	}
	for _, class := range Classes {
		for _, check := range class.Checks {
			if !slices.Contains(declared, check) {
				return false
			}
		}
	}
	return true
}

// classOfChanged is classOf over a path a change touched, which is not quite the
// same population the census walks: a change deletes files as well as writing
// them, and a file that is not there can still be recognized by its suffix or
// its name. Only the shebang needs the content, so only that is skipped, and a
// path nothing recognized — including one whose content could not be read —
// comes back unrecognized.
func classOfChanged(root string, ledger []Class, path string) (Class, bool) {
	if suffix := Extension(path); suffix != "" {
		for _, class := range ledger {
			if slices.Contains(class.Extensions, suffix) {
				return class, true
			}
		}
	}
	base := filepath.Base(path)
	for _, class := range ledger {
		if slices.Contains(class.Names, base) {
			return class, true
		}
	}
	shell, err := shellShebang(root, path)
	if err != nil || !shell {
		return Class{}, false
	}
	for _, class := range ledger {
		if class.ID == ShellClass {
			return class, true
		}
	}
	return Class{}, false
}

func classOf(root string, ledger []Class, path string) (string, error) {
	if suffix := Extension(path); suffix != "" {
		for _, class := range ledger {
			if slices.Contains(class.Extensions, suffix) {
				return class.ID, nil
			}
		}
	}
	base := filepath.Base(path)
	for _, class := range ledger {
		if slices.Contains(class.Names, base) {
			return class.ID, nil
		}
	}
	shell, err := shellShebang(root, path)
	if err != nil {
		return "", err
	}
	if shell {
		return ShellClass, nil
	}
	return "", nil
}

// shebangBytes bounds what is read looking for one. A shebang is on the first
// line or there is not one, and a file with no newline in it at all is the case
// this stops from being read whole into memory.
const shebangBytes = 512

// shellShebang reports whether a file's first line hands it to a shell.
func shellShebang(root, path string) (bool, error) {
	file, err := os.Open(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()

	header := make([]byte, shebangBytes)
	read, err := file.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	first, _, _ := bytes.Cut(header[:read], []byte("\n"))
	if !bytes.HasPrefix(first, []byte("#!")) {
		return false, nil
	}
	// `#!/bin/sh` names the interpreter in the path and `#!/usr/bin/env bash`
	// names it in the argument, so every field is a candidate rather than only
	// the first.
	for _, field := range strings.Fields(string(first)) {
		switch filepath.Base(field) {
		case "sh", "bash", "dash", "ksh", "zsh":
			return true, nil
		}
	}
	return false, nil
}

// Check holds the ledger to this repository and to the checks a run declares.
//
// Three things can be wrong and each is somebody's to act on. A file no class
// recognizes is content a run's gates say nothing about, which is the failure
// this package is named for. A class that recognizes nothing is the same
// failure read backwards: a ledger still describing content the repository
// stopped carrying is one nobody trusts the rest of. And a class naming a check
// the configuration does not declare is coverage that was removed somewhere
// else, leaving the sentence here the only thing that still says it is there.
//
// Problems come back in a fixed order — unclassified files first, in census
// order, then the classes in ledger order — so two runs over one checkout
// report the same thing the same way.
func Check(root string, ledger []Class, files, declared []string) ([]Problem, error) {
	members, unclassified, err := Classify(root, ledger, files)
	if err != nil {
		return nil, err
	}
	var problems []Problem
	for _, path := range unclassified {
		problems = append(problems, Problem{
			Path:   path,
			Reason: "no content class recognizes it, so nothing here says what a run's checks do with it. Add a class to internal/composition naming the checks that exercise it, or recording why none does.",
		})
	}
	for _, class := range ledger {
		if len(members[class.ID]) == 0 {
			problems = append(problems, Problem{
				Class:  class.ID,
				Reason: "recognizes nothing this repository carries; retire it rather than leaving the ledger describing content that has gone.",
			})
		}
		for _, check := range class.Checks {
			if !slices.Contains(declared, check) {
				problems = append(problems, Problem{
					Class:  class.ID,
					Reason: fmt.Sprintf("says %q exercises it, and the project declares no such check. Its coverage was removed with that check; either declare it again or record here what covers this class now.", check),
				})
			}
		}
	}
	return problems, nil
}
