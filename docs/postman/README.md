# Postman — xmail

This folder contains an import-ready Postman collection for the xmail REST API (plus raw MCP requests).

| File | Contents |
|---|---|
| `xmail.postman_collection.json` | The main collection: every REST endpoint + example error scenarios + MCP requests |
| `xmail.postman_environment.json` | Environment `xmail (local)`: `baseUrl`, `apiKey`, `accountId`, `messageUid` |

## How to import

1. Postman → **Import** → select **both** files above (collection + environment).
2. Select the **xmail (local)** environment in the top-right dropdown.
3. Set the `apiKey` variable to the server's `XMAIL_API_KEY` (dev default: `changeme`).
4. Run **Health → Healthz (no auth)** to verify the server is up.
5. Run **Accounts → Create Account** — the test script stores the `id` into `accountId` automatically.

## Variables

| Variable | Default | Set by |
|---|---|---|
| `baseUrl` | `http://localhost:5569` | manual |
| `apiKey` | `changeme` | manual (matches the server env) |
| `accountId` | (empty) | automatically by *Create Account* |
| `messageUid` | (empty) | automatically by *Fetch Messages* |

## Folder structure

- **Health** — health check without auth.
- **Accounts** — account CRUD + test-connection (SMTP/IMAP/POP3) + validation scenarios (no protocol, POP3 starttls) + 404 after delete.
- **Mail** — send (plain, HTML+attachment+headers, no recipients), fetch (cache-first, refresh, POP3), check, mark-read (+ missing `uid`).
- **Folders** — list IMAP folders (`GET /accounts/{id}/folders`, using the `accountId` from the environment).
- **Errors (examples)** — 401 (no key), 400 (unknown field), 404 (account does not exist).
- **MCP** — `initialize`, `tools/list`, `tools/call` (`list_accounts`, `fetch_emails`) over Streamable HTTP.

## Notes

- Auth `X-API-Key` is set at the collection level (auth type: API Key, header `X-API-Key`). The **Healthz** and **Unauthorized** requests override it to `noauth`.
- The collection-level test script verifies every response uses the `{data, error}` envelope — including error responses.
- MCP requests use the `Accept: application/json, text/event-stream` header (required for the Streamable HTTP transport). If the response is SSE, the payload is on the `data:` line.
- For day-to-day MCP use, prefer a real MCP client — see [`../MCP_CLIENT_GUIDE.md`](../MCP_CLIENT_GUIDE.md).

Full API documentation: [`../API.md`](../API.md).
