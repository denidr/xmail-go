---
name: xmail
description: >-
  Send and receive email through a running xmail instance — a self-hosted
  email-as-a-service backend that exposes the same operations over a REST API
  (base URL + X-API-Key header) and over MCP tools. Use when the user asks to
  send an email, check or preview an inbox, fetch or summarize messages, list
  folders, list email accounts, or add/update/delete an account — either as
  plain instructions ("send an email", "check my inbox", "do I have new mail",
  "list email accounts") or when MCP tools named list_accounts, send_email,
  fetch_emails, check_new_emails, or list_folders are already in your tool list.
  Contains the full endpoint list, request/response shapes, error codes, fetch
  cache semantics, TLS/provider settings, and the five MCP tools.
---

# xmail — send, fetch, and check email

xmail is a self-hosted email backend for one or more accounts. Every operation is
available two ways — a **REST API** and an **MCP server** — backed by the same logic, so
the outcome is equivalent whichever you use. This skill covers *using* a running instance:
which interface to reach for and the exact shapes to send.

**Capabilities per protocol** (an account may configure only some):

| Protocol | Send | Fetch | Check (unread) | Mark read | Attachments | Folders |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| SMTP | yes | — | — | — | — | — |
| IMAP | — | yes | yes | yes | yes (names) | many |
| POP3 | — | yes | no (`unread_count` always 0) | no | always empty | `INBOX` only |

## Quick start

1. Decide which interface you have (§1).
2. Get the base URL and API key (§2) — needed for everything except `GET /healthz`.
3. Pick the operation (§3 lists every endpoint; §7 has the MCP tools).
4. On error, read `error.code` (§2) before retrying; §10 covers common causes.

## 1. Work out how you're connected

- **MCP tools already in your tool list?** Look for `list_accounts`, `send_email`,
  `fetch_emails`, `check_new_emails`, `list_folders`. If present, you are connected to
  xmail's MCP server — use §7 for those five operations. No auth header, no base URL.
- **No MCP tools, or you need account management?** Use REST (§3). MCP exposes only those
  five tools; account CRUD and connection tests are **REST-only** (§8).
- You can mix both: manage accounts over REST, then send/fetch over MCP.

## 2. Connection, auth & errors

Get the base URL and API key from the user or the environment — never guess or invent one:

- **Base URL**: `http://localhost:5569` is the default; an instance may listen elsewhere
  (its `XMAIL_LISTEN_ADDR`). Ask if unsure.
- **Auth**: header `X-API-Key: <key>` on every request **except** `GET /healthz`. The key is
  the server's `XMAIL_API_KEY`. There is no other auth (no OAuth, no multi-key).
- **Envelope**: every response — success or failure, including `DELETE` — is
  `{"data": …, "error": null}` or `{"data": null, "error": {"code": …, "message": …}}`.
  Always check `error` before trusting `data`.
- **Strict JSON body**: the decoder rejects unknown fields. A capitalized or misspelled
  field (`Subject` instead of `subject`) fails with `invalid_json` (400).

```bash
curl -s "$BASE_URL/healthz"     # -> {"data":{"status":"ok"},"error":null}   (no auth)
```

| `error.code` | HTTP | Cause |
|---|---|---|
| `unauthorized` | 401 | Missing or wrong `X-API-Key` |
| `invalid_json` | 400 | Body is not valid JSON, or contains an unknown field (strict decoder) |
| `validation_failed` | 400 | Empty required field, bad `tls_mode`, POP3 `starttls`, empty `uid`, unsupported operation for that protocol, protocol not configured, **folder does not exist** |
| `not_found` | 404 | `account_id` does not exist (including after deletion) |
| `internal_error` | 500 | Dial/login/send/DB failure — `error.message` carries the original protocol-library text (e.g. `smtp: send: …`, `imap: login: …`) |

Input mistakes are `validation_failed`; everything else that fails downstream is
`internal_error`. The instance also serves a small web dashboard at its base URL for
account management (browser, same API key) — it is just a client of these endpoints.

## 3. Endpoint reference

| Method | Path | Auth | Purpose | Success |
|---|---|:---:|---|---|
| GET | `/healthz` | no | Health check | `200` |
| GET | any non-API path (`/`, `/app.js`, `/style.css`) | no | Web dashboard static assets | `200` |
| POST | `/accounts` | yes | Create account | `201` |
| GET | `/accounts` | yes | List accounts (sorted by name) | `200` |
| GET | `/accounts/{id}` | yes | Account detail | `200` / `404` |
| PUT | `/accounts/{id}` | yes | Update account (**full replace**) | `200` |
| DELETE | `/accounts/{id}` | yes | Delete account (+ its cache) | `200` |
| POST | `/accounts/{id}/test-connection` | yes | Test connect+login | `200` |
| POST | `/accounts/{id}/send` | yes | Send email | `200` |
| GET | `/accounts/{id}/folders` | yes | List folders (IMAP only) | `200` |
| GET | `/accounts/{id}/messages` | yes | Fetch messages (cache-first) | `200` |
| POST | `/accounts/{id}/check` | yes | Check for new email | `200` |
| POST | `/accounts/{id}/messages/read` | yes | Mark as read (IMAP only) | `200` |
| POST | `/mcp` | yes | MCP server (Streamable HTTP) | — |

### 3.1 Accounts

Create — `name`, `email`, `username`, `password` required; at least one of
`smtp`/`imap`/`pop3` required. Response is the account **without** credentials:

```bash
curl -s -X POST "$BASE_URL/accounts" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{
  "name": "Work Gmail", "email": "me@example.com", "username": "me@example.com",
  "password": "app-password-here",
  "smtp": {"host": "smtp.gmail.com", "port": 587, "tls_mode": "starttls"},
  "imap": {"host": "imap.gmail.com", "port": 993, "tls_mode": "tls"}
}'
```

List / get / delete:

```bash
curl -s "$BASE_URL/accounts" -H "X-API-Key: $API_KEY"
curl -s "$BASE_URL/accounts/$ACCOUNT_ID" -H "X-API-Key: $API_KEY"
curl -s -X DELETE "$BASE_URL/accounts/$ACCOUNT_ID" -H "X-API-Key: $API_KEY"   # -> {"data":{"deleted":true},"error":null}
```

Update — **`PUT` is a full replace, not a patch and not a merge.** Every field except
`password` must be sent again; any protocol block you omit is set to `null` (removing that
protocol). `password` is the only optional field: send a string to change it, omit it or send
`null` to keep the old credentials.

```bash
# rename and drop the pop3 config, keep credentials:
curl -s -X PUT "$BASE_URL/accounts/$ACCOUNT_ID" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{
  "name": "Work Gmail (renamed)", "email": "me@example.com", "username": "me@example.com",
  "imap": {"host": "imap.gmail.com", "port": 993, "tls_mode": "tls"}
}'   # smtp & pop3 not sent -> become null
```

Test a connection before trusting an account (connects + logs in, sends/fetches nothing).
`protocol` defaults to `imap`; response is `{"data":{"ok":true},"error":null}`:

```bash
curl -s -X POST "$BASE_URL/accounts/$ACCOUNT_ID/test-connection" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{"protocol": "smtp"}'
```

### 3.2 Send an email

`POST /accounts/{id}/send` → `{"data":{"status":"sent"},"error":null}`.

| Field | Required | Notes |
|---|:---:|---|
| `to` | yes | array of emails, at least 1 |
| `cc` / `bcc` | no | array of emails |
| `subject` | no | |
| `body_text` | no | plain-text body |
| `body_html` | no | HTML body |
| `attachments` | no | `{filename, content_type, data_base64}` — see below |
| `headers` | no | string→string map of extra headers (e.g. `X-Priority`, `Reply-To`). Standard headers (From/To/Cc/Bcc/Subject/Content-Type) are managed by the server and cannot be overridden |

```bash
curl -s -X POST "$BASE_URL/accounts/$ACCOUNT_ID/send" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{
  "to": ["bob@example.com"], "cc": ["carol@example.com"],
  "subject": "Laporan", "body_text": "Terlampir laporan.", "body_html": "<p>Terlampir laporan.</p>",
  "headers": {"X-Priority": "1"},
  "attachments": [{"filename": "laporan.pdf", "content_type": "application/pdf", "data_base64": "JVBERi0xLjQK..."}]
}'
```

- **From** is always `account.email` — it cannot be overridden via the body.
- Both `body_html` **and** `body_text` set → `multipart/alternative` (HTML primary, text
  alternative). Only `body_html` → HTML. Otherwise plain text.
- Attachment bytes are **standard base64** in `data_base64` (invalid base64 → 400).
  `content_type` is accepted but **not used** — the MIME type is derived from `filename`.
- Status: `200`; `400` (no `to`, bad base64, unknown field); `404`; `500` (dial/auth/From rejected).

### 3.3 Fetch messages

`GET /accounts/{id}/messages` → array of Message, **newest first**.

| Query | Default | Meaning |
|---|---|---|
| `limit` | `20` | max messages returned (`<= 0` → 20) |
| `offset` | `0` | skip N **newest** messages |
| `folder` | `INBOX` | IMAP folder (POP3 ignores it, always `INBOX`) |
| `protocol` | `imap` | `imap` \| `pop3` |
| `refresh` | `false` | `true`/`1` = force a live dial, ignore the cache |

```bash
curl -s "$BASE_URL/accounts/$ACCOUNT_ID/messages?protocol=imap&folder=INBOX&limit=5&refresh=true" -H "X-API-Key: $API_KEY"
```

`offset=0` is the newest; `offset=20&limit=20` is ranks 21–40; an offset past the end is `[]`.
See §4 for the cache behavior. Per-protocol differences:

| | IMAP | POP3 |
|---|---|---|
| Sort | descending UID (newest first) | UIDL order, reversed |
| `is_read` | from the `\Seen` flag | always `true` |
| `attachments` | file names from BODYSTRUCTURE | always empty |
| `folder` | the requested folder | always `INBOX` |
| Body | not downloaded (metadata only) | not downloaded (headers only) |

Status: `200` (including `[]`); `400` when `protocol` is `smtp`/invalid, the protocol is not
configured, or `folder` does not exist; `404`; `500` dial/fetch failure.

### 3.4 Check for new email

`POST /accounts/{id}/check`, body `{"protocol":"imap","folder":"INBOX"}` (both optional,
defaults `imap` / `INBOX`) → `{"data":{"unread_count":5,"new_count":2},"error":null}`.

- `unread_count` — unread reported by the server (IMAP `STATUS ... UNSEEN`). POP3 → `0`.
- `new_count` — how many of the **50 newest** messages are not yet in xmail's cache (it is a
  local cache computation, not a protocol concept). Not the whole mailbox.
- This endpoint **always dials** and fills the cache for the 50 newest messages, so a later
  `/messages` within that range can be served from cache.

Status: `200`; `400` invalid/unconfigured protocol; `404`; `500` dial failure.

### 3.5 Folders

`GET /accounts/{id}/folders?protocol=imap` → array of Folder, sorted by name. Always live
(never cached). **Use this instead of guessing folder names** — IMAP names are
server/locale-specific (an Indonesian Gmail exposes `[Gmail]/Surat Terkirim`, not
`[Gmail]/Sent Mail`). `attributes` carries role hints (`\Sent`, `\Drafts`, `\Noselect`,
`\HasChildren`); `delimiter` and `attributes` are omitted when the server reports NIL/empty.
`\Noselect` entries are returned as-is (not filtered). They cannot be `SELECT`ed, so fetching
one fails — normally `400` (the server reports NONEXISTENT/TRYCREATE), though an unusual
server-side rejection can surface as `500`. IMAP only — `pop3`/`smtp` → `400 validation_failed`.

```json
{ "data": [
    { "name": "INBOX", "delimiter": "/" },
    { "name": "[Gmail]/Surat Terkirim", "delimiter": "/", "attributes": ["\\Sent", "\\HasNoChildren"] }
  ], "error": null }
```

### 3.6 Mark as read

`POST /accounts/{id}/messages/read`, body `{"uid":"1042","protocol":"imap","folder":"INBOX"}`
— `uid` required, `protocol`/`folder` optional → `{"data":{"marked_read":true},"error":null}`.

**IMAP only** (sets the `\Seen` flag). `protocol=pop3` → `400` (POP3 has no per-message
flags). The local cache is updated too, so cached fetches stay consistent.

## 4. Fetch cache semantics (the most misunderstood part)

Fetching is **cache-first**: by default xmail answers from its local cache if the cache
already *covers* the requested window (no dial); otherwise it dials, stores, then replies.
`refresh=true` always dials. Consequences:

- Two identical requests → the second does not dial.
- Requesting a **larger** `limit` than before (e.g. `50` after `20`) does not fit the cached
  coverage → it **dials** (it will not silently truncate).
- `POST /check` always dials and fills the 50 newest.
- The cache is keyed by `(account, protocol, folder)`; `DELETE /accounts/{id}` clears it.
- If the server reports the top of the mailbox changed (new mail or deletions), xmail drops
  that cache key rather than serving shifted/stale rows. When in doubt, use `refresh=true`:
  a cache hit is only ever as fresh as the last dial.

## 5. TLS modes & provider examples

`tls_mode` for every protocol connection is required and one of:

| Value | Meaning | Typical port |
|---|---|---|
| `tls` | implicit TLS | SMTP 465, IMAP 993, POP3 995 |
| `starttls` | plaintext then upgrade (must succeed) | SMTP 587, IMAP 143 |
| `none` | no TLS — must be chosen explicitly | local/test |

Rules: `port` must be 1–65535; **`pop3.tls_mode: "starttls"` is rejected** (use `tls` or
`none`); certificate validation is on by default. xmail has **no OAuth2** — accounts that
require it must use an app-password.

| Provider | SMTP | IMAP | POP3 |
|---|---|---|---|
| Gmail | `smtp.gmail.com:587`/`starttls` | `imap.gmail.com:993`/`tls` | `pop.gmail.com:995`/`tls` |
| Microsoft 365 / Outlook | `smtp.office365.com:587`/`starttls` | `outlook.office365.com:993`/`tls` | usually disabled |
| Yahoo | `smtp.mail.yahoo.com:465`/`tls` | `imap.mail.yahoo.com:993`/`tls` | `pop.mail.yahoo.com:995`/`tls` |
| Local/dev (MailHog) | `localhost:1025`/`none` | `localhost:1143`/`none` | `localhost:1110`/`none` |

## 6. Data types

**Account request** (`POST`/`PUT /accounts`): `name`, `email`, `username` (strings),
`password` (string; required on create, optional on update — `null`/omitted keeps it),
`smtp`/`imap`/`pop3` (`Connection` or `null`; at least one non-null on create). `email`
becomes the From address when sending.

**Account response**: `{id, name, email, username, smtp, imap, pop3, created_at, updated_at}`
— **never** contains credentials. `Connection` = `{host, port, tls_mode}`; a `null` protocol
means it is not configured.

**Message**: `{"uid","folder","subject","from","to","date","is_read","attachments"}` —
`from`/`to` are the first address only; `date` is RFC3339 UTC; `attachments` is a list of
**file names only** and is omitted when empty. Metadata only, never the body.

**Send request**: `to` (required), `cc`, `bcc`, `subject`, `body_text`, `body_html`,
`attachments` (`[{filename, content_type, data_base64}]`), `headers` (string→string).

**Check result**: `{unread_count, new_count}`.

**Folder**: `{name, delimiter?, attributes?}` — use `name` verbatim as the `folder` query.

## 7. MCP tools

When present in your tool list, use them directly — same effect as the REST calls, structured.
These are the exact argument names.

| Tool | Arguments | Notes |
|---|---|---|
| `list_accounts` | *(none)* | Returns `[{id, name, email}]` — never credentials. Get an `account_id` here first. |
| `send_email` | `account_id` (req), `to` (req, string[]), `subject` (req), `cc`, `bcc`, `body_text`, `body_html`, `headers` | No attachments over MCP (REST-only). |
| `fetch_emails` | `account_id` (req), `protocol` (default `imap`), `folder` (default `INBOX`), `limit` (default 20), `refresh` (default false) | Same Message objects as REST. No `offset` — that is REST-only. |
| `check_new_emails` | `account_id` (req), `protocol` (default `imap`), `folder` (default `INBOX`) | Returns `{unread_count, new_count}`. |
| `list_folders` | `account_id` (req), `protocol` (default `imap`) | `[{name, delimiter?, attributes?}]`. IMAP only; `pop3`/`smtp` → `isError: true`. |

A tool that fails on validation or domain grounds (bad argument, unknown account, dial failure)
returns `isError: true` with a message — check it instead of assuming success; a malformed
request instead yields a JSON-RPC `error`. The MCP server is reachable over Streamable HTTP at
`POST /mcp` (same base URL, same `X-API-Key`; stateless, and the `Accept` header must allow
`application/json` and/or `text/event-stream`), or over stdio when the server runs with
`XMAIL_MCP_STDIO=true`.

## 8. REST-only vs MCP-only

- **REST-only**: create / update / delete an account, test a connection, send attachments,
  and mark a message as read. No MCP tool exists for these.
- **MCP-only**: nothing — every MCP tool has a REST equivalent.
- Asked to "set up a new email account" with only MCP access? Say so: that step needs REST (or
  the user doing it in the dashboard). Once the account exists, MCP covers day-to-day use.

## 9. Common recipes

**Add an account, then send a test email**
1. `POST /accounts` (§3.1) → read `data.id`.
2. `POST /accounts/{id}/test-connection` with `{"protocol":"smtp"}` → confirm `ok` first.
3. `send_email` (MCP) or `POST /accounts/{id}/send` (REST) with that id.

**"Do I have new mail?" across every account**
1. `list_accounts` (MCP) or `GET /accounts` (REST) to enumerate.
2. `check_new_emails` / `POST .../check` per account — sum `unread_count` / `new_count`.

**Summarize the last N emails**
`fetch_emails` (or `GET .../messages?limit=N`) → the response already has `subject`, `from`,
`date`, `is_read`; there are no bodies to fetch (metadata only).

**Fetch from a folder whose exact name you don't know (e.g. Sent)**
1. `list_folders` (MCP) or `GET .../folders` (REST) → pick the entry whose `attributes`
   contains `\Sent`, or read its `name`.
2. Fetch with `folder` set to that exact `name`. Avoid `\Noselect` entries (they `400`), and
   don't guess localized names like `[Gmail]/Sent Mail`.

**Get genuinely fresh mail**
Add `refresh=true` to `/messages` (or `refresh: true` on `fetch_emails`) — otherwise you may
get cached rows from an earlier dial.

## 10. Troubleshooting

| Symptom | Cause & fix |
|---|---|
| `401 unauthorized` | Wrong/missing `X-API-Key`; match the server's `XMAIL_API_KEY`. |
| `400 invalid_json` though the JSON looks fine | An unknown field, or a field name with the wrong case (strict decoder). |
| `400 ... at least one of smtp, imap, pop3 must be configured` | Send at least one protocol block. |
| `400 ... pop3.tls_mode "starttls" is not supported` | POP3 allows only `tls` or `none`. |
| Fields vanish after `PUT` | `PUT` is a full replace; unsent protocol blocks become `null`. |
| `500` on `/messages` or `/check` | Server unreachable, wrong credentials, or protocol not configured — run `/test-connection` first. |
| `500` on `/send` | Usually SMTP auth failed or the From (`account.email`) was rejected. |
| Second fetch "doesn't update" | Cache-first behavior; use `refresh=true`. |
| `new_count` is 0 but there is new mail | It only counts the 50 newest vs the local cache. |
| Attachment type looks odd | `content_type` is ignored; the type comes from the `filename` extension. |
| Mark-read fails | You are on POP3, which has no flags — use IMAP. |
