# Postman — xmail

Folder ini berisi koleksi Postman siap import untuk REST API xmail (dan request MCP mentah).

| File | Isi |
|---|---|
| `xmail.postman_collection.json` | Koleksi utama: semua endpoint REST + contoh skenario error + request MCP |
| `xmail.postman_environment.json` | Environment `xmail (local)`: `baseUrl`, `apiKey`, `accountId`, `messageUid` |

## Cara import

1. Postman → **Import** → pilih **kedua** file di atas (collection + environment).
2. Pilih environment **xmail (local)** di dropdown kanan atas.
3. Set variabel `apiKey` = `XMAIL_API_KEY` server (default dev: `changeme`).
4. Jalankan **Health → Healthz (no auth)** untuk memverifikasi server hidup.
5. Jalankan **Accounts → Create Account** — test script menyimpan `id` ke `accountId` secara otomatis.

## Variabel

| Variabel | Default | Diisi oleh |
|---|---|---|
| `baseUrl` | `http://localhost:5569` | manual |
| `apiKey` | `changeme` | manual (sesuai env server) |
| `accountId` | (kosong) | otomatis oleh *Create Account* |
| `messageUid` | (kosong) | otomatis oleh *Fetch Messages* |

## Struktur folder

- **Health** — health check tanpa auth.
- **Accounts** — CRUD akun + test-connection (SMTP/IMAP/POP3) + skenario validasi (tanpa protokol, POP3 starttls) + 404 setelah delete.
- **Mail** — kirim (plain, HTML+attachment+headers, tanpa penerima), fetch (cache-first, refresh, POP3), check, mark-read (+ tanpa `uid`).
- **Errors (examples)** — 401 (tanpa key), 400 (field tak dikenal), 404 (akun tidak ada).
- **MCP** — `initialize`, `tools/list`, `tools/call` (`list_accounts`, `fetch_emails`) via Streamable HTTP.

## Catatan

- Auth `X-API-Key` di-set di level koleksi (auth type: API Key, header `X-API-Key`). Request **Healthz** dan **Unauthorized** meng-override jadi `noauth`.
- Test script di level koleksi memverifikasi setiap response memakai envelope `{data, error}` — termasuk response error.
- Request MCP memakai header `Accept: application/json, text/event-stream` (wajib untuk transport Streamable HTTP). Bila balasannya SSE, payload ada pada baris `data:`.
- Untuk pemakaian MCP sehari-hari, lebih baik pakai MCP client sungguhan — lihat [`../MCP_CLIENT_GUIDE.md`](../MCP_CLIENT_GUIDE.md).

Dokumentasi API lengkap: [`../API.md`](../API.md).
