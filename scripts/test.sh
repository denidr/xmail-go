#!/usr/bin/env bash
# xmail dev verification script — runs every check a contributor should
# pass before committing or pushing. Functionally equivalent to
# scripts/test.ps1 (use that one on native Windows without Git Bash/WSL).
#
# Writes nothing to the repo (the optional --coverage step writes the
# gitignored coverage.out). Safe to re-run.
#
# Usage:
#   scripts/test.sh [--race] [--no-js] [--coverage]
#
# Steps, in order (fail-fast):
#   1.  gofmt -l                      formatting drift
#   2.  go mod tidy -diff             go.mod/go.sum drift
#   3.  go vet ./...                  static analysis (default build)
#   4.  go build ./...                default build compiles
#   5.  GOOS=windows build/vet        tray+service compiles (cross-checked,
#                                     so it also runs on Linux/macOS)
#   6.  go test ./...                 unit tests
#   7.  go test -tags integration     in-process fake SMTP/IMAP/POP3
#   8.  go test -tags xmailtray       tray + service tests (Windows host only)
#   9.  node --check                  dashboard JS syntax (if node present)
#   10. go test -race -tags integration   only with --race (needs cgo/gcc)
#   11. coverage report               only with --coverage
#
# See PLAN.md §6 for the testing strategy behind these commands, and
# PLAN.md §6.4 for why -race is opt-in (no cgo/gcc on every dev machine).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

RACE=0
JS=1
COVERAGE=0

usage() {
	cat >&2 <<EOF
Usage: $(basename "$0") [--race] [--no-js] [--coverage]

  --race      also run the race detector (needs cgo/gcc; see PLAN.md §6.4)
  --no-js     skip the dashboard app.js syntax check
  --coverage  also print a per-function coverage report

Runs gofmt, go mod tidy -diff, vet, build, unit tests, integration tests,
the Windows tray cross-compile check, and (on Windows) the winservice
tests. Exits non-zero on the first failure.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
		--race) RACE=1 ;;
		--no-js) JS=0 ;;
		--coverage) COVERAGE=1 ;;
		-h|--help) usage; exit 0 ;;
		*) echo "error: unknown argument '$1' (try --help)" >&2; exit 2 ;;
	esac
	shift
done

step() { echo "" >&2; echo "==> $*" >&2; }
ok()   { echo "    ok" >&2; }
skip() { echo "    skipped: $*" >&2; }

require_cmd() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "error: required command not found: $1" >&2
		exit 1
	fi
}

require_cmd go

step "gofmt -l cmd internal (formatting drift)"
unformatted="$(gofmt -l cmd internal)"
if [ -n "$unformatted" ]; then
	echo "error: not gofmt-clean, run: gofmt -w cmd internal" >&2
	echo "$unformatted" >&2
	exit 1
fi
ok

step "go mod tidy -diff (go.mod/go.sum drift)"
if ! go mod tidy -diff; then
	echo "error: go.mod/go.sum are not tidy, run: go mod tidy" >&2
	exit 1
fi
ok

step "go vet ./..."
go vet ./...
ok

step "go build ./..."
go build ./...
ok

# Cross-compiled on purpose: the tray+service files are gated
# `//go:build windows && xmailtray`, so a plain `go build -tags xmailtray`
# fails off Windows ("build constraints exclude all Go files"). Setting
# GOOS=windows makes this check work on every host.
step "tray+service compile check (GOOS=windows -tags xmailtray)"
GOOS=windows CGO_ENABLED=0 go build -tags xmailtray ./...
GOOS=windows go vet -tags xmailtray ./...
ok

step "go test ./... (unit)"
go test ./...
ok

step "go test -tags integration ./... (in-process fake SMTP/IMAP/POP3)"
go test -tags integration ./...
ok

step "go test -tags xmailtray (Windows host only)"
if [ "$(go env GOOS)" = "windows" ]; then
	go test -tags xmailtray ./internal/winservice/ ./cmd/xmail-tray/
	ok
else
	skip "host GOOS=$(go env GOOS) — the winservice/xmailtray build tags require windows"
fi

step "dashboard JS syntax (node --check)"
if [ "$JS" -eq 1 ]; then
	if command -v node >/dev/null 2>&1; then
		node --check internal/dashboard/assets/app.js
		ok
	else
		skip "node not found"
	fi
else
	skip "--no-js"
fi

step "go test -race -tags integration ./... (race detector)"
if [ "$RACE" -eq 1 ]; then
	go test -race -tags integration ./...
	ok
else
	skip "needs cgo/gcc — re-run with --race (PLAN.md §6.4)"
fi

if [ "$COVERAGE" -eq 1 ]; then
	step "coverage (go test -coverprofile / go tool cover)"
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out
	ok
fi

echo "" >&2
echo "==> all checks passed" >&2
