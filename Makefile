# Build and test the three runtimes of tabnas-render: the Rust crate in
# rs/, which is the reference, and the TypeScript package in ts/ and the
# Go module in go/, which track it. All three run the shared fixtures in
# test/spec/.
#
# Local builds resolve the @tabnas siblings from sibling checkouts: the
# TypeScript side through node_modules symlinks and the Go side through
# the repo-set go.work (both written by admin/scripts/link.sh), and the
# Rust side as path dependencies (rs/Cargo.toml).

.PHONY: all build test clean build-ts build-go build-rs test-ts test-go test-rs \
        clean-ts clean-go clean-rs version-rs tags-go reset bench gate

all: build test

build: build-ts build-go build-rs

test: test-ts test-go test-rs

clean: clean-ts clean-go clean-rs

# --- TypeScript (package in ts/) ---
build-ts:
	cd ts && npm run build

# npm test builds first, then runs dist-test/*.test.js.
test-ts:
	cd ts && npm test

clean-ts:
	rm -rf ts/dist ts/dist-test

# --- Go (module in go/) ---
build-go:
	cd go && go build ./...

test-go:
	cd go && go test -v ./...

clean-go:
	cd go && go clean

# --- Rust (crate in rs/) ---
build-rs:
	cd rs && cargo build --all-targets

# `--all-targets` does NOT include doctests -- cargo documents the
# selector as "Test all targets (does not include doctests)" -- and the
# README's Rust example is one, so both runs are needed.
test-rs:
	cd rs && cargo test --all-targets && cargo test --doc
	cd rs && cargo clippy --all-targets --all-features -- -D warnings

clean-rs:
	cd rs && cargo clean

# Set the Rust crate version: make version-rs V=x.y.z
#
# Moves `version` in rs/Cargo.toml and refreshes the crate's own entry in
# rs/Cargo.lock. VERSION in rs/src/lib.rs is env!("CARGO_PKG_VERSION"),
# so it follows the manifest by itself. The TypeScript and Go sites are
# the rest of the release bump (see AGENTS.md, "Releasing"):
# ts/test/version.test.ts and go/version_test.go fail when either drifts
# from rs/Cargo.toml.
version-rs:
	@test -n "$(V)" || (echo "Usage: make version-rs V=x.y.z" && exit 1)
	sed -i.bak 's/^version = ".*"/version = "$(V)"/' rs/Cargo.toml
	rm -f rs/Cargo.toml.bak
	cd rs && cargo metadata --format-version 1 --offline >/dev/null

# List published Go module tags, newest first.
tags-go:
	git tag -l 'go/v*' --sort=-version:refname

# Reinstall from the registry and rebuild everything. The TypeScript
# reset removes node_modules, so re-run admin/scripts/link.sh afterwards
# to point the @tabnas siblings at their checkouts again.
reset:
	cd ts && npm run reset
	cd go && go clean -cache && go build ./... && go test -v ./...
	cd rs && cargo clean && cargo test --all-targets && cargo test --doc

# criterion; prints a line per group.
bench:
	cd rs && cargo bench

# The full Rust gate CI runs, with the lock discipline.
gate:
	ci/rust/run.sh
