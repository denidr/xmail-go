# Code Review — xmail

Two-axis review (Standards & Spec) of the entire worktree contents, compared against the fixed point `66ebca7` ("Init empty repo").

## Context & scope

- **Fixed point:** `66ebca7` — the empty scaffold commit.
- **Diff command:** `git diff 66ebca7...HEAD` → **empty**. `git log 66ebca7..HEAD --oneline` is empty as well.
- **Reality reviewed:** the entire project is not yet committed. Aside from `README.MD` (modified), every file is **untracked**, so nothing shows up in `git diff`. Review scope = the entire working tree (55 untracked files + 2 modified files).
- **Spec sources:** `ARCHITECTURE.md`, `CONTEXT.md`, `README.MD`, `docs/API.md`, `docs/MCP_CLIENT_GUIDE.md` (there is no issue tracker / `docs/agents/issue-tracker.md`, and no issue reference in any commit — the only commit message is "Init empty repo").
- **Standards sources:** `ARCHITECTURE.md` (§1 Core Design Principles, §5 How to Extend, §6 Build & Release, §7 Testing, §8 Known Gaps), `CLAUDE.md`, `.commandcode/taste/*.md`, plus **Fowler's smell baseline** (always a judgement call, never a hard violation). There is no `CONTRIBUTING.md` / `CODING_STANDARDS.md` / `AGENTS.md`.

## Standards

**Documented standard violations (hard): none.** What was verified as complied with:

- Only `internal/app/app.go` imports the concrete `mailer/{smtp,imap,pop3}` packages.
- `cmd/xmail` and `cmd/xmail-tray` call `app.Run` without business logic.
- Config comes purely from env vars (`internal/config/config.go`).
- Only the stdlib `testing` package — no testify in any `_test.go`.
- The build tag `//go:build windows && xmailtray` is present in `cmd/xmail-tray/main.go` + `internal/winservice/service.go`.
- No `InsecureSkipVerify`.
- Logs carry no email body/credentials (`internal/api/middleware.go`).
- Release logic lives in `scripts/release.{sh,ps1}`; the Makefile is just a wrapper.
- `make test` is network-free.
- MCP schemas are declared explicitly (no `mcp.WithInputSchema[T]`).

**Divergence (judgement call):** the documented decision locks in "env var only … `.env` optional for dev via `godotenv`", but nothing loads `.env` (no godotenv import) — `.env.example` exists yet `go run ./cmd/xmail` will not read it. This is doc↔code drift, not a hard violation (the clause is explicitly marked optional).

**Smell baseline (all judgement calls):**

1. **Duplicated Code** — identical newest-first window arithmetic in two files. `internal/mailer/imap/client.go` `Fetch`: `end := total - offset; …; start := end - limit + 1; if start < 1 { start = 1 }`; `internal/mailer/pop3/client.go` `Fetch` repeats it verbatim. → extract a single helper.
2. **Duplicated Code** — the default input (`"imap"`, `"INBOX"`, limit 20) is reimplemented three times: `internal/api/messages_handler.go`, `internal/mcpserver/server.go`, and again in `account.Service.FetchMessages`/`CheckNew`. Close to ARCHITECTURE §1 rule 3 / CLAUDE "no duplicated business logic", but still at the adapter level → judgement.
3. **Data Clumps** — `(cfg ConnectionConfig, username, secret string)` flows through `TesterFactory`/`SMTPSenderFactory`/`IMAPFactory`/`POP3Factory` and every `New`; `(accountID, protocol, folder)` flows through `ExistingUIDs`/`UpsertMessages`/`ListMessages`/`FetchMessages`/`CheckNew`. Each of them deserves to be a single type.
4. **Primitive Obsession / Repeated Switches** — the protocol is typed `string` and switched twice in `internal/account/service.go` (`TestConnection`, `resolveFetcher`) to select the same per-protocol wiring; one `Protocol` type or one shared map could collapse both.
5. **Mysterious Name / magic value** — `Service.Update` invents `validateSecret = "unchanged"` purely to pass the non-empty check in `Validate`; the name/value misleads about the intent.
6. **Duplicated Code (minor)** — `connHost`/`connPort`/`connTLSMode` are three identically shaped nil-guards in `internal/account/repository.go`.

## Spec

Verification: `go build ./...`, `go vet ./...`, `go test ./...`, and `-tags integration` are all green. The documented test-count claim holds exactly (60 unit / 76 integration `=== RUN`).

**(a) Missing / partial requirements**

1. **Custom headers — missing.** Requirement: "Support attachments, HTML & plain text body, CC/BCC, **basic custom headers**." `sendRequest` in `internal/api/send_handler.go` has no headers field and `internal/mailer/smtp/client.go` sets nothing either. No note in `ARCHITECTURE.md` records this omission.
2. **IMAP "mark as read" — missing.** Requirement: "IMAP: support folders other than INBOX, **mark as read**." No endpoint, MCP tool, or `Service` method sets `\Seen`; `internal/mailer/imap/client.go` only reads `FlagSeen`. There is no Store call on the production path.
3. **Attachment list caching — partial.** Requirement: "Email body & metadata (subject, from, to, date, **attachment list**) are stored/cached in SQLite." The `messages_cache` table in `0001_init.sql` has no attachment column; `mailer.Message` does not carry one either.
4. **MCP Client role — missing.** Requirement: "MCP Client: the service's ability to call an external MCP server … (**prepared as an extensible interface**)". There is no interface or scaffold; it was merely deferred to a later phase.
5. **API key hashing — contradicted (recorded deviation).** Requirement: "API keys are stored as a hash (not plaintext) in the `api_keys` table." The table is unused; `middleware.go` compares the plaintext `XMAIL_API_KEY` from the environment using a constant-time compare. This was already recorded in the project notes, but it still contradicts the requirement.

**(b) Scope creep (minor, mostly already recorded)**

`GET /healthz`, the `XMAIL_MCP_STDIO` stdio transport, the tray's "Open dashboard", `scripts/release.ps1` — none of these were requested by the original requirements; healthz & stdio are recorded in the project docs.

**(c) Implemented but wrong**

6. **Message cache is write-only on the read path.** The requirement wants caching "so the next fetch is faster (not always a full round-trip to the mail server)". `GET /accounts/{id}/messages` → `Service.FetchMessages` always dials the server; `Repository.ListMessages` is only called from tests — dead production code, so reads never touch the cache.
7. **POP3 `starttls` passes validation but cannot be used.** `Validate` accepts `starttls` for pop3, but `connect()` in `pop3/client.go` returns "not supported". Consequently `POST /accounts` returns 201 for a configuration whose every POP3 operation then fails. The project docs record the client-side error, not the validation gap.
8. **DELETE violates the envelope contract.** The documented contract says: "All responses: `{data, error}`" — `handleAccountDelete` returns 204 with no body.

**(partial)** "test-connection … **before being stored permanently**": the endpoint needs an `{id}` that already exists, so validation happens after save (the original spec sketch contains the same tension).

## Summary

- **Standards** — 7 findings (0 hard, 7 judgement). Worst: the duplicated newest-first window arithmetic in `imap/client.go` + `pop3/client.go`, plus the `string`-typed `protocol` switched twice in `service.go`.
- **Spec** — 10 findings (5 missing/partial, 1 contradicted-but-recorded, 3 implemented-but-wrong, 1 partial). Worst: the message cache is never read on the fetch path, so the caching requirement is effectively unmet and `ListMessages` is dead production code.

The two axes are deliberately not merged/re-ranked — one axis can pass while the other fails, and cross-ranking would hide that.

---

# Re-verification (round 2 — after the fixes)

Changes reviewed: 20 source files + 2 new files (`internal/mcpserver/client.go`, `internal/storage/migrations/0002_message_attachments.sql`), plus `ARCHITECTURE.md`, `README.MD`.

**Technical baseline:** `go build ./...`, `go vet ./...`, `go build -tags xmailtray ./...`, `go vet -tags xmailtray ./...` are all clean; `go test ./...` is green; `go test -tags integration ./...` is green. The documented test-count claim (68 unit / 87 integration) matches the measured result.

**How it was verified:** both axes were re-verified by separate sub-agents against the round-1 finding list; the headline finding (the cache ordering bug) was confirmed first-hand from the source code, not just from the agent reports.

## Standards — status per finding

| # | Finding | Status |
|---|---|---|
| 1 | `.env`/`godotenv` drift — the documented decision locks in "`.env` optional via `godotenv`", but nothing loads `.env` | **Still open** — `go.mod` has no `godotenv`, there is no loader in the code; `README.MD` still tells you to `cp .env.example .env` and then `make run` (which = `go run ./cmd/xmail`, which does not read that file). Neither the code nor the docs have been reconciled |
| 2 | Duplicated newest-first window arithmetic (`imap/client.go` + `pop3/client.go`) | **Fixed** — extracted into `mailer.WindowRange` (`internal/mailer/types.go`), used by both clients |
| 3 | Duplicated default input (`"imap"`, `"INBOX"`, 20) in 3 places | **Fixed** — `account.DefaultProtocol` / `account.DefaultFetchLimit` are now the single source. Remaining minor: the literal `"INBOX"` is still repeated in `imap/client.go`, `pop3/client.go`, and `service.go` |
| 4 | Data Clumps — `(cfg, username, secret)`, `(accountID, protocol, folder)` | **Still open (deliberate)** — recorded as a deliberate decision in the project notes |
| 5 | Primitive Obsession / Repeated Switches on `protocol string` | **Partial** — config selection was unified into `connConfigForProtocol`; *factory* selection is still two separate switches (`TestConnection`, `resolveFetcher`). Recorded as deliberate |
| 6 | `validateSecret = "unchanged"` (magic value in `Service.Update`) | **Fixed** — `Validate(a, requireSecret bool, secret string)`; `Update` passes `secret != nil` |
| 7 | `connHost`/`connPort`/`connTLSMode` — 3 uniform nil-guards in `repository.go` | **Fixed** — collapsed into one `connFields(...)` |

## Spec — status per finding

| # | Finding | Status |
|---|---|---|
| 1 | Custom headers | **Fixed** — `sendRequest.Headers` → `mailer.OutgoingMessage.Headers` → `SetGenHeaderPreformatted`; the MCP `send_email` schema gains `headers`; covered by a test. The original requirement text was left unchanged |
| 2 | IMAP "mark as read" | **Fixed** — `mailer.Marker`, `imap.Client.MarkRead` (STORE +FLAGS `\Seen` by UID), `Service.MarkRead`, `Repository.MarkMessageRead`, route `POST /accounts/{id}/messages/read`, unit + integration tests. POP3 returns a validation error (correct). No MCP tool yet — one was indeed never required |
| 3 | Attachment list caching | **Fixed** — `Message.Attachments`, IMAP `BODYSTRUCTURE{Extended:true}`, migration `0002_message_attachments.sql` (column `attachments TEXT`), `encode/decodeAttachments`, integration test. POP3 stays `nil` (the protocol does not report attachments) |
| 4 | MCP Client role | **Partial** — `internal/mcpserver/client.go` is only an interface: no implementation, not wired into `Service`/`app`, no tests. It satisfies the literal wording "prepared as an extensible interface", but `ARCHITECTURE.md` §8 counts it as a requirement that is "already implemented" → **overstated** for a stub |
| 5 | API key hashing | **Closed by changing the spec** — the requirement was rewritten. The change is explicitly labeled and justified (a static key from the environment is never stored, so there is nothing to hash; the `api_keys` table is described as preparation for multi-key support that is still backlog). The only instance of reword-to-match-code, and it was done transparently — not smuggled in |
| 6 | Message cache write-only on the read path | **Fixed, but it introduced a new bug** — `FetchMessages` is now cache-first + a `refresh` param, proven via a fetch-call counter at the Service and REST level. See "New findings" #1 |
| 7 | POP3 `starttls` passes validation but is unusable | **Fixed** — `Validate` rejects `pop3`+`starttls` at save time, rather than merely failing at use time (`TestValidate/pop3_starttls_rejected` locks this behavior) |
| 8 | DELETE violates the envelope contract (`{data,error}`) | **Fixed** — `handleAccountDelete` returns `200 {"deleted":true}`; asserted by `TestAccountsCRUD_EndToEnd` |
| 9 | `test-connection` before being stored permanently | **Still open** — the endpoint still needs an existing `{id}`, there is no validate-before-save path; the requirement text is unchanged and there is no deviation note |
| 10 | Unrecorded scope creep | **Fixed (partially)** — `healthz`, stdio `XMAIL_MCP_STDIO`, the tray's "Open dashboard", `release.ps1` are now recorded in the project docs + `ARCHITECTURE.md` §6 + README. The requirement text itself still is not updated: it does not include `/healthz`, and it still mentions only one release script |

## New findings

1. **🔴 BUG: cached result order is reversed (no test yet).** `ListMessages` uses `ORDER BY fetched_at DESC, rowid DESC`, but `UpsertMessages` assigns **one** single `now` to the whole batch — so `fetched_at` is identical for every row and ordering falls back to `rowid DESC`. Because `Fetch` inserts newest-first (newest message = smallest rowid), `rowid DESC` actually reverses the order. Confirmed first-hand from `internal/account/messages.go` (`ListMessages` + `UpsertMessages`).
   **Effect:** `GET /accounts/{id}/messages` returns **newest-first when the cache is empty**, then **oldest-first once the cache is populated** — same endpoint, reversed order, and pagination (`offset`) runs in the wrong direction. This also applies to the MCP `fetch_emails` tool. `TestRepository_UpsertAndListMessages` only asserts `len`, never order, so this bug slipped through. Additional note: `fetched_at` has second resolution, so two different batches within the same second can also collide.
   **Suggestion:** order by a monotonic key (at minimum `rowid ASC` for the same batch, and more safely a dedicated sequence column/`AUTOINCREMENT`), then add a test that asserts order with >1 row on the cache path.
2. **Speculative Generality (new):** `ExternalToolCaller` in `internal/mcpserver/client.go` has no implementation and no callers — a dead abstraction. It is scaffolding the requirements did ask for, but still a judgement-call smell.
3. **Duplicated Code (new, minor):** the guards `if protocol == "" { protocol = DefaultProtocol }` / `if folder == "" { folder = "INBOX" }` are now copied into 4 methods (`TestConnection`, `FetchMessages`, `CheckNew`, `MarkRead`) — the fix for finding #3 reintroduced the same two lines in many places.
4. **Doc drift:** the project docs say "68 unit (up from 64)" whereas the documented review baseline was 60; the per-phase numbers are stale (40/48/53/60). The requirement text has not yet added `/messages/read`, `refresh` or `headers`; it has not added the `attachments` column; and it still mentions only one release script.
5. **Minor:** the storage tests do not assert the new `attachments` column.

## Round 2 summary

- **Standards** — 7 findings: **4 fixed**, 1 partial, 2 still open (1 of them deliberate + documented). 0 hard violations; no documented-convention violations. Plus 1 new speculative smell.
- **Spec** — 10 findings: **7 fixed**, 1 partial, 1 still open, 1 closed by changing the spec (transparently). Plus **1 serious new bug** (cache ordering), 1 minor duplication, and doc drift.

Worst right now: **the cache ordering bug.** The fix for finding #6 met the *letter* of the caching requirement ("the next fetch is faster") but broke the newest-first ordering guarantee that holds on the live path — and no test caught it.

---

# Re-verification (round 3 — after the round-2 fixes)

Changes: `internal/account/messages.go`, `internal/account/messages_test.go`, `internal/account/service.go`, new migration `0003_message_sort_rank.sql`, plus `ARCHITECTURE.md`. `internal/mcpserver/client.go` was **deleted**.

**Technical baseline:** `go build`/`go vet` (including `-tags xmailtray`) clean; `go test ./...` = **69 green**; `go test -tags integration ./...` = **88 green**. The documented claim (69/88) matches the re-measured result exactly.

## Status of the round-2 new findings

| # | Finding | Status |
|---|---|---|
| 1 | 🔴 Cached result order reversed | **Fixed** — column `sort_rank` (migration `0003`, `INTEGER NOT NULL DEFAULT 0`) is explicitly filled with `len(msgs)-i` in `UpsertMessages` (newest = highest rank), and ordering becomes `ORDER BY fetched_at DESC, sort_rank DESC`. Verified directly from the code: the logic is correct and no longer depends on rowid/insert order. Regression test `TestRepository_UpsertMessages_PreservesOrder` asserts the order `[3,2,1]` on a cold cache **and** after re-upserting the same batch — the second case matters because `ON CONFLICT DO UPDATE` keeps the old rowid, so a fix relying on "insert order" alone would not have been enough. The old test (`TestRepository_UpsertAndListMessages`) was strengthened too: from checking only `len` to checking order |
| 2 | Speculative Generality — `ExternalToolCaller` with no implementation & callers | **Fixed (deleted)** — `internal/mcpserver/client.go` was dropped entirely rather than kept as a stub; the MCP Client requirement was revised (fully backlog, including its interface design) with an explicitly labeled rationale. `ARCHITECTURE.md` §8 also records this removal. The right call: a speculative interface with no consumer is just a guess at an API shape |
| 3 | Default guards (`protocol`/`folder`) duplicated in 4 methods | **Fixed (partially)** — the `protocol` guard is done: `resolveFetcher` now returns a `resolvedProtocol` that is already defaulted, used by `FetchMessages`/`CheckNew`/`MarkRead`; the remaining 2 guards (`TestConnection` + `resolveFetcher`) really are different code paths. Cleaned up along the way: `resolveFetcher` no longer returns the `Account`/`secret` that no caller ever used. **Remaining:** the `folder == "" → "INBOX"` guard is still duplicated 3× — see "Residual" below |
| 4 | Doc drift | **Fixed** — the storage section now references the actual migration files (`0001`/`0002`/`0003`) and names the `attachments` + `sort_rank` columns; the API section now includes `POST /accounts/{id}/messages/read`, the `refresh` query, and `headers{}` |

## Residual (not regressions — still open from earlier rounds)

- **`.env`/`godotenv` drift** — the documented decision still locks in "`.env` optional via `godotenv`", `README.MD` still tells you to `cp .env.example .env` and then `make run`, but there is no `godotenv` in `go.mod` and no loader in the code. Not yet reconciled.
- **Data Clumps** `(cfg, username, secret)` / `(accountID, protocol, folder)` — deliberately not refactored, recorded as a deliberate decision.
- **Duplicated factory-selection switch** in `TestConnection` vs `resolveFetcher` — deliberately left partially in place, recorded as a deliberate decision.
- **The `folder == "" → "INBOX"` guard is still duplicated 3×** (`FetchMessages`, `CheckNew`, `MarkRead`). This round fixed only the `protocol` part; the recorded resolution note mentions only the `protocol` guard. The `folder` part is untouched **and** not recorded as deliberately left alone.
- **`test-connection` before being stored permanently** — still open: the endpoint still needs an existing `{id}`, the requirement text is unchanged, and there is no deviation note.
- **The requirement text does not include `GET /healthz`**, and it still describes the build as "via one script, `scripts/release.sh`" even though `scripts/release.ps1` already exists and is used (README/ARCHITECTURE are already correct).

## Minor notes on this round's fixes

- **`fetched_at` with second resolution is used as the primary sort key.** Two different batches for the same folder within the same second will still tie, then fall back to `sort_rank`, whose scope is per-batch — they can interleave. It would be stronger to replace the primary key with a truly monotonic batch marker (not a timestamp), or to increase its resolution.
- **Migration `0003` gives old rows `DEFAULT 0`**, so a cache populated before the upgrade has `sort_rank = 0` for everything → arbitrary order until the next live refresh. One-off and low impact, but relevant if any DB is upgraded in place.

## Round 3 summary

- Round-2 new findings (4 items): **3 fully fixed, 1 partially fixed**.
- Cumulative since the start: round 1 (17 findings → 11 fixed, 2 partial, 2 open, 1 closed via a spec change) + round 2 (4 findings → 3 fixed, 1 partial). **There are no known open behavioral bugs left**; everything remaining is a conscious decision/documentation matter or document drift, not a functional defect.
- The most worthwhile remaining work: the `folder = "INBOX"` guard still duplicated 3× (small, but exactly the kind of regression that had just been cleaned up) and the unreconciled `.env`/`godotenv` drift.

---

# Re-verification (round 4 — a new two-axis review)

A full two-axis review of **the entire worktree** (not just the round-3 delta), run again from scratch against the fixed point `66ebca7`. Both axes were handled by separate sub-agents.

## Context & scope

- **Fixed point:** `66ebca7` ("Init empty repo"). `git diff 66ebca7...HEAD` is **empty**, `git log 66ebca7..HEAD --oneline` is **empty** — 66ebca7 is the only commit.
- **Actual scope:** the whole working tree (60 files, ~7,320 inserted lines). Untracked files were made visible via `git add -N .` (the index was restored after the review).
- **Spec sources:** `ARCHITECTURE.md`, `CONTEXT.md`, `README.MD`, `docs/API.md`, `docs/MCP_CLIENT_GUIDE.md` (there is no `docs/agents/issue-tracker.md` and no issue reference in any commit).
- **Standards sources:** `ARCHITECTURE.md`, `CLAUDE.md`, `.commandcode/taste/*.md`, plus Fowler's smell baseline.
- **Technical baseline:** `go build ./...`, `go vet ./...`, `go vet -tags xmailtray ./...` clean; `go test ./...` and `go test -tags integration ./...` green.
- **Stance:** the round 1-3 archive is **not** taken on trust; every "fixed"/"deliberate" claim was re-verified against the code. Both axes found the **`.env`/`godotenv` drift independently**, so that finding is mutually confirmed.

## Standards

**Documented standard violations (hard): 3.**

1. **No `.env` loader.** The documented decision locks in "`.env` optional … via `godotenv`". `go.mod` has no `godotenv`, and there is no loader in the code — `config.Load` (`internal/config/config.go:52`) is just `os.Getenv`. The dev flow in `README.MD` (`cp .env.example .env` then `make run` = `go run ./cmd/xmail`) **will not read that file** — the workflow is silently broken. Still open since round 3.
2. **An installed Windows Service can never start.** `internal/winservice/service.go:60-68` (`New`) builds the `service.Config` **without `EnvVars`**, so the process launched by the SCM does not see `XMAIL_API_KEY`/`XMAIL_ENCRYPTION_KEY`; `cmd/xmail-tray/main.go:45` then calls `log.Fatalf`. This capability is documented in `README.MD` §3 / `ARCHITECTURE.md` §6. (**Worst on this axis.**)
3. **Documents out of sync** (taste: "all relevant docs are updated when a feature/target changes"). The requirement text still does not include `GET /healthz`; it still mentions one release script even though `scripts/release.ps1` already exists (README/ARCHITECTURE are already correct).

**Smell baseline (all judgement calls): 4.**

4. **Duplicated Code** — the guard + literal `folder == "" → "INBOX"` repeats **7×**: `internal/account/service.go:338,373,414`, `internal/mailer/imap/client.go:89,148,171`, `internal/mailer/pop3/client.go:110`. Round 3 acknowledged 3× in `service.go` as not cleaned up, and **missed** 3 occurrences in `imap/client.go`. → one shared default.
5. **Speculative Generality / dead code** — `queryOr` (`internal/api/messages_handler.go:10`) has no callers; `handleMessagesList` uses `r.URL.Query().Get` directly (lines 40-41).
6. **Primitive Obsession / Duplicated** — `const timeFormat = "2006-01-02T15:04:05Z07:00"` (`internal/api/dto.go:80`) re-writes `time.RFC3339`, which is used directly elsewhere (`account/messages.go` and both clients).
7. **Duplicated version source** — `serverVersion = "0.1.0"` is hardcoded (`internal/mcpserver/server.go:21`) and is not wired to the `-X main.version` stamping (`ARCHITECTURE.md` §6) → MCP will report `0.1.0` forever.

**Suppressed** (documented repo decisions outrank the baseline): Repeated Switches on `protocol string` (`connConfigForProtocol`/`TestConnection`/`resolveFetcher`) and Data Clumps `(cfg,username,secret)` / `(accountID,protocol,folder)` — both recorded as deliberate decisions. The recorded resolution claim that the `protocol` guard has been de-duplicated is **correct** (`resolveFetcher` returns `resolvedProtocol`, used by `FetchMessages`/`CheckNew`/`MarkRead`).

## Spec

**(a) Missing / partial requirements: 4.**

1. **MCP Client role — missing.** Requirement: "able to act as an MCP Client". No code: `internal/mcpserver/` contains only `server.go` (no `client.go`). The requirement text was already changed to state that this is backlog, so the *requirement text* now matches — but the capability is absent.
2. **`test-connection` "before being stored permanently" — partial.** The route `POST /accounts/{id}/test-connection` (`internal/api/server.go:66`) needs an id that already exists; `Service.TestConnection` loads the account via `repo.Get`. It cannot validate before save — you must create first. There is no deviation note.
3. **Email body caching — partial.** Requirement: "Email body & metadata (subject, from, to, date, attachment list) are stored/cached". Only metadata + attachment **names** are cached; there is no body column in `migrations/0001`/`0002`.
4. **MCP `send_email` drops cc/bcc/attachments.** The requirement mandates this; `sendEmailArgs` (`internal/mcpserver/server.go:145-152`) has no such fields, even though REST (`internal/api/send_handler.go`) does. This contradicts the repo premise "REST and MCP are identical" (`CLAUDE.md`).

**(b) Scope creep (not requested; only partially reconciled so far): 2.**

5. **A second release script.** The requirement said "via **one** script, `scripts/release.sh`" — the repo added `scripts/release.ps1` (ARCHITECTURE/README updated, the requirement text not).
6. **`/healthz` without auth.** The requirement said "**All** requests need the `X-API-Key` header"; `internal/api/middleware.go:19` exempts `/healthz` (route `server.go:44`). It is recorded in the project docs but contradicts the requirement.

**(c) Implemented but wrong / doc↔code drift: 2.**

7. **`.env`/`godotenv` is dead.** The documented decision locks in "`.env` optional … via `godotenv`"; there is no `godotenv` in `go.mod` and `internal/config/config.go:38` is just `os.Getenv`. `README.MD:56` tells you to `cp .env.example .env && make run` — a setup step that does not work. (**Worst on this axis.**)
8. **Stale tracking claim.** A project-doc tracking entry still lists `ExternalToolCaller` as an implemented requirement, while a later entry records its removal — `internal/mcpserver/client.go` does indeed not exist. The archive table still reads as if it were "done".

## Summary

- **Standards** — **7 findings: 3 hard, 4 judgement.** Worst: the installed Windows Service is not given env vars, so `XMAIL_*` is absent and the process exits at startup (`internal/winservice/service.go:60`, `cmd/xmail-tray/main.go:45`).
- **Spec** — **8 findings: 4 missing/partial, 2 scope-creep, 2 doc/code drift.** Worst: `.env`/`godotenv` is documented as a working config path but the code does not exist (`internal/config/config.go:38`, `README.MD:56`).

The two axes are deliberately not merged/re-ranked. The `.env`/`godotenv` drift was found **independently on both axes**, so it is the most worthwhile next target.

---

# Re-verification (after the round-4 findings were fixed)

Direct verification against the code (rather than trusting document claims) of the fixes recorded in the round-4 resolution notes. The fixed point `66ebca7` is unchanged.

**Technical baseline (re-measured, all green):** `go build`/`go vet` (default and `-tags xmailtray`) clean; `go test ./...` = **71 `=== RUN`**; `go test -tags integration ./...` = **90**; `go test -tags xmailtray ./internal/winservice/` = **1**. All three match the recorded claims exactly — no number was overstated.

## Standards — status per finding

| # | Finding | Status |
|---|---|---|
| 1 | No `.env` loader | **Fixed** — `github.com/joho/godotenv` in `go.mod`, `config.Load` calls `godotenv.Load()` (best-effort, does not override the real environment). Locked by `TestLoad_ReadsDotEnv` + `TestLoad_RealEnvOverridesDotEnv`; `README.MD`/`ARCHITECTURE.md` were updated too |
| 2 | Windows Service without `EnvVars` | **Fixed** — `buildServiceConfig` fills in `EnvVars` (re-encoding `EncryptionKey` to base64); regression test `TestBuildServiceConfig_EnvVarsRoundTrip` simulates an SCM process with an empty environment. **But see new finding A** |
| 3 | Requirement text out of sync | **Fixed** — the API section now includes `GET /healthz`; the build section now says "two equivalent scripts" |
| 4 | Duplicated `folder == ""` guard | **Fixed** — `mailer.DefaultFolder` in `internal/mailer/types.go`, used in 6 places. The remaining `"INBOX"` literal in `pop3/client.go` is legitimate (POP3 has no folders) |
| 5 | `queryOr` dead code | **Fixed** — deleted; `queryIntOr`/`queryBool` are used |
| 6 | `timeFormat` re-writing `time.RFC3339` | **Fixed** — `dto.go` uses `time.RFC3339` directly |
| 7 | Hardcoded `serverVersion` | **Fixed** — `version` is threaded `cmd/*` → `app.Run(ctx,cfg,version)` → `mcpserver.New(svc,version)` (and `winservice.New`) |

## Spec — status per finding

| # | Finding | Status |
|---|---|---|
| 1 | MCP Client role | **Backlog (documented)** — the requirements state that this is full backlog, with no stub. The stale tracking claim was struck through & redirected to the removal note |
| 2 | `test-connection` before save | **Documented** — a design-tension note in the requirements + the reason it was not changed. The "before being stored permanently" claim was corrected |
| 3 | Email body caching | **Spec clarified** — the requirement now reads "**Email metadata**", explicitly stating the body is not cached. The spec now matches the code |
| 4 | MCP `send_email` dropping cc/bcc | **Fixed** — `sendEmailArgs.CC/BCC` + schema + passed through to `OutgoingMessage`, asserted by `TestHandleSendEmail` (`server_test.go:109-129`). Attachments remain REST-only, deliberate & documented (decision note, code, skill) |
| 5 | Scope creep `release.ps1` | **Fixed** — the requirements were updated |
| 6 | `/healthz` without auth | **Fixed** — the requirements now list `/healthz` + "no auth" |
| 7 | `.env`/`godotenv` dead | **Fixed** — same as Standards #1 |
| 8 | Stale `ExternalToolCaller` tracking claim | **Fixed** — the line was struck through and redirected to the removal note |

## New findings / leftovers

1. **(Latent, medium) The `EnvVars` fix is necessary but not sufficient for a service that actually runs.** `buildServiceConfig` passes `XMAIL_DB_PATH` through as-is, and its default is relative (`xmail.db`, see `.env.example:2`). The Windows Service Control Manager launches a service with a working directory of `%SystemRoot%\System32`, so a service installed with the default path will try to create/open `xmail.db` in `System32` — likely failing (`Access denied`) or using a different DB than the tray. `service.Config.WorkingDirectory` **cannot** be the patch: `go doc github.com/kardianos/service.Config` states that field is *"not supported on Windows"*. The remedy: absolutize `DBPath` when writing `EnvVars` (or document that `XMAIL_DB_PATH` must be absolute in service mode). The new tests do not catch this (they only round-trip 4 vars, they do not check the file location). It could not be verified at runtime in this session (installing a service requires Administrator).
2. **(Minor) `XMAIL_MCP_STDIO` is not propagated.** `EnvVars` only carries 4 vars; a service installed from a tray session with `XMAIL_MCP_STDIO=true` will silently lose that setting.
3. **(Minor, doc) Review-round numbering is inconsistent.** `README.MD` says "**3 rounds** of code review ... 25 findings in total" and the resolution notes are titled "**third** round" — whereas `CODE_REVIEW.md` now has **4** review sections (initial, round 2, round 3, round 4), and the round-4 findings are the very ones labeled "third round" in the resolution notes. The old "round 3" section and "round 4" refer to different rounds than the resolution-count convention. A single convention is needed (and `README.MD` is now stale because the 4th review actually happened).
4. **(Minor, doc) The opening paragraph of `ARCHITECTURE.md` §8** still summarizes only "two review rounds" (line 222) even though a later entry already details the third round at line 225.

## Summary

- **Standards:** all 7 round-4 findings **fixed** (verified in code + tests). **Spec:** all 8 **closed** (5 fixed in code/docs, 2 documented as backlog/design tension, 1 clarified in the spec so that it matches the code). No functional regressions.
- These fixes introduced **1 genuine new finding** (relative DB path on the Windows Service, #A) + 3 minor ones (1 small functional, 2 document drift).
- Worst: **#A** — the `EnvVars` fix points in the right direction, but a service installed with the default configuration can still fail to start because `XMAIL_DB_PATH` is relative to `System32`.

**Resolution**:

- **#A fixed** — `buildServiceConfig` now absolutizes `XMAIL_DB_PATH` (`filepath.Abs`) before writing it into `EnvVars`; regression test `TestBuildServiceConfig_AbsolutizesDBPath` (asserts an absolute path + still ending in `xmail.db`). Note: `filepath.Abs` uses the cwd of the interactive process at install time, not the exe directory — the foreground tray & the service are consistent as long as they are run from the same directory.
- **#B fixed** — `XMAIL_MCP_STDIO` is propagated (`strconv.FormatBool(cfg.MCPStdioEnable)`); asserted in `TestBuildServiceConfig_EnvVarsRoundTrip`.
- **#C/#D fixed** — `README.MD` no longer mentions fragile round/finding counts; the opening paragraph of `ARCHITECTURE.md` §8 was updated; the mapping between `CODE_REVIEW.md` (review sessions) numbering and the resolution-cycle numbering is recorded explicitly.

Final verification: `go build`/`go vet` (default + `-tags xmailtray`) clean; `go test ./...` green; `go test -tags integration ./...` green; `go test -tags xmailtray ./internal/winservice/` = 2 green.

---

# Two-Axis Review (round 5) — worktree vs `main`

A full two-axis review run from scratch, both axes by separate sub-agents. Fixed point: `main` (= `66ebca7` "Init empty repo"). Diff command: `git diff main` — it covers the implementation commit `a982ff0` plus uncommitted working-tree changes (`internal/winservice/service.go`, `internal/winservice/service_test.go`, `ARCHITECTURE.md`, `CODE_REVIEW.md`, `README.MD`).

- **Spec sources:** `ARCHITECTURE.md`, `CONTEXT.md`, `README.MD`, `docs/API.md`, `docs/MCP_CLIENT_GUIDE.md`.
- **Standards sources:** `CLAUDE.md`, `ARCHITECTURE.md`, `.commandcode/taste/*.md`, plus Fowler's smell baseline.
- **Stance:** the round 1-4 archive in `CODE_REVIEW.md` is **not** trusted; every "fixed" claim was re-verified against the code. The archive was used only as a checklist.
- **Technical baseline (re-verified):** `go build ./...`, `go vet ./...` (+ `-tags xmailtray`) clean; `go test ./...` = 71 `=== RUN`; `go test -tags integration ./...` = 90; `go test -tags xmailtray ./internal/winservice/` = 2.

## Standards

**Documented standard violations (hard): 2.**

1. **The MCP tool contract documented in the project docs is stale vs the code.** `internal/mcpserver/server.go`: `send_email` (`sendEmailArgs` L145-152, added in a later fix round) has `cc`/`bcc` but the contract table does not mention them; `fetch_emails` accepts `protocol`; `check_new_emails` (`checkNewEmailsArgs`) accepts `protocol`/`folder` even though the contract says its arguments are only `{account_id}`. (Taste: docs are updated when features change; round 4 treated doc drift as hard.)
2. **`/healthz` violates the envelope contract.** The documented rule: "All responses: `{data, error}`"; `ARCHITECTURE.md` §3.4: "without exception". `internal/api/server.go:37-40` writes raw `ok`. *Borderline* — a conventional liveness probe, but the same blanket rule was used in round 1 to flag the `DELETE` 204.

**Smell baseline (all judgement calls): 4.**

- **Duplicated Code (minor)** — the default `limit` is applied per adapter: `internal/api/messages_handler.go:30` (`queryIntOr(..., account.DefaultFetchLimit)`) vs `internal/mcpserver/server.go` (`if limit <= 0 { limit = account.DefaultFetchLimit }`). Same value, duplicated shape.
- **Mysterious Name / doc drift** — the `resolveFetcher` comment says it returns "…plus its decrypted secret", but its signature is `(fetcher, resolvedProtocol, err)` with no secret (`internal/account/service.go:255`).
- **Non-deterministic error order** — `Validate` iterates a `map[string]*ConnectionConfig{…}` (`service.go:180`); map order is random, so which protocol's host/port error surfaces can vary.
- **Comment drift** — the comment "defaultFetchLimit bounds…" sits above `const defaultCheckFetchLimit` (`service.go:96`).

**Suppressed** (documented repo standards outrank the baseline): Repeated Switches on `protocol string`; Data Clumps `(cfg,username,secret)`; MCP `send_email` attachments REST-only (a recorded deliberate decision).

**Verified compliant** (checked against the code, not trusted from the archive): the `mailer.DefaultFolder`/`WindowRange` de-duplication is real (the only remaining `"INBOX"` literal is `pop3/client.go:110`, which is legitimate); `queryOr` is gone; `timeFormat`→`time.RFC3339`; `serverVersion` is now threaded `cmd → app.Run(ctx,cfg,version) → mcpserver.New`; `connFields` collapses the three nil-guards; the concrete mailer packages are imported only by `internal/app`; no business logic in `cmd/`; build tags correct; no testify; no `InsecureSkipVerify`; MCP schemas declared explicitly; logs contain no email bodies. `EnvVars`, `DBPath` absolutization, and `XMAIL_MCP_STDIO` propagation in winservice exist and are asserted by tests.

## Spec

**(a) Missing / partial requirements: 3.**

1. **MCP Client role — missing.** Requirement: "…and also able to act as an MCP Client…"; and "the service's ability to call an external MCP server". No code (no `mcpserver/client.go`). The requirement is now labeled "Full backlog", so the requirement text matches, but the capability is absent.
2. **MCP `send_email` drops attachments — partial.** Requirement: "Support attachments, HTML & plain text body, CC/BCC, basic custom headers." REST `sendRequest` carries `Attachments`; MCP `sendEmailArgs` has none. Deliberate (a recorded decision), but the MCP surface is therefore a partial realization.
3. **`test-connection` "before being stored permanently" — partial.** Route `POST /accounts/{id}/test-connection` calls `repo.Get`, so it can only validate an account that is already saved.

**(b) Scope creep (outside the original spec sketch): 2.**

- `GET /healthz`, the tray's "Open dashboard", the MCP stdio transport (`XMAIL_MCP_STDIO`), and `scripts/release.ps1` — most of these were later retro-documented into the requirements.
- `CLAUDE.md` and `.claude/skills/xmail/SKILL.md` — absent from the documented project structure; artifacts that were not requested.

**(c) Implemented but wrong: 3.**

1. **🔴 Cache-first pins the window (verified first-hand).** `Service.FetchMessages` (`internal/account/service.go:345-353`) returns cache rows whenever `len(cached) > 0` and **never** dials the server. After a `limit=20` cache, `GET /messages?limit=50` returns 20 rows without contacting the server — a silent truncation against the requirement "Fetch the email list (with pagination…)". (**Worst on this axis.**)
2. **`/healthz` is not JSON.** `internal/api/server.go:37-40` writes plain `"ok"`, not an envelope — contradicting the requirement "JSON response format consistent with the `{data, error}` envelope."
3. **The `imap.Fetch` comment is wrong.** The comment says "sort newest-first by UID" while the code only reverses the buffer slice (not a UID sort); the newest-first ordering guarantee silently depends on the server returning ascending order.

## Summary

- **Standards — 6 findings (2 hard, 4 judgement).** Worst: the MCP tool contract documented in the project docs is stale vs the API actually sent (`internal/mcpserver/server.go`).
- **Spec — 8 findings (3 missing/partial, 2 scope creep, 3 implemented-but-wrong).** Worst: cache-first pins the window — a larger `?limit` is silently truncated to the old cache size (`internal/account/service.go:345`).

The two axes are deliberately not merged/re-ranked. `/healthz` (envelope violation) appeared **independently on both axes**, so it is a mutually confirmed finding and, together with the cache-first bug, the most worthwhile next target.

---

# Re-verification (round 6 — after the round-5 fixes)

Direct verification against the code (rather than trusting document claims) of the fixes for the round-5 findings. The fixed point `main` (`66ebca7`) is unchanged.

**Changes reviewed:** `internal/account/service.go`, `internal/account/messages.go`, `internal/account/messages_test.go`, `internal/api/server.go`, `internal/api/messages_handler.go`, `internal/api/server_test.go`, `internal/mailer/imap/client.go`, `internal/mcpserver/server.go`, new migration `internal/storage/migrations/0004_message_cache_state.sql`, plus `ARCHITECTURE.md`.

**Technical baseline (re-measured, all green):** `go build`/`go vet` (default + `-tags xmailtray`) clean; `go test ./...` = **74 `=== RUN`** (up from 71); `go test -tags integration ./...` = **93** (up from 90); `go test -tags xmailtray ./internal/winservice/` = 2.

## Standards — status per finding

| # | Finding | Status |
|---|---|---|
| 1 | Stale MCP tool contract (`send_email` cc/bcc, `fetch_emails` protocol, `check_new_emails` protocol/folder) | **Fixed** — the contract table was synced with `internal/mcpserver/server.go` |
| 2 | `/healthz` violates the `{data,error}` envelope | **Fixed** — `writeData(w, 200, {"status":"ok"})`; `TestHealthz_NoAuthRequired` asserts `data.status` + `error:null` |
| 3 | Duplicated default `limit` (REST vs MCP) + explicit `limit=0` behaving differently | **Fixed** — normalization moved into `Service.FetchMessages`; both adapters pass values through as-is. Test `TestService_FetchMessages_ZeroLimitUsesDefault` |
| 4 | `resolveFetcher` comment claiming a secret | **Fixed** — the claim was removed, the comment merged |
| 5 | `Validate` iterating a map → non-deterministic error order | **Fixed** — an ordered slice keeps smtp→imap→pop3 |
| 6 | Stray `defaultFetchLimit` comment | **Fixed** — it now points at `defaultCheckFetchLimit` |

## Spec — status per finding

| # | Finding | Status |
|---|---|---|
| 1 | MCP Client role | **Backlog (unchanged)** — the requirements already state that this is full backlog; not implemented |
| 2 | MCP `send_email` drops attachments | **Unchanged (deliberate)** — the requirement is met via REST; UX rationale recorded in the decision notes; the contract now states this explicitly |
| 3 | `test-connection` before save | **Unchanged (deliberate)** — a documented design tension in the requirements |
| 4 | 🔴 Cache-first pins the window | **Fixed** — `messages_cache_state` (coverage/exhausted) + `CacheState`/`RecordFetch`; `FetchMessages` serves cache only if `exhausted` or `offset+limit <= coverage`. Regression test `TestService_FetchMessages_LargerWindowRefetches` + `TestRepository_CacheState`. Confirmed from the code: a 30-message window is now re-fetched for `limit=50`, and it does not re-fetch after the mailbox is exhausted |
| 5 | `/healthz` is not JSON | **Fixed** — same as Standards #2 |
| 6 | `imap.Fetch` comment "sort by UID" vs reality | **Fixed** — it now really does `sort.Slice` descending by UID |
| 7 | Scope creep (`healthz`/tray/stdio/release.ps1) | **Already documented** in the project docs |
| 8 | Scope creep from agent artifacts (`CLAUDE.md`, `.claude/skills/xmail/SKILL.md`) | **Documented** in the project docs |

## New findings / leftovers

No new functional regressions. Notes:

1. **(Minor, design)** `messages_cache_state` uses a monotonically increasing `coverage` plus an `exhausted` latch. If the mailbox grows larger after `exhausted` is set, the cache is still considered able to answer until a `refresh=true`/`CheckNew` happens — this is genuinely the cache semantics (not a stale-cache bug), but worth remembering when changing invalidation.
2. **(Minor)** `go test -race` has still never been run (no cgo toolchain) — pre-existing, not a regression.

## Summary

- **Standards:** all 6 round-5 findings **fixed** (verified in code + tests).
- **Spec:** 3 bugs/drift **fixed** (cache window, healthz JSON, imap comment) + 3 items that are genuinely backlog/conscious decisions (unchanged, and the contract now states them explicitly) + 2 documented scope-creep items.
- **Worst thing fixed:** the cache window bug — a request with a larger `?limit` is no longer silently cut to the old cache page, guarded by regression tests at the Service and Repository level.
