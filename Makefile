BINARY      := intenter
PKG         := github.com/Vadym903/Intenter
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)
LDFLAGS     := -s -w -X $(PKG)/internal/version.Version=$(VERSION)
BIN_DIR     := bin
CROSS_TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64 windows/arm64 windows/amd64

export CGO_ENABLED := 0

# Go's default per-package test timeout is 10 minutes, and two packages here
# legitimately need longer on a cold CI runner: test/install starts PowerShell
# 5.1 and 7 once per scenario on Windows and runs whole install/upgrade/uninstall
# cycles elsewhere, and test/e2e drives the real binary through a daemon. Both
# have hit the default — on Windows without the race detector and on Linux with
# it — so the timeout was measuring the runner rather than the code. It is still
# a hang detector, just one set above the work.
TEST_TIMEOUT := 30m

.PHONY: all build test test-race lint lint-scripts e2e install-test cross snapshot \
	clean fmt tidy tidy-check docs docs-check demo social help

all: build

## build: compile the binary into bin/
build:
	go build -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) ./cmd/intenter

## test: run the unit and integration tests
test:
	go test ./... -timeout $(TEST_TIMEOUT)

## test-race: run the tests under the race detector (its runtime is C, so this one target needs cgo)
test-race:
	CGO_ENABLED=1 go test -race ./... -timeout $(TEST_TIMEOUT)

## lint: vet and golangci-lint the Go sources
lint:
	go vet ./...
	golangci-lint run

## lint-scripts: check the installer scripts with ShellCheck and PSScriptAnalyzer
lint-scripts:
	shellcheck -s sh install.sh scripts/check-rename.sh
	pwsh -NoProfile -Command "Invoke-ScriptAnalyzer -Path install.ps1 -Severity Warning -EnableExit"

## e2e: run the end-to-end scenarios against a real binary
e2e: build
	go test ./test/e2e/... -count=1 -timeout $(TEST_TIMEOUT)

## install-test: run the hermetic installer tests against a fake release server
install-test:
	go test ./test/install/... -count=1 -timeout $(TEST_TIMEOUT)

## cross: build all six release targets into bin/
cross:
	@for target in $(CROSS_TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "building $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch go build -ldflags '$(LDFLAGS)' \
			-o $(BIN_DIR)/$(BINARY)_$${os}_$${arch}$$ext ./cmd/intenter || exit 1; \
	done

## snapshot: build a local GoReleaser snapshot without publishing (unsigned: the release key lives only in CI)
snapshot:
	goreleaser release --snapshot --clean --skip=sign

## fmt: format the Go sources
fmt:
	gofmt -w cmd internal test tools

## tidy: update go.mod and go.sum
tidy:
	go mod tidy

## tidy-check: fail if go.mod or go.sum is not tidy
tidy-check:
	go mod tidy
	git diff --exit-code go.mod go.sum

## docs: regenerate the CLI reference under docs/cli/
docs:
	go run ./tools/gendocs docs/cli

## docs-check: verify the docs are regenerated, linted, linked and placeholder-free
docs-check: docs
	git diff --exit-code -- docs/cli
	markdownlint-cli2 "README.md" "docs/**/*.md" "CONTRIBUTING.md"
	lychee --offline --no-progress README.md CONTRIBUTING.md CHANGELOG.md \
		SECURITY.md SUPPORT.md CODE_OF_CONDUCT.md llms.txt docs
	scripts/check-readme.sh
	scripts/check-readme_test.sh
	scripts/check-rename.sh
# Exit 2 is "the network was unreachable", which must not fail a local run on a
# train; a genuinely broken badge exits 1 and does fail.
	@scripts/check-badges.sh; status=$$?; \
		[ $$status -eq 0 ] || [ $$status -eq 2 ] || exit $$status

## demo: re-record the README demo GIF from assets/demo/intenter.tape
demo: build
	@command -v vhs >/dev/null 2>&1 || \
		{ echo "vhs is required: https://github.com/charmbracelet/vhs"; exit 1; }
	vhs assets/demo/intenter.tape

## social: render assets/social/preview.png from preview.svg
social:
	@if command -v rsvg-convert >/dev/null 2>&1; then \
		rsvg-convert -w 1280 -h 640 -o assets/social/preview.png assets/social/preview.svg; \
	elif command -v inkscape >/dev/null 2>&1; then \
		inkscape assets/social/preview.svg -w 1280 -h 640 -o assets/social/preview.png; \
	elif command -v magick >/dev/null 2>&1; then \
		magick -background none -density 192 assets/social/preview.svg -resize 1280x640 assets/social/preview.png; \
	else \
		echo "need one of: rsvg-convert (librsvg), inkscape, magick (ImageMagick)"; exit 1; \
	fi
	@ls -l assets/social/preview.png

## clean: remove build output
clean:
	rm -rf $(BIN_DIR) dist

## help: list the available targets
help:
	@echo "Intenter make targets:"
	@grep -E '^## [a-z-]+:' $(MAKEFILE_LIST) | sed 's/^## /  /' | sort
