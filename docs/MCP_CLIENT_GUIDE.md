# xmail — Panduan MCP Client

Panduan lengkap menghubungkan **MCP client** (Claude Code, Claude Desktop, Cursor, atau agent/custom client) ke xmail, sehingga agent dapat memakai tool email (`list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`, `list_folders`) langsung dari percakapan.

Implementasi: `internal/mcpserver/server.go` (library `github.com/mark3labs/mcp-go`). Kelima tool tersebut **memanggil `account.Service` yang sama** dengan REST API — jadi MCP dan REST tidak bisa berbeda data (satu sumber logika). Lihat [`ARCHITECTURE.md §1`](../ARCHITECTURE.md).

- Referensi REST API: [`docs/API.md`](./API.md).
- Kontrak tool ringkas: [`PLAN.md §4`](../PLAN.md).

**Daftar isi**

1. [Ringkasan & arsitektur](#1-ringkasan--arsitektur)
2. [Perbandingan transport](#2-perbandingan-transport)
3. [Prasyarat](#3-prasyarat)
4. [Transport 1 — Streamable HTTP](#4-transport-1--streamable-http)
5. [Transport 2 — stdio](#5-transport-2--stdio)
6. [Contoh client (Go)](#6-contoh-client-go)
7. [Alur sesi & JSON-RPC lengkap](#7-alur-sesi--json-rpc-lengkap)
8. [Referensi tool (schema + output)](#8-referensi-tool-schema--output)
9. [Error handling](#9-error-handling)
10. [Keamanan](#10-keamanan)
11. [Uji dengan MCP Inspector](#11-uji-dengan-mcp-inspector)
12. [Pemetaan MCP ↔ REST](#12-pemetaan-mcp--rest)
13. [Batasan & catatan](#13-batasan--catatan)
14. [Troubleshooting](#14-troubleshooting)

---

## 1. Ringkasan & arsitektur

```
                 ┌─────────────────────────────┐
   REST client ──▶│  internal/api (HTTP)        │──┐
                 └─────────────────────────────┘  │
                                                   ▼
                 ┌─────────────────────────────┐  ┌────────────────────┐
   MCP client ──▶│  internal/mcpserver         │─▶│  account.Service   │
   (HTTP /mcp    │  (tools: 5)                 │  │  (satu logika)      │
    atau stdio)  └─────────────────────────────┘  └─────────┬──────────┘
                                                            │
                                    ┌───────────────────────┼───────────────────┐
                                    ▼                       ▼                   ▼
                                 SMTP                    IMAP                POP3
                                    │                       │                   │
                                    └──────────────┬────────┴───────────────────┘
                                                   ▼
                                            SQLite (accounts,
                                            credentials terenkripsi,
                                            messages_cache)
```

Poin penting:

- Endpoint MCP **Streamable HTTP** di-mount di `/mcp` pada **HTTP server yang sama** dengan REST (satu port, satu auth) — bukan port terpisah.
- **stdio** opsional, aktif bila `XMAIL_MCP_STDIO=true`; xmail dijalankan sebagai subprocess oleh client.
- `serverInfo.name` = `xmail`; `serverInfo.version` = versi build xmail yang asli (di-stamp saat rilis, bukan angka hardcoded).

## 2. Perbandingan transport

| Aspek | Streamable HTTP | stdio |
|---|---|---|
| Aktivasi | selalu aktif | `XMAIL_MCP_STDIO=true` |
| Cara client konek | URL `http://<host>:<port>/mcp` | client spawn binary xmail |
| Auth | header `X-API-Key` per request | in-process (tanpa header); proses tetap butuh env untuk start |
| Server HTTP tetap jalan | ya | ya (set `XMAIL_LISTEN_ADDR` berbeda bila perlu) |
| Cocok untuk | remote server, banyak client, Claude Code | desktop app (Claude Desktop), instance lokal |
| Session | stateless | in-process |
| Bagikan DB dengan REST | ya | ya |

## 3. Prasyarat

1. xmail berjalan (`make run`, `go run ./cmd/xmail`, binary, atau Docker). Lihat [`README.MD`](../README.MD).
2. Env wajib (server menolak start bila kosong):
   - `XMAIL_API_KEY` — untuk header `X-API-Key`.
   - `XMAIL_ENCRYPTION_KEY` — base64 dari 32 byte (`openssl rand -base64 32`).
3. Untuk HTTP: port dari `XMAIL_LISTEN_ADDR` (default `:5569`).

Verifikasi:

```bash
curl -s http://localhost:5569/healthz
# {"data":{"status":"ok"},"error":null}
```

## 4. Transport 1 — Streamable HTTP

**Endpoint:** `POST http://<host>:<port>/mcp`

Karakteristik:

- Route `/mcp` ada di belakang middleware API-key yang sama dengan REST → **setiap request wajib** menyertakan `X-API-Key` (hanya `/healthz` yang bebas auth).
- Transport **stateless**: tidak perlu mengelola `Mcp-Session-Id`.
- Header `Accept` harus memuat `application/json` **dan/atau** `text/event-stream`; server bisa membalas JSON biasa atau SSE.

### 4.1 Claude Code

```bash
# transport HTTP
claude mcp add --transport http xmail http://localhost:5569/mcp \
  --header "X-API-Key: $XMAIL_API_KEY"

# verifikasi
claude mcp list
```

Default scope bersifat lokal ke project. Tambahkan `--scope user` bila ingin tersedia di semua project:

```bash
claude mcp add --scope user --transport http xmail http://localhost:5569/mcp \
  --header "X-API-Key: $XMAIL_API_KEY"
```

Setelah terhubung, di sesi Claude Code cukup minta secara natural, mis. "pakai xmail untuk list akun", "kirim email ke bob@example.com", atau cek status server dengan `/mcp`.

### 4.2 Konfigurasi JSON generik

Banyak client membaca config `mcpServers`:

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

> Nama field bervariasi antar-client (`type`: `http` vs `streamable-http`; `headers` vs `httpHeaders`). Yang wajib benar: **URL** dan **header `X-API-Key`**.

### 4.3 Claude Desktop (remote via `mcp-remote`)

Claude Desktop lebih umum memakai stdio (bagian 5), tapi endpoint HTTP bisa dijembatani:

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

### 4.4 Uji manual via `curl` (JSON-RPC)

Lihat contoh request/response lengkap di [bagian 7](#7-alur-sesi--json-rpc-lengkap). Ringkasnya:

```bash
curl -s -X POST http://localhost:5569/mcp \
  -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{
        "protocolVersion":"2025-06-18","capabilities":{},
        "clientInfo":{"name":"curl","version":"0.0.0"}}}'
```

Bila balasan bertipe `text/event-stream`, payload JSON-RPC ada pada baris yang diawali `data:`.

## 5. Transport 2 — stdio

Aktifkan dengan `XMAIL_MCP_STDIO=true`. xmail membaca/menulis protokol MCP lewat **stdin/stdout** proses, sementara log server tetap ke **stderr** (jadi tidak mengotori stream MCP). Client tinggal menjalankan binary xmail.

Alur: client spawn proses → kirim `initialize` lewat stdin → terima response lewat stdout → dst.

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
- Set `XMAIL_LISTEN_ADDR` ke port berbeda (`:5570`) bila instance REST lain sudah memakai `:5569`, agar tidak bentrok.
- `XMAIL_API_KEY` & `XMAIL_ENCRYPTION_KEY` tetap wajib — tanpa itu proses xmail langsung gagal start (lihat [troubleshooting](#14-troubleshooting)).
- Gunakan `XMAIL_DB_PATH` absolut supaya tidak bergantung working directory client.

### 5.2 Docker (stdio)

Mode interaktif tanpa TTY agar stdin tetap dipakai protokol:

```bash
docker run --rm -i \
  -e XMAIL_API_KEY=CHANGE_ME \
  -e XMAIL_ENCRYPTION_KEY=CHANGE_ME_base64_32_bytes \
  -e XMAIL_MCP_STDIO=true \
  -v xmail-data:/app/data \
  xmail:<version>-amd64
```

## 6. Contoh client (Go)

Mengikuti pola test transport xmail (`internal/mcpserver/server_test.go`, `dialMCP`) — sudah terverifikasi terhadap server ini:

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
                "subject":    "Halo dari agent",
                "body_text":  "Dikirim lewat MCP.",
            },
        },
    })
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("%+v\n", res)
}
```

## 7. Alur sesi & JSON-RPC lengkap

Urutan yang diharapkan:

```
1. initialize                 -> server balas capabilities + serverInfo
2. notifications/initialized  -> notifikasi (tanpa response)
3. tools/list                 -> daftar tool + JSON schema
4. tools/call                 -> panggil tool
   (ulang 3/4 sesuai kebutuhan)
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

Response (representatif; field `capabilities` mengikuti versi protokol/library):

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

Notifikasi — tidak ada response.

### 7.3 `tools/list`

Request:

```json
{ "jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {} }
```

Response (disederhanakan; lihat [bagian 8](#8-referensi-tool-schema--output) untuk schema tiap tool):

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

Bentuk umum:

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": { "name": "<tool>", "arguments": { "<field>": "<value>" } }
}
```

Response sukses memuat `content` (teks JSON cadangan) **dan** `structuredContent` (data terstruktur — yang sebaiknya dibaca client):

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

### 7.5 Contoh tiap tool

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

`structuredContent`: array `Folder` (`{name, delimiter?, attributes?}`), diurutkan berdasarkan nama.

## 8. Referensi tool (schema + output)

Semua tool memakai `inputSchema` bertipe object. Field tanpa `"required"` bersifat opsional.

### 8.1 `list_accounts`

- **Deskripsi:** List configured email accounts (id, name, email only — never credentials).
- **Input:** `{}` (tanpa argumen).
- **Output (`structuredContent`):** array `{ id, name, email }`.
- **Catatan:** tidak pernah mengembalikan connection config maupun credential.

### 8.2 `send_email`

Input schema:

| Field | Tipe | Wajib | Deskripsi |
|---|---|:---:|---|
| `account_id` | string | ya | ID akun pengirim (dari `list_accounts`) |
| `to` | string[] | ya | alamat penerima |
| `cc` | string[] | tidak | CC |
| `bcc` | string[] | tidak | BCC |
| `subject` | string | ya | subjek |
| `body_text` | string | tidak | body plain-text |
| `body_html` | string | tidak | body HTML (alternatif `body_text`) |
| `headers` | object (`map[string]string`) | tidak | custom header, mis. `{"X-Priority":"1"}` |

- **Output:** `{ "status": "sent" }`.
- **Catatan:** attachment **tidak** didukung di MCP (base64 binary di argumen tool call dianggap buruk) — kirim attachment lewat REST `POST /accounts/{id}/send`.

### 8.3 `fetch_emails`

| Field | Tipe | Wajib | Default | Deskripsi |
|---|---|:---:|---|---|
| `account_id` | string | ya | | ID akun |
| `protocol` | string | tidak | `imap` | `"imap"` atau `"pop3"` |
| `folder` | string | tidak | `INBOX` | folder (IMAP saja) |
| `limit` | number | tidak | `20` | jumlah maks pesan |
| `refresh` | boolean | tidak | `false` | `true` = paksa live-fetch |

- **Output:** array `Message` (sama dengan REST).
- **Catatan:** **tanpa** parameter `offset` (REST punya). Cache-first seperti REST.

### 8.4 `check_new_emails`

| Field | Tipe | Wajib | Default |
|---|---|:---:|---|
| `account_id` | string | ya | |
| `protocol` | string | tidak | `imap` |
| `folder` | string | tidak | `INBOX` |

- **Output:** `{ unread_count, new_count }` (sama dengan REST `POST /accounts/{id}/check`).

### 8.5 `list_folders`

| Field | Tipe | Wajib | Default | Deskripsi |
|---|---|---|---|:---:|
| `account_id` | string | ya | | ID akun |
| `protocol` | string | tidak | `imap` | hanya `"imap"` yang mendukung folder |

- **Output:** array `Folder` (`{name, delimiter?, attributes?}`), diurutkan berdasarkan nama.
- **Catatan:** selalu live ke server (tidak di-cache). `attributes` memuat peran folder (mis. `\Sent`, `\Drafts`) sehingga agent bisa memetakan folder penting sendiri tanpa menebak nama. POP3/SMTP → `isError: true` ("protocol ... has no folders"). Sama dengan REST `GET /accounts/{id}/folders`.

## 9. Error handling

Dua kelas kegagalan dibedakan:

1. **Validasi/domain** (mis. `account_id`/`to` kosong, akun tak ada, gagal dial server mail) → dikembalikan sebagai **tool result** dengan `isError: true`, bukan JSON-RPC error:

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

2. **Protokol/transport** (request malformed, method tak dikenal) → **JSON-RPC error**, mis.:

   ```json
   { "jsonrpc": "2.0", "id": 8, "error": { "code": -32601, "message": "Method not found" } }
   ```

Client yang baik harus memeriksa `result.isError` sebelum memakai `structuredContent`.

## 10. Keamanan

- **Streamable HTTP wajib auth:** `/mcp` tidak mengecualikan diri dari middleware API-key. Tanpa `X-API-Key` valid → `401`.
- **stdio in-process:** tidak ada hop HTTP, tapi proses xmail tetap butuh `XMAIL_API_KEY`/`XMAIL_ENCRYPTION_KEY` untuk start; kredensial email tetap dienkripsi di SQLite (AES-256-GCM).
- **Kredensial tidak pernah bocor:** `list_accounts` hanya mengembalikan `id/name/email`; `send_email`/`fetch_emails`/`check_new_emails` tidak mengembalikan kredensial.
- **Log:** xmail tidak mencatat isi email/kredensial. Untuk stdio, log ditulis ke stderr (aman untuk stream MCP).
- **Jangan** menaruh `X-API-Key` di tempat publik/URL query — kirim sebagai header. Untuk stdio, simpan di `env` client config, bukan argumen command line yang bisa terlihat di process list.

## 11. Uji dengan MCP Inspector

MCP Inspector (GUI/CLI dari MCP) bisa dipakai untuk memeriksa tool tanpa client penuh:

```bash
npx @modelcontextprotocol/inspector
```

Di Inspector: pilih transport **Streamable HTTP**, URL `http://localhost:5569/mcp`, tambahkan header `X-API-Key: <key>`, lalu **Connect** → tab **Tools** untuk `tools/list`, dan panggil tool dengan arguments JSON.

Alternatif tanpa Inspector: gunakan `curl` (bagian 4.4 / 7) atau contoh Go (bagian 6).

## 12. Pemetaan MCP ↔ REST

| MCP tool | REST padanan |
|---|---|
| `list_accounts` | `GET /accounts` (hanya `id/name/email`) |
| `send_email` | `POST /accounts/{id}/send` (tanpa attachment) |
| `fetch_emails` | `GET /accounts/{id}/messages` (tanpa `offset`, tanpa `folder` pada POP3) |
| `check_new_emails` | `POST /accounts/{id}/check` |
| `list_folders` | `GET /accounts/{id}/folders` |

Karena keduanya memanggil `account.Service` yang sama, perbedaan hanya pada bentuk input/output dan fitur yang sengaja tidak diekspos (attachment & offset di MCP).

## 13. Batasan & catatan

- `fetch_emails` tidak punya `offset` (paginasi hanya `limit` + `refresh`).
- `send_email` tidak mendukung attachment.
- `check_new_emails` menghitung `new_count` dari 50 pesan terbaru vs cache — bukan total mailbox.
- `fetch_emails` cache-first; pakai `refresh=true` untuk menjamin data live.
- POP3: folder selalu `INBOX`, `is_read` selalu `true`, `attachments` kosong, `unread_count` = 0.
- Mark-as-read tidak diekspos sebagai tool MCP (hanya REST `POST /accounts/{id}/messages/read`).
- `list_folders` hanya untuk IMAP; POP3/SMTP membalas `isError: true` ("protocol ... has no folders"). Folder selalu live, tidak di-cache.
- Produsen versi: `serverInfo.version` mengikuti versi build xmail.
- "xmail sebagai MCP **client**" (xmail memanggil MCP server lain) **belum ada** — masih backlog (PLAN.md Fase 9). Yang ada saat ini adalah xmail sebagai MCP **server**.

## 14. Troubleshooting

| Gejala / pesan | Penyebab & solusi |
|---|---|
| `401 missing or invalid X-API-Key header` | Header `X-API-Key` tidak dikirim/salah. Untuk HTTP pastikan header diteruskan client (bagian 4). |
| `unexpected content type` / koneksi ditolak | Header `Accept` tidak memuat `text/event-stream`. Set `Accept: application/json, text/event-stream`. |
| `tools/list` kosong / handshake gagal | Belum `initialize`, atau URL/transport salah. Pastikan path `/mcp` dan server hidup (`/healthz`). |
| Tool balas `isError: true` | Argumen wajib kurang (`account_id`, `to`, `subject`, `uid`) atau operasi gagal (akun tak ada, gagal dial). Cek teks di `content[].text`. |
| stdio: client menunggu tanpa balasan | `XMAIL_MCP_STDIO=true` belum di-set, atau binary bukan xmail. Pastikan env terisi. |
| Proses xmail langsung mati saat stdio | `XMAIL_API_KEY` dan/atau `XMAIL_ENCRYPTION_KEY` kosong — server menolak start. Isi di blok `env`. |
| Port bentrok saat stdio | Set `XMAIL_LISTEN_ADDR` ke port lain (mis. `:5570`) agar HTTP server internal tidak bentrok. |
| Data MCP beda dengan REST | Seharusnya tidak bisa (logika sama). Cek `account_id`, `protocol`/`folder`, dan apakah salah satu memakai `refresh`/cache yang berbeda. |
| Ingin melihat log | Log xmail ke **stderr**. HTTP: baris `METHOD /path status durasi`. |
