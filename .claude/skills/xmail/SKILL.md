---
name: xmail
description: >-
  Use when the user asks to send an email, check/fetch inbox messages, or manage
  email accounts through xmail (this project's own email service) — either as
  plain instructions ("kirim email lewat xmail", "cek email masuk", "list akun
  email", "tambah akun email baru") or when MCP tools named list_accounts,
  send_email, fetch_emails, or check_new_emails are already available in your
  tool list and the task is email-related. Covers both of xmail's interfaces:
  its REST API (http://<host>:<port>, header X-API-Key) and its MCP server
  (mounted at /mcp on the same host/port, or via stdio). This skill is about
  *using* a running xmail instance as a client — for how xmail is built/how to
  extend its code, see ARCHITECTURE.md in the repo root instead.
---

# xmail — send/fetch/check email as an agent

xmail is this repo's own email-as-a-service backend (see [PRD.MD](../../../PRD.MD), [ARCHITECTURE.md](../../../ARCHITECTURE.md)). It exposes the exact same operations two ways — a REST API and an MCP server — both backed by the identical business logic (`account.Service`), so results are equivalent either way. This skill tells you which to reach for and the exact shapes to send.

## 1. Figure out how you're connected

- **MCP tools already in your tool list?** Look for `list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`. If present, you are already connected to xmail's MCP server — use §3 (MCP) for those four operations. Skip straight there; no auth header to manage, no base URL to guess.
- **No MCP tools, or you need account CRUD?** Use §2 (REST). MCP only exposes the 4 tools above — creating/updating/deleting an account or testing a connection is **REST-only**, there is no MCP equivalent (see §4).
- You can also mix: manage accounts via REST, then send/fetch via MCP if both are available.

## 2. REST API

### Connection info

You need a base URL and an API key. Don't guess — get them from the user or the environment:
- Base URL: often `http://localhost:8080` for local dev, or whatever `XMAIL_LISTEN_ADDR` was set to. Ask the user if unknown; don't assume a port.
- API key: the value of `XMAIL_API_KEY` for that running instance. Ask the user; never invent one.
- Every request except `GET /healthz` needs header `X-API-Key: <key>`.
- All responses are `{"data": ..., "error": null}` on success, or `{"data": null, "error": {"code": "...", "message": "..."}}` on failure. Check `error` before trusting `data`.

| `error.code` | HTTP status | Meaning |
|---|---|---|
| `unauthorized` | 401 | Missing/wrong `X-API-Key` |
| `validation_failed` | 400 | Bad input (missing field, bad `tls_mode`, unknown protocol, etc.) |
| `not_found` | 404 | Account ID doesn't exist |
| `internal_error` | 500 | Something failed downstream unexpectedly (dial/parse error, cache or storage failure, etc.) — check `error.message`. An unconfigured protocol is *not* this: it returns `validation_failed` 400. |

### Health check (no auth)

```bash
curl -s "$BASE_URL/healthz"
```

### Manage accounts

Create (password required; at least one of `smtp`/`imap`/`pop3` required; `tls_mode` is one of `tls` / `starttls` / `none`):

```bash
curl -s -X POST "$BASE_URL/accounts" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{
  "name": "Work Gmail",
  "email": "me@example.com",
  "username": "me@example.com",
  "password": "app-password-here",
  "smtp": {"host": "smtp.gmail.com", "port": 587, "tls_mode": "starttls"},
  "imap": {"host": "imap.gmail.com", "port": 993, "tls_mode": "tls"}
}'
```

List / get one / delete:

```bash
curl -s "$BASE_URL/accounts" -H "X-API-Key: $API_KEY"
curl -s "$BASE_URL/accounts/$ACCOUNT_ID" -H "X-API-Key: $API_KEY"
curl -s -X DELETE "$BASE_URL/accounts/$ACCOUNT_ID" -H "X-API-Key: $API_KEY"   # -> {"data":{"deleted":true},"error":null}
```

Update — **`PUT` is a full replace, not a patch**. Send every field you want kept, not just the one you're changing, or the omitted ones get nulled out. `password` is the one exception: omit it (or send `null`) to keep the existing credential.

```bash
curl -s -X PUT "$BASE_URL/accounts/$ACCOUNT_ID" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{
  "name": "Work Gmail (renamed)", "email": "me@example.com", "username": "me@example.com",
  "smtp": {"host": "smtp.gmail.com", "port": 587, "tls_mode": "starttls"},
  "imap": {"host": "imap.gmail.com", "port": 993, "tls_mode": "tls"}
}'
```

Test a connection before trusting it (dials + authenticates, sends/fetches nothing):

```bash
curl -s -X POST "$BASE_URL/accounts/$ACCOUNT_ID/test-connection" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{"protocol": "smtp"}'
```

### Send an email

```bash
curl -s -X POST "$BASE_URL/accounts/$ACCOUNT_ID/send" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{
  "to": ["someone@example.com"],
  "cc": [], "bcc": [],
  "subject": "Hello",
  "body_text": "Plain text body",
  "body_html": "<p>Optional HTML body</p>",
  "attachments": [{"filename": "note.txt", "content_type": "text/plain", "data_base64": "aGVsbG8="}],
  "headers": {"X-Priority": "1", "Reply-To": "other@example.com"}
}'
```
`to` is required and non-empty; everything else is optional. Attachment bytes are **base64-encoded** in `data_base64` (JSON has no binary type) — base64-encode file content before sending. `headers` lets you set arbitrary custom header lines.

### Fetch messages / check for new mail / mark as read

```bash
curl -s "$BASE_URL/accounts/$ACCOUNT_ID/messages?folder=INBOX&limit=20&offset=0&protocol=imap" -H "X-API-Key: $API_KEY"
curl -s -X POST "$BASE_URL/accounts/$ACCOUNT_ID/check" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{"protocol": "imap", "folder": "INBOX"}'
curl -s -X POST "$BASE_URL/accounts/$ACCOUNT_ID/messages/read" -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" -d '{"protocol": "imap", "folder": "INBOX", "uid": "12345"}'
```
`protocol` defaults to `imap` if omitted (POP3 has no folders, no unread-count concept, and no mark-as-read — `check` against `pop3` always reports `unread_count: 0`, and `messages/read` against `pop3` returns a validation error). **Fetching is cache-first**: the first call for a given account+folder dials the mail server and caches the result; later calls are served from that cache without re-dialing, as long as the requested window starts inside the cached coverage. A window that starts past the cached prefix dials again — the cache may be behind the mailbox — and so does a window that runs past what's cached unless the cache is known to be exhausted. A page shorter than `limit` tells xmail where the mailbox ends, so any cached row beyond it is dropped rather than served as mail that no longer exists. The cache is only as fresh as the last dial: mail that arrived, or was deleted, since then keeps being returned until a fetch covers it. Add `&refresh=true` to force a fresh dial (e.g. right after you expect new mail to have arrived). Message objects: `{"uid","folder","subject","from","to","date","is_read","attachments"}` (`attachments` is a list of filenames, omitted if none — IMAP only).

## 3. MCP tools

Use these directly when they appear in your tool list — same effect as the REST calls above, just structured. Arguments below are the exact JSON schema field names.

| Tool | Arguments | Notes |
|---|---|---|
| `list_accounts` | *(none)* | Returns `[{id, name, email}]` — never credentials or connection config. Get an `account_id` from here before calling any other tool. |
| `send_email` | `account_id` (required), `to` (required, array of strings), `subject` (required), `body_text`, `body_html`, `headers` (object of string→string) | No attachment support via MCP (REST-only, see §2). |
| `fetch_emails` | `account_id` (required), `protocol` (`imap`/`pop3`, default `imap`), `folder` (default `INBOX`), `limit` (default 20), `refresh` (bool, default false) | Returns the same message objects as the REST endpoint, including `attachments`. `refresh=true` forces a live dial instead of serving cached results (see §2). |
| `check_new_emails` | `account_id` (required), `protocol` (default `imap`), `folder` (default `INBOX`) | Returns `{unread_count, new_count}`. |

A failed tool call comes back with `isError: true` and a human-readable message in the content — check for that instead of assuming success.

## 4. What's REST-only vs MCP-only

- **REST-only**: create/update/delete an account, test a connection, attachments on send, marking a message as read. There is no MCP tool for account management or mark-as-read — don't look for one.
- **MCP-only**: nothing — every MCP tool has a REST equivalent.
- If you're asked to "set up a new email account" via MCP-only access, say so — that step needs REST (or the user doing it through whatever admin UI wraps this API), then you can use MCP for the day-to-day send/fetch/check once the account exists.

## 5. Common recipes

**Add an account then send a test email:**
1. `POST /accounts` (§2) → read `data.id` from the response.
2. `POST /accounts/{id}/test-connection` with `{"protocol":"smtp"}` → confirm no error before trusting the account.
3. `send_email` (MCP) or `POST /accounts/{id}/send` (REST) with that `id`.

**"Do I have new mail?" across every account:**
1. `list_accounts` (MCP) or `GET /accounts` (REST) to enumerate.
2. `check_new_emails`/`POST .../check` per account — sum `unread_count`/`new_count`.

**Summarize the last N emails in an account:**
`fetch_emails` (or `GET .../messages?limit=N`) → the response already has `subject`/`from`/`date`/`is_read`, no need to fetch full bodies (xmail only stores/returns metadata, not message bodies, by design).
