# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

xmail: an email-as-a-service backend in Go. Multi-account SMTP send / IMAP+POP3 fetch, exposed identically over a REST API and an MCP server, credentials encrypted at rest in SQLite. Ships as three release targets from one codebase: Docker x64, Docker Armbian (arm64), Windows x64 (tray + Windows Service).

**Read `ARCHITECTURE.md` before making non-trivial changes** — it is the authoritative, up-to-date technical reference (file-by-file layout, request lifecycles, data model, and step-by-step recipes for the change you're about to make: new REST endpoint, new MCP tool, new mailer protocol, new account field, new migration). It explicitly asks agents to read it first, and to trust the code over the doc if they ever disagree. `PLAN.md` has the phase-by-phase implementation history and design-decision log; `PRD.MD` has product requirements/scope.

## Commands

```bash
go mod tidy
make run                              # or: go run ./cmd/xmail

make test                             # unit tests only — fast, no network/docker
make test-integration                 # + integration tests (build tag "integration"): in-process fake SMTP/IMAP/POP3 servers
make test-race                        # + -race, required before any Docker/Windows release build
make coverage                         # go tool cover report
make test-all                         # ALL dev checks (gofmt, tidy -diff, vet, build, unit+integration, tray cross-compile)

go test ./...                                    # equivalent to make test
go test -tags integration ./...                  # equivalent to make test-integration
go test -race -tags integration ./...            # requires cgo/gcc — not available on every dev machine
go test ./internal/account/ -run TestService_FetchMessages  # single test (package account)

go build -tags xmailtray ./...        # also compile Windows-tray-only files
go vet -tags xmailtray ./...

make docker-build && make docker-run  # local Docker dev loop

scripts/release.sh <docker-amd64|docker-arm64|windows-amd64|all> [version]   # Git Bash/WSL/Linux/macOS/CI
scripts\release.ps1 -Target <...> [-Version <...>]                          # native PowerShell, no Git Bash/WSL needed

bash scripts/test.sh [--race] [--coverage]   # full dev gate — run before committing (see README "Cek lengkap sebelum commit")
scripts\test.ps1 [-Race] [-Coverage]         # same, native PowerShell
```

No test framework beyond stdlib `testing` + table-driven tests (deliberate, minimal-dependency choice — don't add testify).

## Architecture (see ARCHITECTURE.md §1 for full detail)

Five rules drive almost every structural decision:

1. **One behavior, three entrypoints.** `internal/app.Run(ctx, cfg)` is the only place that wires storage → account service → mailer implementations → API server → MCP server. `cmd/xmail` (headless) and `cmd/xmail-tray` (Windows) both just call it. Never put business logic in `cmd/`.
2. **Protocol logic is hidden behind interfaces** (`internal/mailer/types.go`: `Sender`, `Fetcher`, `Checker`, `Marker`, `FetcherChecker`, `FolderLister`), implemented in `internal/mailer/{smtp,imap,pop3}` and dispatched by `account.Service`. Nothing outside `internal/mailer/*` and `internal/app` imports a concrete protocol package.
3. **REST and MCP are two thin adapters over the same `account.Service`** (`internal/api`, `internal/mcpserver`). There is exactly one implementation of each business operation (send/fetch/check/list folders) — if REST and MCP disagree, the bug is in one of the adapters, never in `Service`.
4. **Domain models never leak secrets across a boundary.** `account.Account` has no plaintext password field; credentials live only in the encrypted `credentials` table via `Repository.Secret`. `internal/api/dto.go` and `mcpserver`'s `accountSummary` use separate request/response shapes from the domain model specifically to prevent a stray struct-literal typo from serializing a password.
5. **The dashboard is a static client, not a third backend skin.** `internal/dashboard` embeds its HTML/CSS/JS and calls the same REST endpoints from the same origin — no `account.Service` access, no business logic, no privileged route. It is mounted outside the API-key middleware (`isAPIPath` in `internal/api/server.go`) because a page's asset requests cannot carry a header and hold no secrets; every API endpoint stays behind auth (ADR 0002, PRD §6.7).

Build tag note: `cmd/xmail-tray/*.go` and `internal/winservice/*.go` are gated `//go:build windows && xmailtray` (a custom tag, not just `windows`), so plain `go build ./...` — including on Windows — never needs the tray/service-manager dependencies (`fyne.io/systray`, `kardianos/service`). Only `-tags xmailtray` pulls them in.

## Known gaps (don't assume these are done — see ARCHITECTURE.md §8)

- Windows Service installation (`svc.Install()`) has never been executed (requires Administrator; intentionally not automated).
- `go test -race` has never been run in any environment here (no cgo toolchain available in dev).
- SMTP TLS/STARTTLS integration coverage is plaintext-only; `tls`/`starttls` modes are unit-tested for option-mapping only.
- No automated test asserts REST and MCP return identical data for the same account (true by construction, not regression-tested).
- The `api_keys` table exists in the schema but is unused; auth is currently a single static `XMAIL_API_KEY` compared via `crypto/subtle.ConstantTimeCompare`.

## Required environment variables

`XMAIL_ENCRYPTION_KEY` (base64 of 32 random bytes, `openssl rand -base64 32`) and `XMAIL_API_KEY` are required. `XMAIL_LISTEN_ADDR` (default `:5569`), `XMAIL_DB_PATH` (default `xmail.db`), `XMAIL_MCP_STDIO` are optional. See `.env.example`.

## Agent skills

### Issue tracker

Issues and specs live as local markdown files under `.scratch/<feature>/`. See `docs/agents/issue-tracker.md`.

### Domain docs

Single-context: one `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.
