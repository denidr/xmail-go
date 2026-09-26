.PHONY: build run test test-integration test-race coverage tidy docker-build docker-run \
        release-docker-amd64 release-docker-arm64 release-windows-amd64 release-all

VERSION ?= dev
DOCKER_IMAGE ?= xmail

build:
	go build -o bin/xmail ./cmd/xmail

run:
	go run ./cmd/xmail

# Unit tests only — fast, no network/docker (see PLAN.md §6.1). Must be
# green before any phase in PLAN.md §5 is considered done.
test:
	go test ./...

# Integration tests (build tag "integration") — in-process fake SMTP/IMAP/
# POP3 servers, no external docker needed. See PLAN.md §6.2.
test-integration:
	go test -tags integration ./...

# Race detector — required before every Docker/Windows release build
# (API server + MCP server + mailer clients run concurrently). See
# PLAN.md §6.4.
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
	docker run --rm -p 8080:8080 -v xmail-data:/app/data --env-file .env $(DOCKER_IMAGE):local

## --- Release builds: 3 target platforms (see PLAN.md "Release Build" and
## scripts/release.sh, ARCHITECTURE.md "Build & Release"). These targets are
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
