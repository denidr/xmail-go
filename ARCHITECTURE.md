# xmail — Architecture & Extension Reference

This document explains **what exists in this codebase, how it fits together, and how to add to it.** It is written for two audiences equally: a human contributor and an LLM coding agent picking up this repo cold. It is *not* an installation/usage guide — for that, see [README.MD](./README.MD). For product requirements see [PRD.MD](./PRD.MD); for the phase-by-phase implementation history and design-decision log see [PLAN.md](./PLAN.md) (its §10 specifically logs how every finding in [CODE_REVIEW.md](./CODE_REVIEW.md), an independent audit against these three documents, was resolved).

If you're an agent about to modify this repo: read §1 (Core Design Principles) and the relevant subsection of §5 (How to Extend) before writing code. Everything here reflects the actual current state of the code, not aspirational design — if something here looks wrong, trust the code and fix this doc.

---

## 1. Core Design Principles

These four rules explain almost every structural decision in the codebase. Understand them before changing anything.

1. **One behavior, three entrypoints.** `internal/app.Run(ctx, cfg) error` is the *only* place that wires storage → account service → mailer implementations → API server → MCP server and starts serving. `cmd/xmail/main.go` (headless/Docker) and `cmd/xmail-tray/main.go` (Windows tray+service) both just call `app.Run`. Never put business logic in a `cmd/` package — it won't be shared across release targets.

2. **Protocol logic is hidden behind interfaces, dispatched by `account.Service`.** `internal/mailer/types.go` defines `Sender`, `Fetcher`, `Checker`, `FetcherChecker`. `internal/mailer/{smtp,imap,pop3}` implement them. Nothing outside `internal/mailer/*` and `internal/app` ever imports a concrete protocol package — `internal/api` and `internal/mcpserver` only ever call `account.Service` methods. Adding a 4th protocol means: implement the interface, add a `*Factory` type + setter to `Service`, wire it in `internal/app.wireMailer`. Nothing else changes.

3. **REST and MCP are two skins over the same `account.Service`.** There is exactly one business-logic implementation of "send an email" / "fetch messages" / "check for new mail": `account.Service.Send` / `.FetchMessages` / `.CheckNew`. `internal/api`'s HTTP handlers and `internal/mcpserver`'s tool handlers are both thin adapters that parse their respective input format, call the same `Service` method, and format the response. If REST and MCP ever return different data for the same account, that's a bug in one of the two thin adapters, never in `Service`.

4. **Domain models never leak secrets or internal detail across a boundary.** `account.Account` (domain) never has a plaintext password field — credentials live only in the `credentials` SQL table, encrypted, accessed via `Repository.Secret`. `internal/api/dto.go` defines separate request/response JSON shapes so a stray struct-literal typo can't accidentally serialize a password into an HTTP response (this is enforced by a test, see `TestAccountsCRUD_EndToEnd` in `internal/api/server_test.go`). `internal/mcpserver`'s `accountSummary` type does the same for MCP.

---

## 2. Repository Layout

Every `.go` and `.sql` file in the repo, with its one-line purpose. Generated/vendor files (`go.sum`, `dist/`) are omitted.

```
cmd/
  xmail/main.go              headless entrypoint (Docker x64 + Docker Armbian/arm64) — loads config, calls app.Run, handles OS signal shutdown
  xmail-tray/main.go         Windows entrypoint (build tag: windows,xmailtray) — system tray UI or, if launched by Windows SCM, hands off to winservice
  xmail-tray/icon.ico        embedded tray icon (copy of assets/icon.ico; go:embed requires the file inside the package dir)

internal/
  app/app.go                 Run(ctx, cfg) — THE single wiring point (see §1 rule 1). Also wireMailer(svc), which plugs smtp/imap/pop3 implementations into account.Service.

  config/config.go           Config struct + Load() — best-effort loads a .env file (github.com/joho/godotenv, dev convenience, never overrides a real env var), then reads/validates all XMAIL_* env vars
  config/config_test.go

  storage/sqlite.go          Open(path) — opens modernc.org/sqlite DB, runs embedded migrations idempotently (schema_migrations table)
  storage/sqlite_test.go
  storage/migrations/0001_init.sql   accounts, credentials, messages_cache, api_keys tables + indexes
  storage/migrations/0002_message_attachments.sql   adds messages_cache.attachments (JSON filename list, PRD.MD §6.3)
  storage/migrations/0003_message_sort_rank.sql      adds messages_cache.sort_rank (explicit newest-first order, fixes a real cache-ordering bug — see PLAN.md §10.5 #15)

  cryptox/secretbox.go       Encrypt/Decrypt(key, data) — AES-256-GCM, used only for the `credentials.encrypted_secret` column
  cryptox/secretbox_test.go

  account/model.go           Account, ConnectionConfig, TLSMode domain types (no credential field on Account)
  account/repository.go      Repository — SQL CRUD for accounts + credentials (encrypts/decrypts via cryptox)
  account/messages.go        Repository methods for the messages_cache table: UpsertMessages, ListMessages, ExistingUIDs, MarkMessageRead
  account/service.go         Service — validation + protocol dispatch (see §1 rule 2). THE business logic layer.
  account/repository_test.go, messages_test.go, service_test.go

  mailer/types.go            Sender, Fetcher, Checker, Marker, FetcherChecker interfaces; OutgoingMessage, Message, Attachment structs; WindowRange + DefaultFolder helpers
  mailer/smtp/client.go      Sender impl via github.com/wneessen/go-mail
  mailer/smtp/client_test.go, integration_test.go (build tag: integration)
  mailer/imap/client.go      FetcherChecker + Marker impl via github.com/emersion/go-imap/v2 (Fetch also pulls BODYSTRUCTURE for attachment names)
  mailer/imap/integration_test.go (build tag: integration)
  mailer/pop3/client.go      Fetcher impl via github.com/knadh/go-pop3 (no Checker/Marker — POP3 has no unseen-flag or per-message-flag concept)
  mailer/pop3/integration_test.go (build tag: integration)

  api/server.go               Server — http.ServeMux + route table + Handler() (logging+auth middleware chain)
  api/middleware.go           apiKeyAuth (constant-time compare, /healthz exempt), requestLogger (method/path/status/duration only — never bodies)
  api/response.go             writeData/writeError — {data,error} JSON envelope
  api/dto.go                  Request/response JSON shapes, separate from account.Account (see §1 rule 4)
  api/accounts_handler.go     POST/GET/PUT/DELETE /accounts, POST /accounts/{id}/test-connection
  api/send_handler.go         POST /accounts/{id}/send
  api/messages_handler.go     GET /accounts/{id}/messages (cache-first, ?refresh=true forces live), POST /accounts/{id}/check, POST /accounts/{id}/messages/read
  api/server_test.go          httptest-based contract tests (real Service + real sqlite temp file, not mocked)

  mcpserver/server.go         Server — wraps mark3labs/mcp-go, registers 4 tools, all calling the same account.Service methods as internal/api
  mcpserver/server_test.go

  winservice/service.go       (build tag: windows,xmailtray) kardianos/service wrapper — Install/Uninstall/Start/Stop as a native Windows Service. buildServiceConfig sets EnvVars (see PLAN.md §10.6 #19 — without this an installed service could never start, SCM launches it in a fresh empty environment)
  winservice/service_test.go  (same build tag) round-trips buildServiceConfig's EnvVars back through config.Load to prove #19's fix, without needing an actual Administrator-only service install

scripts/release.sh            build script for all 3 release targets → dist/ + SHA256SUMS.txt (Git Bash/WSL/Linux/macOS/CI — see §6)
scripts/release.ps1           same, native PowerShell (no Git Bash/WSL needed — for a bare Windows machine)
assets/icon.ico                source tray icon (placeholder; copied into cmd/xmail-tray/icon.ico for go:embed)

Dockerfile, .dockerignore      multi-stage build → gcr.io/distroless/static-debian12:nonroot
Makefile                       thin wrappers around go test/build and scripts/release.sh
go.mod, go.sum
PRD.MD, PLAN.md, README.MD, ARCHITECTURE.md (this file)
```

---

## 3. Request Lifecycles (concrete, file-by-file)

### 3.1 `POST /accounts/{id}/send` (REST)

1. `internal/api/server.go` routes to `handleSend` (`internal/api/send_handler.go`).
2. Handler decodes JSON into `sendRequest`, validates `To` is non-empty, converts to `mailer.OutgoingMessage` via `sendRequest.toDomain()` (base64-decodes attachments).
3. Calls `s.service.Send(ctx, accountID, msg)`.
4. `account.Service.Send` (`internal/account/service.go`): loads the `Account` + decrypted secret via `Repository`, checks `a.SMTP != nil`, calls the injected `SMTPSenderFactory` (set in `internal/app.wireMailer` to `smtp.New(cfg, fromAddress, username, secret)`), then `.Send(ctx, msg)` on that.
5. `smtp.Client.Send` (`internal/mailer/smtp/client.go`) maps `TLSMode` → `go-mail` TLS options, builds a `*gomail.Msg`, calls `DialAndSendWithContext`.
6. Handler writes `{"status":"sent"}` via `writeData`, or maps the error via `writeErrFor` (→ 400 for `account.ErrValidation`, 404 for `account.ErrNotFound`, 500 otherwise).

### 3.2 `send_email` (MCP)

Identical to 3.1 from step 4 onward. `internal/mcpserver/server.go`'s `handleSendEmail` parses `sendEmailArgs` (bound automatically from the tool call's JSON arguments by `mcp.NewTypedToolHandler`), validates `AccountID`/`To`, builds the same `mailer.OutgoingMessage`, and calls `s.service.Send` — the exact same method REST calls. Errors become `mcp.NewToolResultErrorFromErr(...)` with `IsError: true` instead of an HTTP status code, but the underlying failure mode is identical.

### 3.3 `GET /accounts/{id}/messages` and `check_new_emails`/`POST /check`

Both funnel into `account.Service.FetchMessages` / `.CheckNew` (`internal/account/service.go`). `resolveFetcher` is the single dispatch point that picks `imapFactory` or `pop3Factory` based on the `protocol` string, defaulting empty/unspecified to `account.DefaultProtocol` and **returning that resolved value** to the caller (`(fetcher, resolvedProtocol, err)`) — callers use it for their cache keys instead of re-implementing the same default themselves (a prior version had each caller re-guard `protocol == ""` independently, which both duplicated logic and, once, caused the guard to disagree with what `resolveFetcher` actually built — see PLAN.md §10.5 #17). `FetchMessages` is **cache-first**: unless `refresh` is true, it calls `Repository.ListMessages` first and only dials the mail server (then upserts the cache) if that comes back empty — this is what makes a repeated fetch for the same mailbox window not re-dial every time. `CheckNew` always dials: (a) if the resolved fetcher also implements `mailer.Checker` (only IMAP does), calls `.Check()` for the server-reported unread count; (b) does a `Fetch` of the most recent `defaultCheckFetchLimit` (50) messages and diffs their UIDs against `Repository.ExistingUIDs` to compute "new since last check" — this is **not** a protocol-level concept, it's computed from `messages_cache`. See PLAN.md §10 (CODE_REVIEW.md resolution) for why `FetchMessages` used to always dial live (a real bug: the cache was write-only).

Marking a message read (`POST /accounts/{id}/messages/read`) goes through `Service.MarkRead`, which type-asserts the resolved fetcher to `mailer.Marker` — only IMAP satisfies it (POP3 has no per-message flag concept), so calling it with `protocol=pop3` returns a validation error.

### 3.4 Account CRUD

`internal/api/accounts_handler.go` ↔ `account.Service.{Create,Get,List,Update,Delete}` ↔ `account.Repository` (SQL). `Create` calls `Validate(a, true, secret)`; `Update` calls `Validate(a, secret != nil, secretValue)` — `requireSecret` decouples "was a new password supplied at all" from "what is it", so `Update` with a nil secret pointer doesn't need any placeholder value to pass validation (see PLAN.md §10.4 #13 for the bug this replaced). Required fields, at least one protocol configured, valid `TLSMode` per configured protocol (including the POP3-specific rule: `starttls` is rejected for POP3, since `mailer/pop3` can't do it — PLAN.md §10.1 #2). **`Update` is a full replace, not a JSON merge-patch**: omitted `smtp`/`imap`/`pop3` fields are written as SQL NULL, i.e. removed. `password` is the only field where `nil` means "leave unchanged" (see `accountRequest.Password *string` in `internal/api/dto.go`). `DELETE` returns `200 {"data":{"deleted":true}}`, not a bare `204` — every endpoint uses the same envelope, no exceptions.

---

## 4. Data Model

`internal/storage/migrations/0001_init.sql` is the single source of truth; summary:

| Table | Purpose | Notes |
|---|---|---|
| `accounts` | One row per configured mailbox | `smtp_host`/`imap_host`/`pop3_host` etc. are nullable — a protocol is "not configured" iff its `*_host` is NULL |
| `credentials` | `account_id` → `encrypted_secret` + `nonce` | 1:1 with `accounts` (`ON DELETE CASCADE`), AES-256-GCM via `internal/cryptox` |
| `messages_cache` | Locally cached message metadata (never bodies), **actually read from** by `FetchMessages` (see §3.3) | `UNIQUE(account_id, protocol, folder, uid)` — this is the upsert key everywhere; `attachments` is a JSON filename array (migration `0002`); `sort_rank` is an explicit per-batch newest-first order set by `UpsertMessages` (migration `0003`) — **do not** re-derive display order from `fetched_at`/rowid, that was the bug in §8 |
| `api_keys` | Schema exists, **not used yet** — this is intentional, not a bug (PLAN.md §10.3) | Auth is currently a single static key from `XMAIL_API_KEY` (see §5.6 to change this) |
| `schema_migrations` | Migration tracking, auto-created by `storage.Open` | Not in the `.sql` file — created in code (`internal/storage/sqlite.go`) |

Migrations are plain `.sql` files under `internal/storage/migrations/`, embedded via `go:embed`, applied in filename order, each tracked by filename in `schema_migrations` (idempotent — safe to call `storage.Open` on an already-migrated DB). To add a migration: create `0002_whatever.sql` in that directory. No code changes needed — `storage.Open`'s `migrate()` picks it up automatically.

---

## 5. How to Extend

Concrete recipes for the changes an agent is most likely to be asked to make.

### 5.1 Add a new REST endpoint

1. Add the route in `internal/api/server.go`'s `routes()` (Go 1.22+ `ServeMux` pattern syntax, e.g. `"POST /accounts/{id}/whatever"`).
2. Add the handler function — put it in the most relevant existing `*_handler.go`, or a new file if it's a new resource. Use `decodeJSON[T](r)` for request bodies, `writeData`/`writeError` (or `writeErrFor(w, err)` for domain errors) for responses (`internal/api/response.go`, `internal/api/accounts_handler.go`).
3. If it needs new business logic, add a method to `account.Service` (`internal/account/service.go`) — the handler should be a thin adapter, not contain logic.
4. Add an `httptest`-based test in `internal/api/server_test.go` following the existing pattern (`newTestServer(t)` builds a real `Server` over a real temp-file SQLite DB — not mocked, see PLAN.md §6.1/§6.3).

### 5.2 Add a new MCP tool

1. Define an `xxxArgs struct` with `json` tags in `internal/mcpserver/server.go`.
2. Register it in `registerTools()`: `s.mcp.AddTool(mcp.NewTool("tool_name", mcp.WithDescription(...), mcp.WithString(...)/mcp.WithArray(...)/mcp.WithNumber(...) for each field), mcp.NewTypedToolHandler(s.handleXxx))`. Declare the schema explicitly (don't use `mcp.WithInputSchema[T]()` — see PLAN.md Fase 5 notes for why this codebase avoids it).
3. Write `handleXxx(ctx context.Context, req mcp.CallToolRequest, args xxxArgs) (*mcp.CallToolResult, error)` — validate required args manually (return `mcp.NewToolResultError("...")`, not a Go `error`, for user-facing validation failures — see existing handlers), call the matching `account.Service` method, return `mcp.NewToolResultStructuredOnly(result)`.
4. Add a unit test in `internal/mcpserver/server_test.go` calling the handler function directly (not through the MCP transport — see existing tests for the mock pattern). If the tool takes arguments, also manually verify the JSON-RPC binding once via a live `curl` against `/mcp` (see PLAN.md Fase 8 crosscheck notes for why this matters — `mcp.NewTypedToolHandler`'s `BindArguments` call is a real code path that direct-call unit tests skip).

### 5.3 Add a new mailer protocol (e.g. JMAP)

1. Create `internal/mailer/jmap/client.go`. Implement whichever of `mailer.Sender`/`Fetcher`/`Checker` make sense (see `internal/mailer/types.go`) — follow `internal/mailer/imap/client.go` as the fullest example (implements both `Fetcher` and `Checker`).
2. Add a `*Factory` type + `Set*Factory`/`Set*Tester` setter to `account.Service` (`internal/account/service.go`), following the `IMAPFactory`/`SetIMAPFactory` pattern exactly.
3. Wire it in `internal/app.wireMailer` (`internal/app/app.go`).
4. If it needs its own `ConnectionConfig`-like fields on `Account`, extend `internal/account/model.go` and the `accounts` table (new migration, §4) and `internal/api/dto.go`.
5. Write unit tests (TLS-mode mapping, no network) + an integration test with a fake/in-process server (build tag `integration` — see `internal/mailer/{smtp,imap,pop3}/integration_test.go` for three different fake-server strategies depending on library availability).

### 5.4 Add a new account field / config option

- **Account field** (e.g. a display color): add to `account.Account` (`internal/account/model.go`), the `accounts` table (new migration), `Repository.{insertAccount,scanAccount,Update}` (`internal/account/repository.go`), and `internal/api/dto.go`'s request/response structs.
- **Runtime config** (e.g. a new env var): add to `config.Config` + parsing/validation in `Load()` (`internal/config/config.go`), following the existing required-vs-optional pattern (`XMAIL_API_KEY`/`XMAIL_ENCRYPTION_KEY` are required with clear errors; `XMAIL_LISTEN_ADDR`/`XMAIL_DB_PATH` have defaults via `getEnvOr`).

### 5.5 Add a database migration

Drop a new `NNNN_description.sql` file into `internal/storage/migrations/` (next sequential number, zero-padded 4 digits). No Go code changes needed — `storage.Open`'s embedded-FS migration runner (`internal/storage/sqlite.go`) picks it up automatically, applies it once, tracks it in `schema_migrations`.

### 5.6 Multi-key / rotatable API auth (currently backlog)

The `api_keys` table exists in the schema but is unused — auth is currently one static key compared via `crypto/subtle.ConstantTimeCompare` against `XMAIL_API_KEY` (`internal/api/middleware.go`'s `apiKeyAuth`). To implement real multi-key auth: add `account`-style CRUD for `api_keys` (hash on write, `sha256(key)` lookup on request), swap `apiKeyAuth`'s single-string comparison for a DB lookup. This is tracked as backlog in PLAN.md Fase 9.

---

## 6. Build & Release

Three release targets share one codebase and one `internal/app.Run` (see §1 rule 1) — the only difference is the entrypoint and packaging:

| Target | `GOOS/GOARCH` | Entry point | Build tag |
|---|---|---|---|
| Docker x64 | `linux/amd64` | `cmd/xmail` | (none) |
| Docker Armbian | `linux/arm64` | `cmd/xmail` | (none) |
| Windows x64 | `windows/amd64` | `cmd/xmail-tray` | `xmailtray` (required — see below) |

**Two equivalent release scripts** — same targets, same flags, same `dist/` output, pick whichever matches your shell:

| Script | Run from | Notes |
|---|---|---|
| `scripts/release.sh` | Git Bash, WSL, Linux, macOS, CI | Docker artifact is gzip'd (`*-docker.tar.gz`) |
| `scripts/release.ps1` | native Windows PowerShell (no Git Bash/WSL needed) | Docker artifact is plain, uncompressed (`*-docker.tar`) — avoids depending on an external `gzip` binary on a bare Windows install; both forms load fine with `docker load` |

```
scripts/release.sh   <docker-amd64|docker-arm64|windows-amd64|all> [version]
scripts/release.ps1  -Target <docker-amd64|docker-arm64|windows-amd64|all> [-Version <string>]
```

Both auto-detect the version from `git describe --tags --always --dirty` (override with a positional arg / `-Version`, or the `VERSION` env var), write to `dist/`, and end with a `dist/SHA256SUMS.txt` covering everything produced. `make release-*` targets are one-line wrappers around `release.sh` (so they need `make`, which — like `release.sh` itself — isn't native to plain Windows; use `release.ps1` directly there instead). **The scripts are the implementation** — keep new release logic in them, not in the Makefile, so builds also work standalone in CI without `make`. Both were verified by actually running them end-to-end (build → checksum-verify → execute the resulting binary) — see PLAN.md Fase 8 notes.

**Why the `xmailtray` build tag exists**: `cmd/xmail-tray/*.go` and `internal/winservice/*.go` are marked `//go:build windows && xmailtray` — a *custom* tag, not just `windows`. This means plain `go build ./...` skips them on every platform, including on a Windows dev machine, so the Docker build never needs `fyne.io/systray`/`kardianos/service` (GUI/service-manager dependencies) at all. The tag is only set explicitly via `-tags xmailtray`, which `scripts/release.sh windows-amd64` does for you.

**Version stamping**: `cmd/xmail` and `cmd/xmail-tray` each declare `var version = "dev"`, overridden at build time via `-ldflags "-X main.version=$VERSION"` (done automatically by `scripts/release.sh`), logged once at startup, and threaded through `internal/app.Run(ctx, cfg, version)` → `mcpserver.New(svc, version)` so MCP clients see the same real build version in the `initialize` handshake (`serverInfo.version`) — not a separate hardcoded value (that was a real drift bug, see PLAN.md §10.6 #23).

---

## 7. Testing

Full strategy (unit/integration/contract/race pyramid, coverage philosophy) is in [PLAN.md §6](./PLAN.md#6-testing-strategy). Quick reference for "where do I put a test for X":

| You're testing... | Put it in | Pattern to follow |
|---|---|---|
| Pure logic, no I/O (validation, crypto, TLS-mode mapping) | `*_test.go` next to the code, no build tag | `internal/cryptox/secretbox_test.go`, `internal/mailer/smtp/client_test.go` |
| Code that touches SQLite | `*_test.go`, no build tag — use a real `storage.Open(t.TempDir()+"/x.db")`, never mock the DB | `internal/account/repository_test.go` |
| Code that needs a mailer implementation but shouldn't dial real network | `*_test.go`, no build tag — implement a tiny mock satisfying `mailer.Sender`/`Fetcher`/`Checker`/`FetcherChecker` inline in the test file | `mockFetcherChecker` in `internal/account/messages_test.go` and `internal/mcpserver/server_test.go` |
| A real protocol client against a real (fake) server | `integration_test.go`, `//go:build integration` tag | `internal/mailer/{smtp,imap,pop3}/integration_test.go` — three different fake-server strategies: `go-smtp` server library, hand-rolled `imapserver.Session`, and a hand-rolled raw-socket POP3 server, chosen per what was available/practical for each protocol |
| A full HTTP request → response round trip | `internal/api/server_test.go`, no build tag — real `Server` + real temp-file SQLite, `httptest.NewRecorder` | `TestAccountsCRUD_EndToEnd` |
| An MCP tool end-to-end through the real JSON-RPC transport | Not automated (yet) — do it manually with `curl` against a running binary's `/mcp` endpoint (`initialize` → `tools/list` → `tools/call`) whenever you change a tool's argument struct or schema declaration | See PLAN.md Fase 8 "crosscheck" notes for the exact `curl` sequence used to catch a real `BindArguments` gap this way |

Commands: `go test ./...` (unit), `go test -tags integration ./...` (+ integration), `go test -race -tags integration ./...` (requires cgo/gcc — not available on every dev machine, see PLAN.md Fase 2/6 notes; run in CI). `go build -tags xmailtray ./...` and `go vet -tags xmailtray ./...` to cover the Windows-only files.

---

## 8. Known Gaps / Things an Agent Should Not Assume Are Done

Kept in sync with PLAN.md's per-phase "catatan implementasi" — check there for full detail and dates. A structured, independent code review (`CODE_REVIEW.md`, kept in the repo root as an archive) was run against this document + PRD.MD + PLAN.md; every finding was verified against the actual code and either fixed (with a regression test) or documented as an intentional decision — see **PLAN.md §10** for the full list, including 3 real bugs (`DELETE` breaking the response envelope, POP3 `starttls` passing validation despite being unsupported, and `messages_cache` being write-only/dead code) and 4 previously-missing PRD requirements that are now implemented (custom headers, IMAP mark-as-read, attachment-list caching) or explicitly deferred rather than half-stubbed (MCP client — see below). A second review pass over those fixes then caught one more real bug (`messages_cache` ordering silently flipping once the cache warmed up — PLAN.md §10.5 #15) plus a few follow-on cleanups. As of the last update to this document:

- **Docker x64 has been built and run for real** (`scripts/release.sh docker-amd64`, then `docker run` with a named volume, REST calls, and a `docker restart` to prove data persistence — all passed). This caught a real bug: the original `Dockerfile` left `/app/data` unwritable by the distroless image's nonroot user, so every container crashed on startup with a SQLite `unable to open database file` error — building the image alone never surfaced this, only actually running it did. Fixed by pre-creating `/app/data` with correct ownership in the build stage (see PLAN.md Fase 6 notes). **Docker Armbian/arm64 has also been built and run for real** under QEMU emulation (slow — several minutes — but it completed and passed the same REST smoke test), so both Docker targets are verified, not just Windows.
- **Windows Service installation (`svc.Install()`) has never been executed** — intentionally, since it's a system-level change requiring Administrator. This is exactly how a real, serious bug slipped through once already: `New()` never set `service.Config.EnvVars`, so an installed service would launch via the SCM into a fresh, empty environment and immediately fail `config.Load()` — the tray's "Start"/"Stop" (foreground, non-service) path was tested and worked, which is a different code path and didn't exercise this at all. Fixed (PLAN.md §10.6 #19) and verified by simulating the exact failure mode in `winservice/service_test.go` (round-trip `buildServiceConfig`'s `EnvVars` back through `config.Load` in a cleared environment), but a real `Install()` → SCM `Start()` still hasn't been done end-to-end. If you touch `internal/winservice` again, don't trust the foreground tray path as evidence the service path also works — they diverge exactly where this bug was.
- **`go test -race` has never been run** in any environment for this codebase (no cgo toolchain available). Run it before trusting concurrency-sensitive changes near `internal/app.Run`'s goroutines.
- **TLS/STARTTLS integration test coverage for SMTP is plaintext-only** — the automated integration test only exercises `tls_mode: none`; `tls`/`starttls` modes are implemented and unit-tested for option-mapping, but not integration-tested against a real TLS handshake (would need a self-signed-cert test fixture, not yet built). Note this is orthogonal to SMTP *authentication*, which **is** integration-tested (`TestIntegration_Send_ActuallyAuthenticates`, CRAM-MD5 over the plaintext fixture) — that test exists specifically because a prior version of `smtp.Client` never actually sent an AUTH command at all despite `WithUsername`/`WithPassword` being set (go-mail defaults to `SMTPAuthNoAuth` unless `WithSMTPAuth(...)` is also called), a bug caught by manually probing a real Gmail account with a wrong password and getting a suspicious `ok:true` back. Fixed in `buildMailClient()`; see PLAN.md Fase 2/8 notes.
- **No automated test asserts "REST and MCP return identical data for the same account"** — §1 rule 3 is true by construction (both call the same `Service` method) and spot-checked manually, but there's no regression test that would catch the two adapters silently diverging in field mapping.
- **`api_keys` table is unused** — intentional (see §5.6 and PLAN.md §10.3); the PRD line that used to contradict this ("API key disimpan sebagai hash") was a mis-stated plan for this same backlog feature, corrected in PRD.MD rather than implemented to match, since hashing a key that's never stored doesn't make sense.
- **"xmail as MCP client" has zero code** — not even a scaffold interface. An earlier attempt at one (`ExternalToolCaller`) was removed after review flagged it as a dead abstraction (no caller anywhere) — see PLAN.md §10.5 #16. Design the interface when Fase 9 is actually implemented, against a real use case, not before.
- **`messages_cache` ordering was silently backwards once the cache was warm** until PLAN.md §10.5 #15's fix (`sort_rank` column) — if you touch `UpsertMessages`/`ListMessages` again, keep the regression test (`TestRepository_UpsertMessages_PreservesOrder`) passing; it specifically re-upserts the same batch twice (insert then update) because the bug only showed up on the *second* write in some variants of the (rejected) fix attempts.
