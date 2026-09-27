.PHONY: build run test test-all test-integration test-race coverage tidy docker-build docker-run \
        release-docker-amd64 release-docker-arm64 release-windows-amd64 release-all

VERSION ?= dev
DOCKER_IMAGE ?= xmail

build:
	go build -o bin/xmail ./cmd/xmail

run:
	go run ./cmd/xmail

# Unit tests only — fast, no network/docker. Must be
# green before any phase is considered done.
test:
	go test ./...

# Everything a dev should pass before committing: gofmt, go mod tidy
# -diff, vet, build, unit + integration tests, and the Windows tray
# cross-compile check. Thin wrapper around scripts/test.sh so the logic
# also works standalone in CI without make — use scripts\test.ps1 on
# Windows without Git Bash/WSL. Flags: bash scripts/test.sh --help
# (--race needs cgo/gcc, --coverage prints a report).
#
# Invoked via `bash` on purpose: the scripts are stored mode 100644 in
# git (same as scripts/release.sh), so relying on the executable bit
# would fail with "Permission denied" on a fresh Linux/macOS checkout.
test-all:
	bash scripts/test.sh

# Integration tests (build tag "integration") — in-process fake SMTP/IMAP/
# POP3 servers, no external docker needed.
test-integration:
	go test -tags integration ./...

# Race detector — required before every Docker/Windows release build
# (API server + MCP server + mailer clients run concurrently).
test-race:
	go test -race -tags integration ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

tidy:
	go mod tidy

docker-build:
	docker build -t $(DOCKER_IMAGE):local .

docker-run:
	docker run --rm -p 5569:5569 -v xmail-data:/app/data --env-file .env $(DOCKER_IMAGE):local

## --- Release builds: 3 target platforms (see ARCHITECTURE.md
## "Build & Release" and scripts/release.sh). These targets are
## thin wrappers — all real logic (artifact packaging, checksums, version
## detection) lives in the script, so it also works standalone in CI without
## make. Artifacts land in dist/, not bin/.

release-docker-amd64:
	scripts/release.sh docker-amd64 $(VERSION)

release-docker-arm64:
	scripts/release.sh docker-arm64 $(VERSION)

release-windows-amd64:
	scripts/release.sh windows-amd64 $(VERSION)

release-all:
	scripts/release.sh all $(VERSION)
