# xmail

Email-as-a-service backend: send & receive email via REST API and MCP, multi-account, TLS & non-TLS, SQLite storage.

Documentation:
- [ARCHITECTURE.md](./ARCHITECTURE.md) — **technical reference for the current codebase**: what's in it, how it works, and how to add features. Written so both humans and LLM agents working on this repo can understand it fast — start here if you want to change/add code directly.
- [CODE_REVIEW.md](./CODE_REVIEW.md) — archive of the independent code review results; the resolution (which fix for which finding) is recorded there.
- [CONTEXT.md](./CONTEXT.md) — domain glossary: core terms (Account, Message, Message cache, Coverage, Exhausted, Fetch window, etc.) plus the terms to avoid.
- [docs/adr/](./docs/adr/) — Architecture Decision Records: architecture decisions and their rationale (starting with ADR-0001 on unifying protocol dispatch).
- [docs/API.md](./docs/API.md) — **REST API reference**: base URL & auth, response envelope, every endpoint (request/response), error codes, and `curl` examples.
- [docs/MCP_CLIENT_GUIDE.md](./docs/MCP_CLIENT_GUIDE.md) — **MCP client guide**: how to connect Claude Code/Claude Desktop/custom clients via Streamable HTTP or stdio, reference for the 5 tools, and troubleshooting.
- [docs/postman/](./docs/postman/) — ready-to-import Postman collection (`xmail.postman_collection.json` + `xmail.postman_environment.json`).

Current status: **all phases (1-8) complete & verified for real** — core, SMTP, IMAP, POP3, MCP, hardening review, Windows tray+service, and all three release targets (Docker x64, Docker Armbian/arm64, Windows x64) have been built, run, and smoke-tested directly (not just compiled successfully). The repo has also been through several rounds of independent review (`CODE_REVIEW.md` — full archive of every round; each finding's resolution is recorded there). One of those rounds found 3 hard violations (the worst: an installed Windows Service could never start) — all were fixed with regression tests, not merely recorded. Beyond those core phases, **Phase 10** (IMAP folder listing: `GET /accounts/{id}/folders` + MCP tool `list_folders`, plus the folder error classification 500→400) is also complete.

## Features

- Multiple email accounts in a single instance.
- Send email via SMTP (implicit TLS, STARTTLS, or non-TLS), with attachment & custom header support (`X-Priority`, etc.).
- Fetch & check email via IMAP (folders, unread count, attachment list, mark-as-read) and POP3 (simple fetch).
- List IMAP folders (`GET /accounts/{id}/folders` / MCP tool `list_folders`) — know the correct folder names along with their role attributes (`\Sent`, `\Drafts`, …), without guessing.
- **Cache-first** email fetching — the first request hits the server & saves to SQLite, subsequent requests come straight from the cache (`refresh=true` forces a re-fetch from the server).
- Account credentials are encrypted (AES-256-GCM) before being stored in SQLite.
- Expose email operations as MCP tools (`list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`, `list_folders`) for AI agents such as Claude Desktop/Claude Code.
- **Lightweight web dashboard for account management** — account CRUD + per-protocol connectivity test, served from the same binary (no extra service/artifact). See [Dashboard](#dashboard).
- Single static binary (Go, no CGO), small Docker image.
- **3 release variants** with identical behavior: Docker x64, Docker Armbian (arm64), Windows x64 (system tray + installable as a Windows Service). See [Release / Installation](#release--installation).

## Project Structure

```
cmd/xmail/              entrypoint headless — used by Docker x64 & Docker Armbian (arm64)
cmd/xmail-tray/          entrypoint Windows x64 — system tray + Windows Service (build tag: xmailtray)
internal/app/            Run(ctx, cfg): shared wiring, used by cmd/xmail AND cmd/xmail-tray
internal/winservice/     install/start/stop/uninstall as a Windows Service (build tag: xmailtray)
internal/config/         load config from environment variables
internal/storage/        SQLite connection + migration
internal/cryptox/        credential encryption/decryption (AES-GCM)
internal/account/        email account model, repository, service
internal/mailer/         Sender/Fetcher/Checker/Marker/FolderLister interface
internal/mailer/smtp/    send implementation (SMTP)
internal/mailer/imap/    fetch/check/folder implementation (IMAP)
internal/mailer/pop3/    fetch implementation (POP3)
internal/api/            REST API (router, middleware, handler)
internal/dashboard/      web dashboard (HTML/CSS/JS assets embedded + static handler)
internal/mcpserver/      MCP server (expose tools)
scripts/release.sh       build release for 3 targets → dist/ + checksums
assets/icon.ico          Windows tray icon (placeholder)
```

Details for each package, the design rationale, and how to add new code (endpoint/tool/protocol) are in **[ARCHITECTURE.md](./ARCHITECTURE.md)** — that document is more complete & more up-to-date than the project structure sketch above.

## Requirements

- Go 1.26+
- Docker (optional, for building/running the image)

## Running (development)

```bash
# copy the example env, then fill it in
cp .env.example .env

go mod tidy
make run
# or: go run ./cmd/xmail
```

`.env` is loaded automatically (via `github.com/joho/godotenv`, dev-only) — real env vars that are already set (e.g. from Docker `-e`/`--env-file`) always win; `.env` is only a fallback when nothing is set.

### Environment Variables

| Variable | Required | Description |
|---|---|---|
| `XMAIL_LISTEN_ADDR` | no (default `:5569`) | HTTP API listen address |
| `XMAIL_DB_PATH` | no (default `xmail.db`) | SQLite file path |
| `XMAIL_ENCRYPTION_KEY` | **yes** | Base64 of a random 32-byte key, used to encrypt account credentials. Generate: `openssl rand -base64 32` |
| `XMAIL_API_KEY` | **yes** (MVP) | Static API key for the `X-API-Key` auth header |
| `XMAIL_MCP_STDIO` | no | Set `true` to expose the MCP server over the stdio transport |

## Dashboard

The same binary serves a web dashboard for **account management** (CRUD + connectivity test). There's no extra process or artifact — the assets are embedded into the binary.

- **Open**: `http://localhost:5569` (or `http://<XMAIL_LISTEN_ADDR>`) in a browser. From the Windows tray, the **Open dashboard** menu item opens this URL.
- **Sign in**: enter the `XMAIL_API_KEY` value. The key is stored in the browser's `sessionStorage` (lost when the tab is closed) and sent as the `X-API-Key` header on every request — **the same key** as the REST API/MCP.
- **What you can do**: list/detail/create/edit/delete accounts, and test SMTP/IMAP/POP3 connectivity per account.
- **What the dashboard doesn't have**: sending email, fetching/reading messages, and listing folders — the email path still goes through the REST API/MCP.

The dashboard doesn't add any access rights: static assets (`GET /`) are indeed unauthenticated because they hold no secrets, but every API endpoint still requires `X-API-Key` (see [docs/API.md](./docs/API.md) §3). Because it's served same-origin, there's no CORS. Dashboard responses are given a `default-src 'self'` CSP.

**Caution**: if `XMAIL_LISTEN_ADDR` is not bound to loopback (e.g. `0.0.0.0:5569`), the dashboard & API can be reached from the network — anyone holding the API key can manage accounts. Restrict network/firewall access as needed.

## Running via Docker (development)

```bash
make docker-build
make docker-run
```

SQLite data is stored in the `xmail-data` volume (mounted to `/app/data` in the container) so it persists across restarts.

## Release / Installation

There are 3 release targets, all going through the same `internal/app.Run` so behavior stays consistent. All artifacts are built by a script, landing in `dist/` complete with `SHA256SUMS.txt`. There are **2 script versions with exactly the same functionality** — use whichever one matches the shell you have available:

| Script | Run from | When to use |
|---|---|---|
| [`scripts/release.sh`](./scripts/release.sh) | Git Bash / WSL / Linux / macOS | Git for Windows is already installed (most common on dev machines), or WSL/Linux/macOS/CI |
| [`scripts/release.ps1`](./scripts/release.ps1) | PowerShell (native Windows) | **Windows without Git Bash/WSL** — needs no `bash`, `make`, or any Unix tool |

```bash
# Git Bash / WSL / Linux / macOS
scripts/release.sh <target> [version]   # target: docker-amd64 | docker-arm64 | windows-amd64 | all
scripts/release.sh all v0.1.0           # or: make release-all VERSION=v0.1.0
```

```powershell
# PowerShell native (Windows without Git Bash/WSL)
scripts\release.ps1 -Target <target> [-Version <version>]
scripts\release.ps1 -Target all -Version v0.1.0
```

How the script works & how to add a new target is in [ARCHITECTURE.md § Build & Release](./ARCHITECTURE.md#6-build--release). `make release-*` is just a short alias for `release.sh` — if you don't have `make` (the default on plain Windows), use `release.ps1` or `release.sh` directly.

### 1. Docker x64 (`linux/amd64`)

```bash
scripts/release.sh docker-amd64
docker load < dist/xmail-<version>-linux-amd64-docker.tar.gz
docker run -d -p 5569:5569 -v xmail-data:/app/data --env-file .env xmail:<version>-amd64
```

### 2. Docker Armbian (`linux/arm64`)

For ARM SBCs running Armbian (Orange Pi, Rock Pi, etc.). It can be cross-built from an x64 machine via `docker buildx`, or built right on the board.

```bash
scripts/release.sh docker-arm64
docker load < dist/xmail-<version>-linux-arm64-docker.tar.gz
docker run -d -p 5569:5569 -v xmail-data:/app/data --env-file .env xmail:<version>-arm64
```

### 3. Windows x64 (system tray + Windows Service)

```bash
scripts/release.sh windows-amd64          # via Git Bash/WSL
```
```powershell
scripts\release.ps1 -Target windows-amd64  # via native PowerShell, without Git Bash/WSL
```
Both produce the same result: `dist/xmail-tray-windows-amd64-<version>.exe` (tested working via both routes).

Run the `.exe` — an icon appears in the system tray. From the tray menu you can:
- **Start / Stop** — run/stop the service in the foreground (while the tray app is open).
- **Install as Windows Service** — register itself as a Windows Service (`XmailService`) so it keeps running in the background without needing a login/open tray; it shows up in `services.msc`.
- **Uninstall** — remove the Windows Service.
- **Open dashboard** — open `http://<listen_addr>` in the default browser.

#### If the `.exe` is double-clicked but "nothing happens"

Symptom: no tray icon, no entry in Task Manager, nothing in `services.msc`, no log. The most common cause: **the configuration doesn't exist yet**, so the process exits in the first second with `XMAIL_API_KEY is required` — and because this release build is `windowsgui` (no console), that error previously wasn't visible anywhere.

Now a failed start always leaves a trace:

- **Error dialog** appears when there's a user who can see it, containing what needs to be created (skipped if `XMAIL_NO_DIALOG=1`).
- **Log file**: `%LOCALAPPDATA%\xmail\xmail-tray.log` (e.g. `C:\Users\<you>\AppData\Local\xmail\xmail-tray.log`) — check this first.

What's needed for it to run: `XMAIL_API_KEY` and `XMAIL_ENCRYPTION_KEY`. Easiest way: create a `.env` file **in the same folder as `xmail.exe`** (not in the repo root) — `.env` is read from the working directory, and for a double-clicked exe that means the exe's own folder:

```ini
XMAIL_API_KEY=your-chosen-key
XMAIL_ENCRYPTION_KEY=<output of: openssl rand -base64 32>
```

Two notes so you don't misdiagnose:
- Even when running normally, the tray app doesn't show up in Task Manager's **Apps** list (it has no regular window) — look at the **Details** tab.
- After the tray icon appears, the server is **not** running yet until you click **Start**, or install it as a Windows Service. Before that, "Open dashboard" opens a port that is still closed.

> Status: the Windows tray target **is already implemented and verified running** — `make release-windows-amd64` produces an `.exe` that has been tested actually running (tray icon + menu rendering without errors). One thing is **not** automatically validated: actually clicking "Install as Windows Service" (deliberately not executed automatically because it's a system-level change that needs Administrator — run it manually and check `services.msc`/`sc query XmailService` before release). The icon at `cmd/xmail-tray/icon.ico` is still a placeholder (generated programmatically) — replace it with the final asset at any point before a public release.

## Testing

The full strategy is a pyramid: unit → integration → contract → race/static → manual per release target. In summary:

```bash
make test               # unit tests only — fast, no network/docker, must be green every phase
make test-integration   # + integration tests (tag "integration"): in-process fake SMTP/IMAP/POP3 servers
make test-race          # + race detector (-race), required before image/release builds
make coverage           # per-function coverage report (go tool cover)

make test-all           # ALL checks at once (see below)
```

### Full check before commit

There are **two equivalent scripts** (like `release.sh`/`release.ps1`) that run the entire dev verification suite in sequence and stop at the first failure:

| Script | Run from | When to use |
|---|---|---|
| [`scripts/test.sh`](./scripts/test.sh) | Git Bash / WSL / Linux / macOS / CI | you have Git Bash/WSL/macOS/Linux |
| [`scripts/test.ps1`](./scripts/test.ps1) | PowerShell (native Windows) | **Windows without Git Bash/WSL** — needs no `bash`/`make` |

```bash
bash scripts/test.sh                  # all checks
bash scripts/test.sh --race           # + race detector (needs cgo/gcc)
bash scripts/test.sh --coverage       # + coverage report
bash scripts/test.sh --help           # list of flags
```

```powershell
scripts\test.ps1                 # all checks
scripts\test.ps1 -Race -Coverage
```

Note: in git both scripts are stored with mode `100644` (same as `scripts/release.sh`), so call them via `bash <script>` — or run `chmod +x scripts/*.sh` once if you want to invoke them directly.

What runs: `gofmt` (format check), `go mod tidy -diff` (go.mod/go.sum drift), `go vet`, `go build`, the tray+service compile check (`GOOS=windows -tags xmailtray`, so it also runs on Linux/macOS), unit tests, integration tests, tests for `internal/winservice` + `cmd/xmail-tray` (only on a Windows host), and `node --check` for the dashboard's `app.js` (if `node` is available). The race detector is opt-in because it needs cgo/gcc. `make test-all` is just a thin alias for `scripts/test.sh`.

No `testify`/extra framework — just the stdlib `testing` + table-driven tests, in line with this project's minimal-dependency principle. There is also a manual/E2E checklist for each release target (Docker x64, Docker Armbian, Windows tray+service).

## Contributing / Work Order

Follow the phases in order: Phase 1 (Core) → Phase 2 (SMTP) → Phase 3 (IMAP) → Phase 4 (POP3) → Phase 5 (MCP) → Phase 6 (Docker & Hardening) → Phase 7 (Windows Tray & Service) → Phase 8 (Release Packaging, 3 targets), then Phase 10 (IMAP folder listing). Each phase has its own verification steps.
