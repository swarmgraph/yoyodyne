GO ?= go
BINARY ?= bin/yoyo
DIST ?= dist

# What a build reports as its version. A release passes the tag in; a build
# from a checkout describes itself from git, so a bug report about a local
# binary names a commit rather than only saying "dev".
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# The platforms a release ships prebuilt binaries for. macOS is where Yoyodyne
# is actually used; linux/amd64 is built and run by CI and nothing else. See
# the README's install section, which says so rather than implying parity.
PLATFORMS ?= darwin/arm64 darwin/amd64 linux/amd64

# How long one package's test binary may run. Go's own figure is ten minutes,
# and that is a bound on the machine rather than on the change: this repository
# is checked at the concurrency it develops at -- several runs' suites at once,
# one-minute load averages past fifty on sixteen cores. internal/orchestrator is
# the package that sets the figure, because it is the slowest by far and its
# tests wait on the parallel limit rather than fail when the machine is busy.
#
# The figure is that package's measured worst case with a margin. With a second
# check stage running beside it, the package took 1,439 seconds (24 minutes)
# under `make race` and 870 seconds under `make test`; the harness's own `make
# test` has reached 1,170 seconds. 28 minutes is four minutes over the worst of
# those. docs/diagnoses/yoyodyne-ifd-429-69-orchestrator-test-limit.md has the
# figures and the load they were taken at.
#
# It cannot go higher, because what the bound buys is a dump of every goroutine
# instead of a hang, and that is bought only while go test's timeout ends first.
# A check is given `execution.check_timeout`, 30 minutes and not scaled for
# load (docs/configuration.md), and `make race` spends about a minute compiling
# before the package starts. So a test that genuinely hangs is still reported by
# the binary naming what it waited on, at 28 minutes, rather than killed by the
# harness at 30 with nothing to read. The check stage's own limit is wider still.
#
# The figure is interim: it holds until the package split (yoyodyne-ifd.429.14)
# moves the tests that touch no internals out of internal/orchestrator, and
# should come back down then. A package that outgrows it again is a package to
# split, not a figure to raise past the check's limit.
TEST_TIMEOUT ?= 28m

.PHONY: build test race vet fmt fmtcheck cachecheck check adoption codex-resume dist dist-verify clean-dist release release-notes
.NOTPARALLEL: check

# Every Go command below writes what it compiles to the build cache before it
# compiles anything, so a cache the environment does not grant fails the whole
# gate at setup -- "operation not permitted" on a path in the message and no
# mention of a cache anywhere, which reads as a broken toolchain rather than as
# a directory nobody granted. That is exactly what an agent sandbox looks like
# from in here: it grants writes to the worktree and TMPDIR, and the cache
# defaults under the user's home. The harness sets GOCACHE and explicitly
# admits the assigned cache and scratch directories for Codex developers. This
# warning is also needed in an environment the harness did not make -- an
# interactive agent session, or any other sandbox -- and it names the redirect
# rather than leaving it to be rediscovered.
cachecheck:
	@cache="$$($(GO) env GOCACHE)"; \
	if ! mkdir -p "$$cache" 2>/dev/null || ! touch "$$cache/.yoyodyne-writable" 2>/dev/null; then \
		echo "The Go build cache at $$cache cannot be written, so every Go command here fails at setup." >&2; \
		echo "Point it somewhere this environment grants, such as its temporary directory:" >&2; \
		echo "  export GOCACHE=\"$${TMPDIR:-/tmp}/go-build\"" >&2; \
		echo "docs/developing-yoyo.md says what else this affects." >&2; \
		exit 1; \
	fi; \
	rm -f "$$cache/.yoyodyne-writable"

build: cachecheck
	mkdir -p $(dir $(BINARY))
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/yoyo

# The shipped-documentation gate warns for the length of a margin before it
# fails, and `go test ./...` discards everything a passing test says -- so the
# gate's size line and its warning are printed here, after the suite that
# judges the set, where a person running the checks can read them. The grep
# keeps the one or two lines that matter; the suite above is still the verdict.
test: cachecheck
	$(GO) test -timeout $(TEST_TIMEOUT) ./...
	@$(GO) test -v -run '^TestShippedDocumentationNamesDocumentsThisRepositoryHas$$' ./internal/contextbundle \
		| grep -E 'shipped documentation is|WARNING:'
	@# The dashboard page's only behavioural evidence is its render comparison,
	@# and `go test ./...` prints one line per package -- so a run that compared
	@# every render and a run where the render test skipped for want of Node read
	@# identically as `ok ... internal/dashboard`. That is the silence the render
	@# test's own failure was written to end, and a package line cannot end it.
	@# So the one line that says which of the two happened is printed here.
	@#
	@# It is captured rather than piped, because a pipe would make grep's status
	@# the step's and a failing render test would pass the gate. -count=1 is for
	@# the same reason in the other direction: a cached line is the last run's
	@# answer printed as this run's. The status is held in a name of its own
	@# rather than in `status`, which zsh reserves read-only -- make runs this
	@# under /bin/sh, so that is a trap for whoever copies the step rather than
	@# a defect here, and the way not to leave it is not to write it.
	@render=$$($(GO) test -count=1 -v -run '^TestThePageRendersEverySectionInEveryState$$' ./internal/dashboard 2>&1); \
	rendered=$$?; \
	line=$$(printf '%s\n' "$$render" | grep -E '^--- (PASS|SKIP|FAIL): TestThePageRendersEverySectionInEveryState'); \
	case "$$line" in \
		"--- PASS:"*) printf '%s\n' "$$line" ;; \
		*) printf '%s\n' "$$render" ;; \
	esac; \
	exit $$rendered

# The race suite is the expensive one -- several times the plain suite, and
# under two concurrent runs the check that took a run's check stage past two
# hours on 2026-09-19 -- so it takes the packages it covers as a variable. Left
# alone it is the whole module, which is what a person's `make race` and the
# landing check over an integrated commit want. A run's per-run gate passes it
# the packages its change touches, which the harness works out and hands every
# check as YOYODYNE_CHANGED_GO_PACKAGES:
#
#   checks:
#     - make race RACE_PACKAGES="${YOYODYNE_CHANGED_GO_PACKAGES-./...}"
#   landing_checks:
#     - make race
#
# The shell's unset-only default is the point of that line. The variable is set
# only by the harness's check runner, so the same line run anywhere else -- a
# developer executing the declared checks in its worktree for its probe and its
# evidence, a person running the list by hand -- tests the whole module rather
# than reading an unset variable as "nothing to test" and passing on that. Set
# and empty is different: it is the harness saying the change touches no Go
# package, and the target says so and passes rather than testing the module
# root, which holds no Go files and would fail on that alone. `check` below
# keeps the whole module: it is what a person runs before handing work over, and
# it does not know what the change touches.
RACE_PACKAGES ?= ./...

race: cachecheck
	@if [ -z "$(RACE_PACKAGES)" ]; then \
		echo "race: the change touches no Go package, so there is nothing to run the race detector over"; \
	else \
		echo "$(GO) test -race -timeout $(TEST_TIMEOUT) $(RACE_PACKAGES)"; \
		$(GO) test -race -timeout $(TEST_TIMEOUT) $(RACE_PACKAGES); \
	fi

vet: cachecheck
	$(GO) vet ./...

fmt:
	gofmt -w .

# Formatting is a gate, not a suggestion: gofmt -l exits 0 even when it finds
# unformatted files, so the result has to be inspected rather than trusted.
# Parse and tool failures must also fail the gate; stderr remains visible.
fmtcheck:
	@unformatted=$$(gofmt -l .) || exit $$?; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt needed:"; echo "$$unformatted"; exit 1; \
	fi

check: fmtcheck test race vet

# The README's "Getting started" executed against a throwaway project that is
# neither this one nor written in Go, so that section is verified rather than
# asserted. It is the gate on a change to the README's install or
# getting-started sections, and CI runs it on every change: the `adoption` job
# in .github/workflows/ci.yml installs `bd` and calls this target, so the gate
# is enforced rather than left to somebody remembering it.
#
# It stays out of `check` all the same, because `check` is every change's gate
# and most changes touch nothing this vouches for. Once .yoyodyne/config.yaml
# names it under `path_checks`, the harness runs it as a path check instead,
# for a change touching a path scripts/walk-adoption.paths lists;
# docs/developing-yoyo.md says when.
#
#   make adoption                    every step that needs no provider
#   WALK_PROVIDER=1 make adoption    also hand an item to a developer agent
adoption:
	scripts/walk-adoption.sh

# The Codex native-resume test, run where it can run and required to have run.
# It launches the real Codex, so the harness's checks -- which hide the
# provider CLIs from every other check -- run it only as the path check
# .yoyodyne/config.yaml names with `needs_provider_clis: true`, for a change
# touching a path scripts/codex-resume.paths lists. The test skips where Codex
# is not installed, and inside a developer run, whose sandbox will not let Codex
# apply its own. A skip checked nothing, so it never reads as a pass: this
# target exits non-zero with the line that tells the harness the check could
# not run, carrying the test's own reason, so it spends no repair attempt. Only
# a test that ran and failed is a failure. docs/configuration/runs.md says
# where the test runs.
codex-resume: cachecheck
	@result=$$($(GO) test -count=1 -v -run '^TestNativeResumeReplacesSavedDirectoryGrants$$' ./internal/backend/codex 2>&1); \
	tested=$$?; \
	printf '%s\n' "$$result"; \
	if [ $$tested -ne 0 ]; then exit $$tested; fi; \
	if ! printf '%s\n' "$$result" | grep -q '^--- PASS: TestNativeResumeReplacesSavedDirectoryGrants'; then \
		reason=$$(printf '%s\n' "$$result" | sed -n 's/^ *native_resume_test\.go:[0-9]*: //p' | head -n 1); \
		echo "yoyo check could not run: the Codex native-resume test did not run here, so nothing was checked: $${reason:-the test neither passed nor said why}" >&2; \
		exit 1; \
	fi

clean-dist:
	rm -rf $(DIST)

# One release's binaries. This is the whole of what makes a release, so a later
# release is a rerun of this target rather than a fresh act of judgement: pass
# the tag as VERSION and the archives, their names, and their checksums follow.
# CGO is off and paths are trimmed so the build does not depend on the machine
# it ran on.
dist: clean-dist
	mkdir -p $(DIST)
	@set -e; for platform in $(PLATFORMS); do \
		goos=$${platform%/*}; goarch=$${platform#*/}; \
		stem=yoyo_$(VERSION)_$${goos}_$${goarch}; \
		echo "building $$stem"; \
		mkdir -p $(DIST)/$$stem; \
		GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 $(GO) build -trimpath \
			-ldflags '$(LDFLAGS)' -o $(DIST)/$$stem/yoyo ./cmd/yoyo; \
		tar -czf $(DIST)/$$stem.tar.gz -C $(DIST)/$$stem yoyo; \
		rm -rf $(DIST)/$$stem; \
	done
	@set -e; cd $(DIST); \
	if command -v shasum >/dev/null 2>&1; then \
		shasum -a 256 *.tar.gz > checksums.txt; \
	elif command -v sha256sum >/dev/null 2>&1; then \
		sha256sum *.tar.gz > checksums.txt; \
	else \
		echo "dist needs shasum or sha256sum to write checksums" >&2; exit 1; \
	fi
	@# This path has consumers outside this file: the release workflow publishes
	@# it by name, and `make release` reports the cut's checksums from it. A
	@# rename that missed them would otherwise surface as a published release
	@# with no checksums, or a cut that prints none, so it fails here instead --
	@# in the target CI runs on every change.
	@if [ ! -f $(DIST)/checksums.txt ]; then \
		echo "dist: no $(DIST)/checksums.txt; the checksum step wrote somewhere the release does not read" >&2; \
		exit 1; \
	fi
	@echo "wrote $(DIST)/checksums.txt"

# A release whose binaries do not name the tag they were built from is worse
# than no release, because every report filed against it is unattributable.
# This unpacks the archive for whatever platform it is running on and asks the
# binary what it is, so the check that guards a release is the same one CI runs
# on every change rather than a copy of it that first executes at a tag push.
dist-verify: dist
	@set -e; \
	goos=$$($(GO) env GOOS); goarch=$$($(GO) env GOARCH); \
	stem=yoyo_$(VERSION)_$${goos}_$${goarch}; \
	archive=$(DIST)/$$stem.tar.gz; \
	if [ ! -f "$$archive" ]; then \
		echo "dist-verify: no $$archive; PLATFORMS does not cover $$goos/$$goarch" >&2; \
		exit 1; \
	fi; \
	unpacked=$(DIST)/.verify; \
	rm -rf "$$unpacked"; mkdir -p "$$unpacked"; \
	trap 'rm -rf "$$unpacked"' EXIT; \
	tar -xzf "$$archive" -C "$$unpacked"; \
	reported=$$("$$unpacked/yoyo" version); \
	if [ "$$reported" != "$(VERSION)" ]; then \
		echo "dist-verify: yoyo version reported '$$reported', expected '$(VERSION)'" >&2; \
		exit 1; \
	fi; \
	echo "$$stem reports version $$reported"

# Cutting one release, gate included. `dist` is what a release consists of;
# this is the one invocation around it that makes a daily cadence cheap enough
# to keep and safe enough to trust: the adoption walkthrough and `check` green
# first, then the archives and checksums for the tag, then the tag itself, on
# the commit origin's default branch already holds -- the cut writes nothing to
# that branch. A red gate refuses the cut, names what was red, and writes
# nothing. Publishing stays the operator's own `git push origin <tag>`, which
# the release workflow acts on.
#
# VERSION carries a git-describe default so `build` and `dist` work from a
# checkout, and that default is not a release tag. Pass it on only where
# somebody actually set it, so `make release` with nothing set asks for a tag
# rather than cutting whatever the checkout happens to describe itself as.
release:
	scripts/cut-release.sh $(if $(filter command line environment,$(origin VERSION)),$(VERSION))

# One release's notes, drafted from the work items that landed since the last
# tag and then edited: which work is key functionality, which is an enhancement,
# and which fix is critical enough to go to the top is a judgement the draft
# does not make. `release` above refuses a tag whose notes are missing and
# drafts them for you, so this is for drafting ahead of the cut, or again after
# more work lands. VERSION is withheld the same way, for the same reason.
release-notes:
	bash scripts/release-notes.sh $(if $(filter command line environment,$(origin VERSION)),$(VERSION))
