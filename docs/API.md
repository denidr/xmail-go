# xmail — REST API Reference

Complete reference for the xmail REST API: basic concepts, authentication, response envelope, pagination/cache semantics, per-provider TLS configuration, and details for each endpoint (parameters, status codes, request/response examples).

- Handler code: `internal/api/` (all thin adapters on top of `account.Service`).
- Business logic: `internal/account/service.go`.
- Ready-to-use Postman import: [`docs/postman/`](./postman/).
- MCP client guide: [`docs/MCP_CLIENT_GUIDE.md`](./MCP_CLIENT_GUIDE.md).
- How to add a new endpoint: [`ARCHITECTURE.md §5.1`](../ARCHITECTURE.md).

**Table of contents**

1. [Basic concepts](#1-basic-concepts)
2. [Quickstart](#2-quickstart)
3. [Authentication](#3-authentication)
4. [Response envelope & error codes](#4-response-envelope--error-codes)
5. [Pagination & cache semantics](#5-pagination--cache-semantics)
6. [TLS modes & provider examples](#6-tls-modes--provider-examples)
7. [Endpoint reference](#7-endpoint-reference)
8. [Data type reference](#8-data-type-reference)
9. [End-to-end walkthrough](#9-end-to-end-walkthrough)
10. [Troubleshooting / FAQ](#10-troubleshooting--faq)
11. [Importing into Postman](#11-importing-into-postman)
12. [Web dashboard](#12-web-dashboard)

---

## 1. Basic concepts

xmail is an "email-as-a-service" backend: it stores multiple **Account**s (mailboxes), sends email on behalf of those accounts (SMTP), and reads their mailboxes (IMAP/POP3). The same operations are also exposed over MCP for AI agents — REST and MCP call the same `account.Service`, so the data is identical.

Core terms (see [`CONTEXT.md`](../CONTEXT.md) for the full glossary):

| Term | Meaning |
|---|---|
| **Account** | A single configured mailbox (name, email, username, per-protocol connection). It never carries credentials in a response. |
| **Mailer protocol** | `smtp` (send), `imap` (fetch/check/mark-read), `pop3` (fetch only). An account may configure only some of them. |
| **Connection** | `{host, port, tls_mode}` for one protocol on one account. |
| **Message** | Metadata for one email: UID, subject, from, to, date, is_read, attachment filenames. **Not** the body. |
| **Folder** | A named mailbox on the server. IMAP can have many; POP3 only has `INBOX`. IMAP folder names depend on the server/account language — use `GET /accounts/{id}/folders` to find them out, don't guess. |
| **Fetch window** | A slice of the mailbox, newest first: the `limit` newest messages, skipping `offset`. |
| **Message cache** | Local store of Message metadata, keyed by `(account, protocol, folder)`. |
| **Coverage / Exhausted** | How many of the newest messages are guaranteed to be in the cache / the entire mailbox is cached. |
| **Check** | Poll the mailbox: unread count + how many of the newest messages are not yet in the cache. |

Capabilities per protocol:

| Protocol | Send | Fetch | Check (unread) | Mark read | Attachment (name) | Folder |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| SMTP | yes | — | — | — | — | — |
| IMAP | — | yes | yes | yes | yes | many |
| POP3 | — | yes | no | no | no | `INBOX` only |

POP3 note: it has no concept of per-message flags, so `is_read` is always `true`, `unread_count` is always `0`, and `POST /messages/read` returns an error. TLS `starttls` is also **not supported** for POP3.

IMAP can **list folders** via `GET /accounts/{id}/folders` (§7.12) — POP3 is always `INBOX` and that protocol does not support listing (it returns `400`).

## 2. Quickstart

```bash
export BASE=http://localhost:5569
export XMAIL_API_KEY=changeme   # match the server env

# Health (no auth)
curl -s $BASE/healthz

# Create an account
curl -s -X POST $BASE/accounts \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Gmail","email":"me@example.com","username":"me@example.com","password":"app-pw",
       "smtp":{"host":"smtp.example.com","port":587,"tls_mode":"starttls"},
       "imap":{"host":"imap.example.com","port":993,"tls_mode":"tls"}}'

# Send
curl -s -X POST $BASE/accounts/<ID>/send \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"to":["bob@example.com"],"subject":"Hi","body_text":"Halo"}'

# Fetch (live)
curl -s "$BASE/accounts/<ID>/messages?limit=10&refresh=true" -H "X-API-Key: $XMAIL_API_KEY"
```

## 3. Authentication

| Item | Value |
|---|---|
| Header | `X-API-Key: <XMAIL_API_KEY>` |
| Key source | env `XMAIL_API_KEY` (a single static key for the MVP) |
| Comparison | constant-time (`crypto/subtle`) — safe from timing attacks |
| Applies to | all endpoints **except** `GET /healthz` |
| Invalid/missing key | `401` with `error.code = "unauthorized"` |

```bash
# rejected (401)
curl -s $BASE/accounts
# {"data":null,"error":{"code":"unauthorized","message":"missing or invalid X-API-Key header"}}
```

Note: the key is **not** stored/hashed in the database — it is read from the env on every request. Multi-key/rotatable API keys are still backlog (the `api_keys` table exists but is not used yet).

## 4. Response envelope & error codes

Every response — success or failure, on all endpoints — uses the same shape:

```json
// Success
{ "data": <payload>, "error": null }

// Failure
{ "data": null, "error": { "code": "<code>", "message": "<explanation>" } }
```

`DELETE` is not `204` either; it is still `200` with an envelope.

| `error.code` | HTTP | Cause |
|---|---|---|
| `unauthorized` | 401 | Missing or wrong `X-API-Key` header |
| `invalid_json` | 400 | Body is not valid JSON, or contains an unknown field (the decoder uses `DisallowUnknownFields`) |
| `validation_failed` | 400 | Required field empty, invalid `tls_mode`, POP3 `starttls`, empty `uid`, protocol does not support the operation, the account has no configuration for that protocol, **folder does not exist on the server**, etc. |
| `not_found` | 404 | `account_id` does not exist (including after deletion) |
| `internal_error` | 500 | Other errors: failed to dial the mail server, wrong credentials when sending, DB/query issues, etc. |

Important: connection/send errors to the mail server carry the original message from the protocol library (e.g. `smtp: send: ...`, `imap: login: ...`) but are wrapped in the `internal_error` code. Errors caused by caller input (validation) use `validation_failed`.

**Behavior change (missing folder):** `GET /messages` with a `folder` that does not exist now returns `400 validation_failed`, not `500`. This also applies to `POST /check` and `POST /messages/read` with a wrong folder. The error message mentions the folder name and the text from the server (e.g. `Unknown Mailbox`). Check the correct folder name list via `GET /accounts/{id}/folders`. Errors that are **not** input mistakes (dial failure, wrong auth, timeout, other IMAP errors) remain `500`.

The body must not contain unknown fields — the decoder rejects them as `invalid_json`. Example: sending `{"to":["a@b.c"],"Subject":"x"}` (capitalized) will fail.

## 5. Pagination & cache semantics

This is the part that is most often misunderstood. `GET /accounts/{id}/messages` accepts:

| Param | Default | Meaning |
|---|---|---|
| `limit` | `20` | max number of messages returned. `<= 0` → default 20 |
| `offset` | `0` | skip N **newest** messages |
| `folder` | `INBOX` | IMAP folder (POP3 ignores it, always INBOX) |
| `protocol` | `imap` | `imap` \| `pop3` |
| `refresh` | `false` | `true`/`1` = force a live dial, ignore the cache |

**Window** = the `limit` newest messages, skipping `offset`. Example with a mailbox of 100 messages (largest UID = newest):

| Request | Result |
|---|---|
| `?limit=20&offset=0` | 20 newest messages |
| `?limit=20&offset=20` | the next 20 messages (ranks 21–40) |
| `?limit=20&offset=200` | `[]` (beyond the number of messages) |
| `?limit=500` | all existing messages (short page) |

**Cache-first.** By default, xmail serves from `messages_cache` if the cache already **covers** the requested window — without dialing the server. If the cache does not cover it (e.g. the window is larger than anything fetched before), xmail dials, stores, then replies. `refresh=true` always dials.

State that determines whether the cache "covers" a window:

- **Coverage** — how many of the newest messages are known to be cached contiguously from the top of the mailbox.
- **Exhausted** — the entire mailbox is cached, so any window can be answered from the cache.

Practical consequences:

- Two consecutive identical requests: the second one does **not** dial (saves a connection).
- Requesting a larger `limit` than before (e.g. `limit=50` after `limit=20`): the cache does not cover it → it **dials** (it does not silently truncate the old result).
- `POST /check` always dials and fills the cache for the 50 newest messages.
- `refresh=true` forces a live dial even if the cache already covers it.

The cache is keyed by `(account, protocol, folder)` — so fetching a different protocol/folder has a separate cache, and `DELETE /accounts/{id}` deletes the entire cache for that account.

Consistency: if the server reports that the mailbox changed at the top (new mail arrived, or mail was deleted) so that the cache positions shift, xmail discards that cache key instead of serving rows with stale positions. So a "from cache" result is always a coherent prefix; if in doubt, use `refresh=true`.

## 6. TLS modes & provider examples

`tls_mode` per protocol:

| Value | Meaning | Common port example |
|---|---|---|
| `tls` | Implicit TLS (direct TLS connection) | SMTP 465, IMAP 993, POP3 995 |
| `starttls` | Connect in plaintext then upgrade with STARTTLS (must succeed) | SMTP 587, IMAP 143 |
| `none` | no TLS at all — **must be chosen explicitly** | test/local ports |

Validation rules:
- `tls_mode` is required and must be one of the three values above for each protocol that is provided.
- `port` must be 1–65535.
- `pop3.tls_mode = "starttls"` is **rejected** on save (the POP3 library does not support it) — use `tls` or `none`.
- Certificate validation is on by default; `none` is only for those who know the risk (credentials are sent in plaintext).

Examples of common provider configurations (re-verify in the provider documentation — xmail does **not** support OAuth2, so email accounts that require OAuth/app-password need an app-password):

| Provider | SMTP | IMAP | POP3 |
|---|---|---|---|
| Gmail | `smtp.gmail.com:587` / `starttls` | `imap.gmail.com:993` / `tls` | `pop.gmail.com:995` / `tls` |
| Microsoft 365 / Outlook | `smtp.office365.com:587` / `starttls` | `outlook.office365.com:993` / `tls` | (usually disabled) |
| Yahoo Mail | `smtp.mail.yahoo.com:465` / `tls` | `imap.mail.yahoo.com:993` / `tls` | `pop.mail.yahoo.com:995` / `tls` |
| Local/dev (e.g. MailHog) | `localhost:1025` / `none` | `localhost:1143` / `none` | `localhost:1110` / `none` |

Gmail/Outlook passwords are most likely **app-passwords**, not a regular login password.

## 7. Endpoint reference

Summary:

| Method | Path | Auth | Description |
|---|---|:---:|---|
| GET | `/healthz` | no | Health check |
| GET | `/` | no | Web dashboard — static HTML/CSS/JS assets (§12) |
| POST | `/accounts` | yes | Create an account → `201` |
| GET | `/accounts` | yes | List accounts → `200` |
| GET | `/accounts/{id}` | yes | Account detail → `200` / `404` |
| PUT | `/accounts/{id}` | yes | Update account (full replace) → `200` |
| DELETE | `/accounts/{id}` | yes | Delete account → `200` |
| POST | `/accounts/{id}/test-connection` | yes | Test connection+auth → `200` |
| POST | `/accounts/{id}/send` | yes | Send email → `200` |
| GET | `/accounts/{id}/folders` | yes | List folders (IMAP) → `200` |
| GET | `/accounts/{id}/messages` | yes | Fetch (cache-first) → `200` |
| POST | `/accounts/{id}/check` | yes | Check for new email → `200` |
| POST | `/accounts/{id}/messages/read` | yes | Mark as read (IMAP) → `200` |
| POST | `/mcp` | yes | MCP Streamable HTTP (see the MCP guide) |

---

### 7.1 `GET /healthz`

- **Auth:** no.
- **Response `200`:**

  ```json
  { "data": { "status": "ok" }, "error": null }
  ```

Used by container orchestrators (Docker/k8s) for liveness probes without needing a key.

---

### 7.2 `POST /accounts` — create an account

- **Auth:** yes.
- **Body:** [`accountRequest`](#81-accountrequest) (`password` is **required**).
- **Response `201`:** [`accountResponse`](#82-accountresponse) (no credentials).

Request example:

```json
{
  "name": "Gmail Kerja",
  "email": "me@example.com",
  "username": "me@example.com",
  "password": "app-password-here",
  "smtp": { "host": "smtp.gmail.com", "port": 587, "tls_mode": "starttls" },
  "imap": { "host": "imap.gmail.com", "port": 993, "tls_mode": "tls" }
}
```

Response example:

```json
{
  "data": {
    "id": "0d70a4d2-1e2f-4c3b-9a11-7f6c0b2e5a90",
    "name": "Gmail Kerja",
    "email": "me@example.com",
    "username": "me@example.com",
    "smtp": { "host": "smtp.gmail.com", "port": 587, "tls_mode": "starttls" },
    "imap": { "host": "imap.gmail.com", "port": 993, "tls_mode": "tls" },
    "pop3": null,
    "created_at": "2026-01-02T10:00:00Z",
    "updated_at": "2026-01-02T10:00:00Z"
  },
  "error": null
}
```

Status:

| Code | When |
|---|---|
| `201` | Successfully created |
| `400 invalid_json` | Body is not JSON / contains an unknown field |
| `400 validation_failed` | Required field empty, no protocol, invalid `tls_mode`, POP3 `starttls` |
| `500 internal_error` | Failed to encrypt/store |

Note: `id` is generated by the server (UUID). Credentials are encrypted with AES-256-GCM before being stored; the response **never** contains `password`.

---

### 7.3 `GET /accounts` — list accounts

- **Auth:** yes. No parameters.
- **Response `200`:** an array of `accountResponse`, sorted by `name`. Can be `[]`.

```json
{ "data": [ { "id": "...", "name": "Gmail Kerja", "email": "...", "username": "...",
             "smtp": {...}, "imap": {...}, "pop3": null,
             "created_at": "...", "updated_at": "..." } ], "error": null }
```

---

### 7.4 `GET /accounts/{id}` — account detail

- **Response `200`:** a single `accountResponse`.
- **`404 not_found`** if the id does not exist.

---

### 7.5 `PUT /accounts/{id}` — update an account

- **Body:** [`accountRequest`](#81-accountrequest), but `password` is **optional**.
- **Response `200`:** the updated `accountResponse`.

**Full replace, not a merge.** All fields other than `password` must be sent again; protocol fields (`smtp`/`imap`/`pop3`) that are **not** sent will be set to `NULL` (effectively removing that protocol configuration). The only optional field is `password`:

| `password` value | Effect |
|---|---|
| a string is sent | credentials are replaced with the new value |
| `null` / not sent | the old credentials are kept |

Example — rename and remove the POP3 configuration, keep the credentials:

```bash
curl -s -X PUT $BASE/accounts/$ID \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Gmail (renamed)","email":"me@example.com","username":"me@example.com",
       "imap":{"host":"imap.gmail.com","port":993,"tls_mode":"tls"}}'
# Note: smtp & pop3 are not sent -> they become null
```

Status: `200` success; `400` validation; `404` if the id does not exist.

---

### 7.6 `DELETE /accounts/{id}` — delete an account

- **Response `200`:** `{ "data": { "deleted": true }, "error": null }`.
- **`404 not_found`** if the id does not exist.

Deleting an account also removes its credentials and its entire `messages_cache` (cascade).

---

### 7.7 `POST /accounts/{id}/test-connection` — test connection

- **Body:** `{ "protocol": "smtp" | "imap" | "pop3" }` (optional, default `imap`).
- **Response `200`:** `{ "data": { "ok": true }, "error": null }`.

Performs connect + login to the selected protocol **without** sending/fetching anything. Good for verifying credentials/config before use.

| Code | When |
|---|---|
| `200` | Connect + auth succeeded |
| `400 validation_failed` | Unknown `protocol`, or the account has no configuration for that protocol |
| `404 not_found` | `account_id` does not exist |
| `500 internal_error` | Failed to connect/login (wrong host, wrong credentials, TLS mismatch, etc.) |

---

### 7.8 `POST /accounts/{id}/send` — send email

- **Body:** [`sendRequest`](#84-sendrequest).
- **Response `200`:** `{ "data": { "status": "sent" }, "error": null }`.

Body fields:

| Field | Required | Notes |
|---|:---:|---|
| `to` | yes | array of emails, at least 1 |
| `cc` / `bcc` | no | array of emails |
| `subject` | no | |
| `body_text` | no | plain-text body |
| `body_html` | no | HTML body |
| `attachments` | no | `{filename, content_type, data_base64}` |
| `headers` | no | `map[string]string`, custom headers sent as-is |

Body/From rules:
- **From** = `account.email` (set automatically; it cannot be overridden via the body).
- If **both** `body_html` **and** `body_text` are set → sent as `multipart/alternative` (HTML primary, plain-text alternative).
- If only `body_html` → HTML body. Otherwise → plain-text (`body_text`, may be empty).
- `data_base64` in `attachments` is decoded as standard base64; invalid base64 → `400 validation_failed`.
- Currently `content_type` on an attachment is **not used** — the MIME type of the attachment is determined automatically by the SMTP library from the file name. The content is still sent (`data_base64`).

Example:

```json
{
  "to": ["bob@example.com"],
  "cc": ["carol@example.com"],
  "subject": "Laporan",
  "body_text": "Terlampir laporan.",
  "body_html": "<p>Terlampir laporan.</p>",
  "headers": { "X-Priority": "1", "Reply-To": "reply@example.com" },
  "attachments": [
    { "filename": "laporan.pdf", "content_type": "application/pdf", "data_base64": "JVBERi0xLjQK..." }
  ]
}
```

Status: `200` sent; `400` (no `to`, invalid base64, invalid JSON); `404` id does not exist; `500` send/dial/auth failure.

---

### 7.9 `GET /accounts/{id}/messages` — fetch email

- **Query:** [`limit`, `offset`, `folder`, `protocol`, `refresh`](#5-pagination--cache-semantics).
- **Response `200`:** an array of [`Message`](#83-message) (newest first).

Example:

```bash
curl -s "$BASE/accounts/$ID/messages?protocol=imap&folder=INBOX&limit=5&refresh=true" \
  -H "X-API-Key: $XMAIL_API_KEY"
```

```json
{
  "data": [
    { "uid": "1042", "folder": "INBOX", "subject": "Laporan bulanan",
      "from": "alice@example.com", "to": "me@example.com",
      "date": "2026-01-02T09:31:00Z", "is_read": false,
      "attachments": ["laporan.pdf", "data.xlsx"] },
    { "uid": "1041", "folder": "INBOX", "subject": "Re: meeting",
      "from": "bob@example.com", "to": "me@example.com",
      "date": "2026-01-02T08:10:00Z", "is_read": true }
  ],
  "error": null
}
```

Per-protocol notes:

| | IMAP | POP3 |
|---|---|---|
| Sort source | descending UID (UID increases with arrival) | UIDL order, reversed (newest first) |
| `is_read` | from the `\Seen` flag | always `true` (POP3 has no flags) |
| `attachments` | file names from BODYSTRUCTURE | always empty (TOP does not include the body structure) |
| `folder` | the requested folder | always `INBOX` |
| Email body | not downloaded (metadata only) | not downloaded (headers only) |

Status: `200` success (including `[]` when the window is empty); `400 validation_failed` when `protocol` is SMTP/invalid, the protocol is not configured, or `folder` does not exist on the server; `404` id does not exist; `500` dial/fetch failure.

---

### 7.10 `POST /accounts/{id}/check` — check for new email

- **Body:** `{ "protocol": "imap" | "pop3", "folder": "INBOX" }` (both optional; default `imap`/`INBOX`).
- **Response `200`:** [`CheckResult`](#85-checkresult).

```json
{ "data": { "unread_count": 5, "new_count": 2 }, "error": null }
```

Semantics:

- **`unread_count`** — the number of unread reported by the server (IMAP `STATUS ... UNSEEN`). POP3 has no concept of unread → `0`.
- **`new_count`** — how many of the **50 newest messages** are not yet in xmail's `messages_cache` ("new since the last check"). This is **not** a protocol concept; it is computed locally from the cache.
- This endpoint **always** dials the server (not cache-first) and updates the cache for the 50 newest messages — so a subsequent `GET /messages` with a window within that range can be served from the cache.

Status: `200`; `400` invalid/unconfigured protocol; `404`; `500` dial failure.

---

### 7.11 `POST /accounts/{id}/messages/read` — mark as read

- **Body:**

  | Field | Required | Notes |
  |---|:---:|---|
  | `uid` | yes | message UID (from the `/messages` result) |
  | `protocol` | no | default `imap` |
  | `folder` | no | default `INBOX` |

- **Response `200`:** `{ "data": { "marked_read": true }, "error": null }`.

**IMAP only** (sets the `\Seen` flag). For `protocol=pop3` → `400 validation_failed` because POP3 has no per-message flags. The local cache is also updated so that subsequent cached fetches are consistent.

Status: `200`; `400` (empty `uid`, POP3, invalid IMAP UID, folder does not exist); `404`; `500` dial/store failure.

---

### 7.12 `GET /accounts/{id}/folders` — list folders

- **Query:** `?protocol=imap` (optional, default `imap`).
- **Response `200`:** an array of [`Folder`](#86-folder), sorted by name.

Used to find out the correct folder names before calling `/messages` — IMAP folder names depend on the server/account (e.g. an Indonesian-language Gmail uses `[Gmail]/Surat Terkirim`, not `[Gmail]/Sent Mail`). Each entry carries role `attributes` (e.g. `\Sent`, `\Drafts`) so clients can map the folders they care about themselves.

```bash
curl -s "$BASE/accounts/$ID/folders" -H "X-API-Key: $XMAIL_API_KEY"
```

```json
{
  "data": [
    { "name": "INBOX", "delimiter": "/" },
    { "name": "[Gmail]/Surat Terkirim", "delimiter": "/", "attributes": ["\\Sent", "\\HasNoChildren"] },
    { "name": "[Gmail]", "delimiter": "/", "attributes": ["\\Noselect", "\\HasChildren"] }
  ],
  "error": null
}
```

Notes:

- Always **live** to the server (a single cheap `LIST` command; not cached).
- `delimiter` is omitted when the server reports NIL; `attributes` is omitted when empty.
- `\Noselect` entries are returned as-is (not filtered) — such an entry **cannot** be `SELECT`ed, so fetching that folder will `400`.
- Always `[]` (not `null`) when the server does not report any folders.

Status:

| Code | When |
|---|---|
| `200` | Success (including `[]`) |
| `400 validation_failed` | `protocol` is not IMAP (`pop3`/`smtp`), the account has no configuration for that protocol, or the protocol is not supported |
| `404 not_found` | `account_id` does not exist |
| `500 internal_error` | Failed to dial/login to the server, or `LIST` failed |

## 8. Data type reference

### 8.1 `accountRequest`

Body of `POST`/`PUT /accounts`.

| Field | Type | Required (create) | Required (update) | Notes |
|---|---|:---:|:---:|---|
| `name` | string | yes | yes | display name |
| `email` | string | yes | yes | becomes the **From** address when sending |
| `username` | string | yes | yes | server login username |
| `password` | string \| null | yes | no | `null` on update = keep the old credentials |
| `smtp` | `Connection` \| null | one of the protocols | yes (if it is to be kept) | |
| `imap` | `Connection` \| null | one of the protocols | yes | |
| `pop3` | `Connection` \| null | one of the protocols | yes | |

At least one of `smtp`/`imap`/`pop3` must be non-null.

### 8.2 `accountResponse`

Returned by all account endpoints. **Never** contains credentials.

| Field | Type | Notes |
|---|---|---|
| `id` | string | UUID |
| `name` | string | |
| `email` | string | |
| `username` | string | |
| `smtp` / `imap` / `pop3` | `Connection` \| null | `null` = protocol not configured |
| `created_at` | string | RFC3339 UTC |
| `updated_at` | string | RFC3339 UTC |

`Connection`:

| Field | Type | Value |
|---|---|---|
| `host` | string | hostname |
| `port` | int | 1–65535 |
| `tls_mode` | string | `tls` \| `starttls` \| `none` |

### 8.3 `Message`

| Field | Type | Notes |
|---|---|---|
| `uid` | string | UID from the server (IMAP UID / POP3 UIDL) |
| `folder` | string | source folder (POP3 is always `INBOX`) |
| `subject` | string | |
| `from` | string | first address from the From header |
| `to` | string | first address from the To header |
| `date` | string | RFC3339 UTC |
| `is_read` | bool | IMAP `\Seen` flag; POP3 always `true` |
| `attachments` | string[] | **file names only**; omitted when empty; POP3 always empty |

### 8.4 `sendRequest`

| Field | Type | Required |
|---|---|:---:|
| `to` | string[] | yes |
| `cc` / `bcc` | string[] | no |
| `subject` | string | no |
| `body_text` / `body_html` | string | no |
| `attachments` | `Attachment[]` | no |
| `headers` | object `map[string]string` | no |

`Attachment`: `{ "filename": string, "content_type": string, "data_base64": string }` (content = standard base64).

### 8.5 `CheckResult`

| Field | Type | Notes |
|---|---|---|
| `unread_count` | int | IMAP only; POP3 = 0 |
| `new_count` | int | of the 50 newest messages, those not yet cached |

### 8.6 `Folder`

Returned by `GET /accounts/{id}/folders`.

| Field | Type | Notes |
|---|---|---|
| `name` | string | exact mailbox name as on the server (use this for the `folder` query) |
| `delimiter` | string | hierarchy separator (e.g. `/`); omitted when the server reports NIL |
| `attributes` | string[] | mailbox attributes (e.g. `\Sent`, `\Drafts`, `\Noselect`, `\HasChildren`); omitted when empty |

## 9. End-to-end walkthrough

```bash
export BASE=http://localhost:5569
export XMAIL_API_KEY=changeme

# 1) Create an account & capture the id
RESP=$(curl -s -X POST $BASE/accounts \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Gmail","email":"me@example.com","username":"me@example.com","password":"app-pw",
       "smtp":{"host":"smtp.gmail.com","port":587,"tls_mode":"starttls"},
       "imap":{"host":"imap.gmail.com","port":993,"tls_mode":"tls"}}')
ID=$(echo "$RESP" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "account id = $ID"

# 2) Validate the connection for each protocol
curl -s -X POST $BASE/accounts/$ID/test-connection -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"protocol":"smtp"}'
curl -s -X POST $BASE/accounts/$ID/test-connection -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"protocol":"imap"}'

# 3) Send an email
curl -s -X POST $BASE/accounts/$ID/send -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"to":["bob@example.com"],"subject":"Halo","body_text":"Pesan percobaan."}'

# 4) Check for new email (fills the cache with the 50 newest)
curl -s -X POST $BASE/accounts/$ID/check -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"protocol":"imap"}'

# 5) Fetch from cache (fast, no dial)
curl -s "$BASE/accounts/$ID/messages?limit=10" -H "X-API-Key: $XMAIL_API_KEY"

# 6) Fetch live to make sure it is the latest
curl -s "$BASE/accounts/$ID/messages?limit=10&refresh=true" -H "X-API-Key: $XMAIL_API_KEY"

# 7) Mark one message as read (use the uid from step 5)
curl -s -X POST $BASE/accounts/$ID/messages/read -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"uid":"1042"}'

# 8) Clean up
curl -s -X DELETE $BASE/accounts/$ID -H "X-API-Key: $XMAIL_API_KEY"
```

## 10. Troubleshooting / FAQ

| Symptom | Cause & solution |
|---|---|
| `401 unauthorized` | Wrong/missing `X-API-Key`. Match it with the server's `XMAIL_API_KEY` env. |
| `400 invalid_json` even though the JSON looks correct | There is an **unknown field** (strict decoder), or a field name has the wrong case. Only the fields in this document are accepted. |
| `400 validation_failed: at least one of smtp, imap, pop3 must be configured` | Send at least one protocol block with `host`, `port`, `tls_mode`. |
| `400 ... pop3.tls_mode "starttls" is not supported` | POP3 supports only `tls` or `none`. |
| `500` on `/messages` or `/check` | The mail server cannot be reached / wrong credentials / the account has no configuration for that protocol (check the error message). Try `/test-connection` first. |
| `500` on `/send` with `internal_error` | Usually SMTP auth failed or the From (`account.email`) was rejected by the server. Verify the app-password & the From address. |
| Fields I sent disappear after `PUT` | `PUT` = full replace. Fields that are not sent become `null`. |
| The second fetch seems "not to update" | That is cache-first behavior. Use `refresh=true` to force a live fetch. |
| `new_count` is small/0 but there is new email | `new_count` is computed from the 50 newest messages vs the local cache, not the whole mailbox. |
| An attachment is sent but its type is odd | `content_type` is not used; the type is determined from the `filename` extension. |
| `mark read` fails for POP3 | POP3 does not support per-message flags. Use IMAP. |
| I need a different key per client | Not supported yet (single static key). Multi-key/rotatable is still backlog. |

## 11. Importing into Postman

1. Postman → **Import** → select `docs/postman/xmail.postman_collection.json` **and** `docs/postman/xmail.postman_environment.json`.
2. Select the **xmail (local)** environment.
3. Set the `apiKey` variable to the server's `XMAIL_API_KEY`.
4. Run **Health → Healthz** to verify the connection.
5. Run **Accounts → Create Account** — the test script automatically fills in `accountId`, so the other `{id}` requests are ready to use.

The collection contains a request for each endpoint, example bodies, example error scenarios (401/400/404), and raw MCP requests. See [`docs/postman/README.md`](./postman/README.md) for the full list.

## 12. Web dashboard

The xmail binary also serves a web dashboard for **account management** (CRUD + connectivity tests). The dashboard is **not** a new API — it is a client that calls the endpoints in §7 via `fetch` from the browser.

| Item | Value |
|---|---|
| URL | `http://localhost:5569` (or the `XMAIL_LISTEN_ADDR` address) — the Windows tray has an **Open dashboard** menu |
| Assets | `GET /`, `GET /app.js`, `GET /style.css` — **no auth**, `go:embed`-ed into the binary |
| Dashboard auth | the login page accepts the `XMAIL_API_KEY`, stores it in `sessionStorage`, then sends it as the `X-API-Key` header on every API call |
| Scope | list/detail/create/update/delete accounts + `POST /accounts/{id}/test-connection` per protocol |
| Out of scope | send email, fetch/check messages, mark-read, list folders (still REST/MCP) |
| Asset response headers | `Content-Security-Policy: default-src 'self'`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-cache` |

Key points for integration:

- API endpoints (`/accounts`, `/mcp`) **still** require `X-API-Key`; accessing the dashboard does not grant API access. `GET /` only returns static assets (no secrets) — see `docs/adr/0002-dashboard-serving-and-auth.md`.
- Because the dashboard is served **same-origin**, there is no CORS; no preflight is needed.
- Account passwords are never returned by the API (`§7.5`), so the edit form always shows an empty password field: leaving it empty = keep the old credentials.
- `PUT /accounts/{id}` is a **full replace**; the dashboard form always resends all fields, so its behavior is consistent with §7.5.
- A non-API path that does not exist (e.g. `/nope`) is answered with `404` by the file server, not with a JSON envelope.

Security limitation: if `XMAIL_LISTEN_ADDR` is not bound to loopback, the dashboard & API can be reached from the network — restrict it with a firewall/reverse proxy and keep `XMAIL_API_KEY` secret.
