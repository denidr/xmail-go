# xmail — MCP Client Guide

Complete guide to connecting an **MCP client** (Claude Code, Claude Desktop, Cursor, or an agent/custom client) to xmail, so an agent can use the email tools (`list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`, `list_folders`) directly from the conversation.

Implementation: `internal/mcpserver/server.go` (library `github.com/mark3labs/mcp-go`). All five tools **call the same `account.Service`** as the REST API — so MCP and REST cannot diverge in data (a single source of logic). See [`ARCHITECTURE.md §1`](../ARCHITECTURE.md).

- REST API reference: [`docs/API.md`](./API.md).

**Table of contents**

1. [Overview & architecture](#1-overview--architecture)
2. [Transport comparison](#2-transport-comparison)
3. [Prerequisites](#3-prerequisites)
4. [Transport 1 — Streamable HTTP](#4-transport-1--streamable-http)
5. [Transport 2 — stdio](#5-transport-2--stdio)
6. [Example client (Go)](#6-example-client-go)
7. [Session flow & full JSON-RPC](#7-session-flow--full-json-rpc)
8. [Tool reference (schema + output)](#8-tool-reference-schema--output)
9. [Error handling](#9-error-handling)
10. [Security](#10-security)
11. [Testing with MCP Inspector](#11-testing-with-mcp-inspector)
12. [MCP ↔ REST mapping](#12-mcp--rest-mapping)
13. [Limitations & notes](#13-limitations--notes)
14. [Troubleshooting](#14-troubleshooting)

---

## 1. Overview & architecture

```
                 ┌─────────────────────────────┐
   REST client ──▶│  internal/api (HTTP)        │──┐
                 └─────────────────────────────┘  │
                                                   ▼
                 ┌─────────────────────────────┐  ┌────────────────────┐
   MCP client ──▶│  internal/mcpserver         │─▶│  account.Service   │
   (HTTP /mcp    │  (tools: 5)                 │  │  (single logic)    │
      or stdio)  └─────────────────────────────┘  └─────────┬──────────┘
                                                            │
                                    ┌───────────────────────┼───────────────────┐
                                    ▼                       ▼                   ▼
                                 SMTP                    IMAP                POP3
                                    │                       │                   │
                                    └──────────────┬────────┴───────────────────┘
                                                   ▼
                                            SQLite (accounts,
                                            encrypted credentials,
                                            messages_cache)
```

Key points:

- The MCP **Streamable HTTP** endpoint is mounted at `/mcp` on the **same HTTP server** as REST (one port, one auth) — not a separate port.
- **stdio** is optional, enabled when `XMAIL_MCP_STDIO=true`; xmail is run as a subprocess by the client.
- `serverInfo.name` = `xmail`; `serverInfo.version` = the real xmail build version (stamped at release, not a hardcoded number).

## 2. Transport comparison

| Aspect | Streamable HTTP | stdio |
|---|---|---|
| Activation | always on | `XMAIL_MCP_STDIO=true` |
| How the client connects | URL `http://<host>:<port>/mcp` | the client spawns the xmail binary |
| Auth | `X-API-Key` header per request | in-process (no header); the process still needs env vars to start |
| HTTP server still runs | yes | yes (set a different `XMAIL_LISTEN_ADDR` if needed) |
| Good for | remote servers, many clients, Claude Code | desktop apps (Claude Desktop), local instances |
| Session | stateless | in-process |
| Shares DB with REST | yes | yes |

## 3. Prerequisites

1. xmail is running (`make run`, `go run ./cmd/xmail`, a binary, or Docker). See [`README.MD`](../README.MD).
2. Required env vars (the server refuses to start if they are empty):
   - `XMAIL_API_KEY` — for the `X-API-Key` header.
   - `XMAIL_ENCRYPTION_KEY` — base64 of 32 bytes (`openssl rand -base64 32`).
3. For HTTP: the port from `XMAIL_LISTEN_ADDR` (default `:5569`).

Verification:

```bash
curl -s http://localhost:5569/healthz
# {"data":{"status":"ok"},"error":null}
```

## 4. Transport 1 — Streamable HTTP

**Endpoint:** `POST http://<host>:<port>/mcp`

Characteristics:

- The `/mcp` route sits behind the same API-key middleware as REST → **every request must** include `X-API-Key` (only `/healthz` is auth-free).
- **Stateless** transport: no need to manage `Mcp-Session-Id`.
- The `Accept` header must contain `application/json` **and/or** `text/event-stream`; the server may reply with plain JSON or SSE.

### 4.1 Claude Code

```bash
# HTTP transport
claude mcp add --transport http xmail http://localhost:5569/mcp \
  --header "X-API-Key: $XMAIL_API_KEY"

# verify
claude mcp list
```

The default scope is local to the project. Add `--scope user` if you want it available in all projects:

```bash
claude mcp add --scope user --transport http xmail http://localhost:5569/mcp \
  --header "X-API-Key: $XMAIL_API_KEY"
```

Once connected, in a Claude Code session just ask naturally, e.g. "use xmail to list accounts", "send an email to bob@example.com", or check the server status with `/mcp`.

### 4.2 Generic JSON configuration

Many clients read the `mcpServers` config:

```json
{
  "mcpServers": {
    "xmail": {
      "type": "http",
      "url": "http://localhost:5569/mcp",
      "headers": {
        "X-API-Key": "CHANGE_ME_api_key"
      }
    }
  }
}
```

> Field names vary between clients (`type`: `http` vs `streamable-http`; `headers` vs `httpHeaders`). What must be correct: the **URL** and the **`X-API-Key` header**.

### 4.3 Claude Desktop (remote via `mcp-remote`)

Claude Desktop more commonly uses stdio (section 5), but the HTTP endpoint can be bridged:

```json
{
  "mcpServers": {
    "xmail": {
      "command": "npx",
      "args": [
        "mcp-remote",
        "http://localhost:5569/mcp",
        "--header",
        "X-API-Key:${XMAIL_API_KEY}"
      ],
      "env": { "XMAIL_API_KEY": "CHANGE_ME_api_key" }
    }
  }
}
```

### 4.4 Manual testing via `curl` (JSON-RPC)

See the full request/response example in [section 7](#7-session-flow--full-json-rpc). In short:

```bash
curl -s -X POST http://localhost:5569/mcp \
  -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
        "protocolVersion":"2025-06-18","capabilities":{},
        "clientInfo":{"name":"curl","version":"0.0.0"}}}'
```

If the reply is of type `text/event-stream`, the JSON-RPC payload is on the line starting with `data:`.

## 5. Transport 2 — stdio

Enable it with `XMAIL_MCP_STDIO=true`. xmail reads/writes the MCP protocol over the process's **stdin/stdout**, while server logs still go to **stderr** (so they don't pollute the MCP stream). The client just runs the xmail binary.

Flow: the client spawns the process → sends `initialize` over stdin → receives the response over stdout → and so on.

### 5.1 Claude Desktop (`claude_desktop_config.json`)

Windows:

```json
{
  "mcpServers": {
    "xmail": {
      "command": "C:\\path\\to\\xmail.exe",
      "env": {
        "XMAIL_API_KEY": "CHANGE_ME_api_key",
        "XMAIL_ENCRYPTION_KEY": "CHANGE_ME_base64_32_bytes",
        "XMAIL_DB_PATH": "C:\\path\\to\\xmail.db",
        "XMAIL_MCP_STDIO": "true",
        "XMAIL_LISTEN_ADDR": ":5570"
      }
    }
  }
}
```

Linux / macOS:

```json
{
  "mcpServers": {
    "xmail": {
      "command": "/path/to/xmail",
      "env": {
        "XMAIL_API_KEY": "CHANGE_ME_api_key",
        "XMAIL_ENCRYPTION_KEY": "CHANGE_ME_base64_32_bytes",
        "XMAIL_DB_PATH": "/path/to/xmail.db",
        "XMAIL_MCP_STDIO": "true",
        "XMAIL_LISTEN_ADDR": ":5570"
      }
    }
  }
}
```

Tips:
- Set `XMAIL_LISTEN_ADDR` to a different port (`:5570`) if another REST instance is already using `:5569`, to avoid a clash.
- `XMAIL_API_KEY` & `XMAIL_ENCRYPTION_KEY` are still required — without them the xmail process fails to start immediately (see [troubleshooting](#14-troubleshooting)).
- Use an absolute `XMAIL_DB_PATH` so it doesn't depend on the client's working directory.

### 5.2 Docker (stdio)

Interactive mode without a TTY so stdin stays available for the protocol:

```bash
docker run --rm -i \
  -e XMAIL_API_KEY=CHANGE_ME \
  -e XMAIL_ENCRYPTION_KEY=CHANGE_ME_base64_32_bytes \
  -e XMAIL_MCP_STDIO=true \
  -v xmail-data:/app/data \
  xmail:<version>-amd64
```

## 6. Example client (Go)

Following xmail's transport test pattern (`internal/mcpserver/server_test.go`, `dialMCP`) — already verified against this server:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/mark3labs/mcp-go/client"
    "github.com/mark3labs/mcp-go/client/transport"
    "github.com/mark3labs/mcp-go/mcp"
)

func main() {
    ctx := context.Background()

    c, err := client.NewStreamableHttpClient(
        "http://localhost:5569/mcp",
        transport.WithHTTPHeaders(map[string]string{
            "X-API-Key": "CHANGE_ME_api_key",
        }),
    )
    if err != nil {
        log.Fatal(err)
    }
    defer c.Close()

    if err := c.Start(ctx); err != nil {
        log.Fatal(err)
    }
    if _, err := c.Initialize(ctx, mcp.InitializeRequest{
        Params: mcp.InitializeParams{
            ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
            ClientInfo:      mcp.Implementation{Name: "my-agent", Version: "0.0.1"},
        },
    }); err != nil {
        log.Fatal(err)
    }

    res, err := c.CallTool(ctx, mcp.CallToolRequest{
        Params: mcp.CallToolParams{
            Name:      "send_email",
            Arguments: map[string]any{
                "account_id": "3f2b...",
                "to":         []string{"bob@example.com"},
                "subject":    "Hello from the agent",
                "body_text":  "Sent via MCP.",
            },
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%+v\n", res)
}
```

## 7. Session flow & full JSON-RPC

Expected order:

```
1. initialize                 -> server replies with capabilities + serverInfo
2. notifications/initialized  -> notification (no response)
3. tools/list                 -> tool list + JSON schema
4. tools/call                 -> call a tool
   (repeat 3/4 as needed)
```

### 7.1 `initialize`

Request:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "initialize",
  "params": {
    "protocolVersion": "2025-06-18",
    "capabilities": {},
    "clientInfo": { "name": "curl", "version": "0.0.0" }
  }
}
```

Response (representative; the `capabilities` field follows the protocol/library version):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2025-06-18",
    "capabilities": {
      "tools": { "listChanged": true }
    },
    "serverInfo": { "name": "xmail", "version": "dev" }
  }
}
```

### 7.2 `notifications/initialized`

```json
{ "jsonrpc": "2.0", "method": "notifications/initialized" }
```

Notification — there is no response.

### 7.3 `tools/list`

Request:

```json
{ "jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {} }
```

Response (simplified; see [section 8](#8-tool-reference-schema--output) for each tool's schema):

```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "tools": [
      { "name": "list_accounts", "description": "List configured email accounts (id, name, email only — never credentials).",
        "inputSchema": { "type": "object", "properties": {} } },
      { "name": "send_email", "description": "Send an email from one of the configured accounts via SMTP.",
        "inputSchema": { "type": "object", "properties": { "...": {} }, "required": ["account_id", "to", "subject"] } },
      { "name": "fetch_emails", "description": "Fetch recent emails from an account's mailbox (IMAP or POP3).",
        "inputSchema": { "type": "object", "properties": { "...": {} }, "required": ["account_id"] } },
      { "name": "check_new_emails", "description": "Check unread/new email counts for an account without downloading messages.",
        "inputSchema": { "type": "object", "properties": { "...": {} }, "required": ["account_id"] } },
      { "name": "list_folders", "description": "List the mailboxes (folders) available on an account's IMAP server, with their delimiter and attributes (e.g. \\Sent). Use this to discover exact folder names before fetching.",
        "inputSchema": { "type": "object", "properties": { "...": {} }, "required": ["account_id"] } }
    ]
  }
}
```

### 7.4 `tools/call`

General shape:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": { "name": "<tool>", "arguments": { "<field>": "<value>" } }
}
```

A successful response contains `content` (fallback JSON text) **and** `structuredContent` (structured data — which the client should read):

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [ { "type": "text", "text": "[{\"id\":\"3f2b...\",\"name\":\"Gmail\",\"email\":\"me@example.com\"}]" } ],
    "structuredContent": [ { "id": "3f2b...", "name": "Gmail", "email": "me@example.com" } ]
  }
}
```

### 7.5 Example for each tool

**`list_accounts`** — arguments `{}`.

```json
{ "jsonrpc":"2.0","id":3,"method":"tools/call",
  "params": { "name":"list_accounts","arguments":{} } }
```

`structuredContent`: array `{id,name,email}`.

**`send_email`**:

```json
{ "jsonrpc":"2.0","id":4,"method":"tools/call",
  "params": { "name":"send_email","arguments":{
    "account_id":"3f2b...","to":["bob@example.com"],
    "subject":"Halo","body_text":"Dikirim lewat MCP.",
    "headers":{"X-Priority":"1"} } } }
```

`structuredContent`: `{ "status": "sent" }`.

**`fetch_emails`**:

```json
{ "jsonrpc":"2.0","id":5,"method":"tools/call",
  "params": { "name":"fetch_emails","arguments":{
    "account_id":"3f2b...","protocol":"imap","folder":"INBOX","limit":5,"refresh":false } } }
```

`structuredContent`: array `Message` (`{uid,folder,subject,from,to,date,is_read,attachments?}`).

**`check_new_emails`**:

```json
{ "jsonrpc":"2.0","id":6,"method":"tools/call",
  "params": { "name":"check_new_emails","arguments":{ "account_id":"3f2b..." } } }
```

`structuredContent`: `{ "unread_count": 5, "new_count": 2 }`.

**`list_folders`**:

```json
{ "jsonrpc":"2.0","id":7,"method":"tools/call",
  "params": { "name":"list_folders","arguments":{ "account_id":"3f2b..." } } }
```

`structuredContent`: array `Folder` (`{name, delimiter?, attributes?}`), sorted by name.

## 8. Tool reference (schema + output)

All tools use an `inputSchema` of type object. Fields without `"required"` are optional.

### 8.1 `list_accounts`

- **Description:** List configured email accounts (id, name, email only — never credentials).
- **Input:** `{}` (no arguments).
- **Output (`structuredContent`):** array `{ id, name, email }`.
- **Notes:** never returns the connection config or credentials.

### 8.2 `send_email`

Input schema:

| Field | Type | Required | Description |
|---|---|:---:|---|
| `account_id` | string | yes | Sender account ID (from `list_accounts`) |
| `to` | string[] | yes | recipient address |
| `cc` | string[] | no | CC |
| `bcc` | string[] | no | BCC |
| `subject` | string | yes | subject |
| `body_text` | string | no | plain-text body |
| `body_html` | string | no | HTML body (alternative to `body_text`) |
| `headers` | object (`map[string]string`) | no | custom headers, e.g. `{"X-Priority":"1"}` |

- **Output:** `{ "status": "sent" }`.
- **Notes:** attachments are **not** supported over MCP (base64 binary in tool-call arguments is considered bad) — send attachments via the REST `POST /accounts/{id}/send`.

### 8.3 `fetch_emails`

| Field | Type | Required | Default | Description |
|---|---|:---:|---|---|
| `account_id` | string | yes | | Account ID |
| `protocol` | string | no | `imap` | `"imap"` or `"pop3"` |
| `folder` | string | no | `INBOX` | folder (IMAP only) |
| `limit` | number | no | `20` | max number of messages |
| `refresh` | boolean | no | `false` | `true` = force a live fetch |

- **Output:** array `Message` (same as REST).
- **Notes:** **no** `offset` parameter (REST has one). Cache-first like REST.

### 8.4 `check_new_emails`

| Field | Type | Required | Default |
|---|---|:---:|---|
| `account_id` | string | yes | |
| `protocol` | string | no | `imap` |
| `folder` | string | no | `INBOX` |

- **Output:** `{ unread_count, new_count }` (same as REST `POST /accounts/{id}/check`).

### 8.5 `list_folders`

| Field | Type | Required | Default | Description |
|---|---|---|---|:---:|
| `account_id` | string | yes | | Account ID |
| `protocol` | string | no | `imap` | only `"imap"` supports folders |

- **Output:** array `Folder` (`{name, delimiter?, attributes?}`), sorted by name.
- **Notes:** always live against the server (not cached). `attributes` contains the folder roles (e.g. `\Sent`, `\Drafts`) so the agent can map important folders itself without guessing names. POP3/SMTP → `isError: true` ("protocol ... has no folders"). Same as REST `GET /accounts/{id}/folders`.

## 9. Error handling

Two classes of failure are distinguished:

1. **Validation/domain** (e.g. empty `account_id`/`to`, account not found, failed to dial the mail server) → returned as a **tool result** with `isError: true`, not a JSON-RPC error:

   ```json
   {
     "jsonrpc": "2.0",
     "id": 7,
     "result": {
       "content": [ { "type": "text", "text": "send_email failed: account: not found" } ],
       "isError": true
     }
   }
   ```

2. **Protocol/transport** (malformed request, unknown method) → **JSON-RPC error**, e.g.:

   ```json
   { "jsonrpc": "2.0", "id": 8, "error": { "code": -32601, "message": "Method not found" } }
   ```

A well-behaved client must check `result.isError` before using `structuredContent`.

## 10. Security

- **Streamable HTTP requires auth:** `/mcp` does not exempt itself from the API-key middleware. Without a valid `X-API-Key` → `401`.
- **stdio in-process:** there is no HTTP hop, but the xmail process still needs `XMAIL_API_KEY`/`XMAIL_ENCRYPTION_KEY` to start; email credentials are still encrypted in SQLite (AES-256-GCM).
- **Credentials never leak:** `list_accounts` only returns `id/name/email`; `send_email`/`fetch_emails`/`check_new_emails` do not return credentials.
- **Logs:** xmail does not log email contents/credentials. For stdio, logs are written to stderr (safe for the MCP stream).
- **Do not** put `X-API-Key` in a public place/URL query — send it as a header. For stdio, store it in the client config's `env`, not in command-line arguments that can be seen in the process list.

## 11. Testing with MCP Inspector

MCP Inspector (a GUI/CLI from MCP) can be used to inspect the tools without a full client:

```bash
npx @modelcontextprotocol/inspector
```

In the Inspector: choose the **Streamable HTTP** transport, URL `http://localhost:5569/mcp`, add the `X-API-Key: <key>` header, then **Connect** → the **Tools** tab for `tools/list`, and call a tool with JSON arguments.

Alternative without the Inspector: use `curl` (section 4.4 / 7) or the Go example (section 6).

## 12. MCP ↔ REST mapping

| MCP tool | REST equivalent |
|---|---|
| `list_accounts` | `GET /accounts` (only `id/name/email`) |
| `send_email` | `POST /accounts/{id}/send` (no attachments) |
| `fetch_emails` | `GET /accounts/{id}/messages` (no `offset`, no `folder` on POP3) |
| `check_new_emails` | `POST /accounts/{id}/check` |
| `list_folders` | `GET /accounts/{id}/folders` |

Because both call the same `account.Service`, the only differences are the input/output shape and the features deliberately not exposed (attachments & offset in MCP).

## 13. Limitations & notes

- `fetch_emails` has no `offset` (pagination is only `limit` + `refresh`).
- `send_email` does not support attachments.
- `check_new_emails` computes `new_count` from the 50 most recent messages vs the cache — not the total mailbox.
- `fetch_emails` is cache-first; use `refresh=true` to guarantee live data.
- POP3: the folder is always `INBOX`, `is_read` is always `true`, `attachments` is empty, `unread_count` = 0.
- Mark-as-read is not exposed as an MCP tool (only REST `POST /accounts/{id}/messages/read`).
- `list_folders` is IMAP-only; POP3/SMTP replies `isError: true` ("protocol ... has no folders"). Folders are always live, not cached.
- Version producer: `serverInfo.version` follows the xmail build version.
- "xmail as an MCP **client**" (xmail calling another MCP server) **does not exist yet** — still backlogged. What exists today is xmail as an MCP **server**.

## 14. Troubleshooting

| Symptom / message | Cause & solution |
|---|---|
| `401 missing or invalid X-API-Key header` | The `X-API-Key` header is missing/wrong. For HTTP, make sure the client forwards the header (section 4). |
| `unexpected content type` / connection refused | The `Accept` header does not include `text/event-stream`. Set `Accept: application/json, text/event-stream`. |
| `tools/list` empty / handshake fails | `initialize` hasn't happened, or the URL/transport is wrong. Make sure the path is `/mcp` and the server is up (`/healthz`). |
| Tool replies `isError: true` | A required argument is missing (`account_id`, `to`, `subject`, `uid`) or the operation failed (account not found, dial failed). Check the text in `content[].text`. |
| stdio: client waits with no reply | `XMAIL_MCP_STDIO=true` is not set, or the binary is not xmail. Make sure the env is set. |
| xmail process dies immediately on stdio | `XMAIL_API_KEY` and/or `XMAIL_ENCRYPTION_KEY` are empty — the server refuses to start. Fill them in the `env` block. |
| Port clash on stdio | Set `XMAIL_LISTEN_ADDR` to another port (e.g. `:5570`) so the internal HTTP server doesn't clash. |
| MCP data differs from REST | It shouldn't (same logic). Check `account_id`, `protocol`/`folder`, and whether one of them uses a different `refresh`/cache. |
| Want to see logs | xmail logs go to **stderr**. HTTP: a `METHOD /path status duration` line. |
