# xmail — REST API Reference

Referensi lengkap REST API xmail: konsep dasar, autentikasi, envelope response, semantik paginasi/cache, konfigurasi TLS per provider, dan detail tiap endpoint (parameter, status code, contoh request/response).

- Kode handler: `internal/api/` (semua adapter tipis di atas `account.Service`).
- Logika bisnis: `internal/account/service.go`.
- Import Postman siap pakai: [`docs/postman/`](./postman/).
- Panduan MCP client: [`docs/MCP_CLIENT_GUIDE.md`](./MCP_CLIENT_GUIDE.md).
- Cara menambah endpoint baru: [`ARCHITECTURE.md §5.1`](../ARCHITECTURE.md).

**Daftar isi**

1. [Konsep dasar](#1-konsep-dasar)
2. [Quickstart](#2-quickstart)
3. [Autentikasi](#3-autentikasi)
4. [Envelope response & kode error](#4-envelope-response--kode-error)
5. [Paginasi & semantik cache](#5-paginasi--semantik-cache)
6. [Mode TLS & contoh provider](#6-mode-tls--contoh-provider)
7. [Referensi endpoint](#7-referensi-endpoint)
8. [Referensi tipe data](#8-referensi-tipe-data)
9. [Walkthrough end-to-end](#9-walkthrough-end-to-end)
10. [Troubleshooting / FAQ](#10-troubleshooting--faq)
11. [Import ke Postman](#11-import-ke-postman)
12. [Dashboard web](#12-dashboard-web)

---

## 1. Konsep dasar

xmail adalah backend "email-as-a-service": menyimpan beberapa **Account** (mailbox), mengirim email atas nama akun tersebut (SMTP), dan membaca mailbox-nya (IMAP/POP3). Operasi yang sama juga diekspos lewat MCP untuk AI agent — REST dan MCP memanggil `account.Service` yang sama, jadi datanya identik.

Istilah inti (lihat [`CONTEXT.md`](../CONTEXT.md) untuk glosarium lengkap):

| Istilah | Arti |
|---|---|
| **Account** | Satu mailbox terkonfigurasi (nama, email, username, koneksi per protokol). Tidak pernah membawa kredensial di response. |
| **Mailer protocol** | `smtp` (kirim), `imap` (fetch/check/mark-read), `pop3` (fetch saja). Satu akun boleh mengonfigurasi sebagian saja. |
| **Connection** | `{host, port, tls_mode}` untuk satu protokol pada satu akun. |
| **Message** | Metadata satu email: UID, subject, from, to, date, is_read, nama-nama attachment. **Bukan** body. |
| **Folder** | Mailbox bernama di server. IMAP bisa banyak; POP3 hanya punya `INBOX`. |
| **Fetch window** | Sepotong mailbox, terbaru dulu: `limit` pesan terbaru, melewati `offset`. |
| **Message cache** | Store lokal metadata Message, di-key `(account, protocol, folder)`. |
| **Coverage / Exhausted** | Seberapa banyak pesan terbaru yang dijamin ada di cache / seluruh mailbox sudah tercache. |
| **Check** | Poll mailbox: jumlah unread + berapa pesan terbaru yang belum ada di cache. |

Kemampuan per protokol:

| Protokol | Kirim | Fetch | Check (unread) | Mark read | Attachment (nama) | Folder |
|---|:---:|:---:|:---:|:---:|:---:|:---:|
| SMTP | ya | — | — | — | — | — |
| IMAP | — | ya | ya | ya | ya | banyak |
| POP3 | — | ya | tidak | tidak | tidak | `INBOX` saja |

Catatan POP3: tidak punya konsep flag per-pesan, jadi `is_read` selalu `true`, `unread_count` selalu `0`, dan `POST /messages/read` mengembalikan error. TLS `starttls` juga **tidak didukung** POP3.

## 2. Quickstart

```bash
export BASE=http://localhost:5569
export XMAIL_API_KEY=changeme   # samakan dengan env server

# Health (tanpa auth)
curl -s $BASE/healthz

# Buat akun
curl -s -X POST $BASE/accounts \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Gmail","email":"me@example.com","username":"me@example.com","password":"app-pw",
       "smtp":{"host":"smtp.example.com","port":587,"tls_mode":"starttls"},
       "imap":{"host":"imap.example.com","port":993,"tls_mode":"tls"}}'

# Kirim
curl -s -X POST $BASE/accounts/<ID>/send \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"to":["bob@example.com"],"subject":"Hi","body_text":"Halo"}'

# Fetch (live)
curl -s "$BASE/accounts/<ID>/messages?limit=10&refresh=true" -H "X-API-Key: $XMAIL_API_KEY"
```

## 3. Autentikasi

| Item | Nilai |
|---|---|
| Header | `X-API-Key: <XMAIL_API_KEY>` |
| Sumber key | env `XMAIL_API_KEY` (satu key statis untuk MVP) |
| Perbandingan | constant-time (`crypto/subtle`) — aman dari timing attack |
| Berlaku untuk | semua endpoint **kecuali** `GET /healthz` |
| Key tidak valid/hilang | `401` dengan `error.code = "unauthorized"` |

```bash
# ditolak (401)
curl -s $BASE/accounts
# {"data":null,"error":{"code":"unauthorized","message":"missing or invalid X-API-Key header"}}
```

Catatan: key **tidak** disimpan/hash di database — dibaca dari env tiap request. Multi-key/rotatable API key masih backlog (tabel `api_keys` sudah ada tapi belum dipakai).

## 4. Envelope response & kode error

Setiap response — sukses maupun gagal, semua endpoint — memakai bentuk yang sama:

```json
// Sukses
{ "data": <payload>, "error": null }

// Gagal
{ "data": null, "error": { "code": "<kode>", "message": "<penjelasan>" } }
```

`DELETE` pun bukan `204`; ia tetap `200` dengan envelope.

| `error.code` | HTTP | Penyebab |
|---|---|---|
| `unauthorized` | 401 | Header `X-API-Key` hilang atau salah |
| `invalid_json` | 400 | Body bukan JSON valid, atau ada field tak dikenal (decoder pakai `DisallowUnknownFields`) |
| `validation_failed` | 400 | Field wajib kosong, `tls_mode` tidak valid, POP3 `starttls`, `uid` kosong, protokol tidak mendukung operasi, akun tidak punya konfigurasi protokol tsb, dsb |
| `not_found` | 404 | `account_id` tidak ada (termasuk setelah dihapus) |
| `internal_error` | 500 | Error lain: gagal dial server mail, kredensial salah saat kirim, masalah DB/query, dll |

Penting: error koneksi/kirim ke server mail membawa pesan asli dari library protokol (mis. `smtp: send: ...`, `imap: login: ...`) tetapi dibungkus kode `internal_error`. Error yang disebabkan input pemanggil (validasi) memakai `validation_failed`.

Body tidak boleh punya field tak dikenal — decoder menolaknya sebagai `invalid_json`. Contoh: mengirim `{"to":["a@b.c"],"Subject":"x"}` (huruf besar) akan gagal.

## 5. Paginasi & semantik cache

Ini bagian yang paling sering disalahpahami. `GET /accounts/{id}/messages` menerima:

| Param | Default | Arti |
|---|---|---|
| `limit` | `20` | jumlah maks pesan dikembalikan. `<= 0` → default 20 |
| `offset` | `0` | lewati N pesan **terbaru** |
| `folder` | `INBOX` | folder IMAP (POP3 mengabaikan, selalu INBOX) |
| `protocol` | `imap` | `imap` \| `pop3` |
| `refresh` | `false` | `true`/`1` = paksa live dial, abaikan cache |

**Window** = `limit` pesan terbaru, melewati `offset`. Contoh dengan mailbox 100 pesan (UID terbesar = terbaru):

| Request | Hasil |
|---|---|
| `?limit=20&offset=0` | 20 pesan terbaru |
| `?limit=20&offset=20` | 20 pesan berikutnya (peringkat 21–40) |
| `?limit=20&offset=200` | `[]` (di luar jumlah pesan) |
| `?limit=500` | semua pesan yang ada (page pendek) |

**Cache-first.** Secara default, xmail melayani dari `messages_cache` bila cache sudah **mencakup** window yang diminta — tanpa dial server. Kalau cache belum mencakupnya (mis. window lebih besar dari yang pernah diambil), xmail dial, simpan, lalu balas. `refresh=true` selalu dial.

State yang menentukan apakah cache "mencakup" sebuah window:

- **Coverage** — berapa banyak pesan terbaru yang diketahui tercache berurutan dari atas mailbox.
- **Exhausted** — seluruh mailbox sudah tercache, jadi window apa pun bisa dijawab dari cache.

Konsekuensi praktis:

- Request yang sama dua kali berturut-turut: yang kedua **tidak** dial (hemat koneksi).
- Minta `limit` lebih besar dari sebelumnya (mis. `limit=50` setelah `limit=20`): cache tidak mencakup → **dial** (tidak diam-diam memotong hasil lama).
- `POST /check` selalu dial dan mengisi cache untuk 50 pesan terbaru.
- `refresh=true` memaksa live dial walau cache sudah mencakup.

Cache di-key `(account, protocol, folder)` — jadi mengambil protokol/folder berbeda punya cache terpisah, dan `DELETE /accounts/{id}` menghapus seluruh cache akun tsb.

Konsistensi: bila server melaporkan mailbox berubah di atas (mail baru masuk, atau mail dihapus) sehingga posisi cache bergeser, xmail membuang cache key tsb alih-alih menyajikan baris dengan posisi basi. Jadi hasil "dari cache" selalu berupa prefix yang koheren; kalau ragu, pakai `refresh=true`.

## 6. Mode TLS & contoh provider

`tls_mode` per protokol:

| Nilai | Arti | Contoh port umum |
|---|---|---|
| `tls` | TLS implicit (koneksi langsung TLS) | SMTP 465, IMAP 993, POP3 995 |
| `starttls` | konek plaintext lalu upgrade STARTTLS (wajib sukses) | SMTP 587, IMAP 143 |
| `none` | tanpa TLS sama sekali — **harus dipilih eksplisit** | port test/local |

Aturan validasi:
- `tls_mode` wajib ada dan salah satu dari tiga nilai di atas untuk tiap protokol yang diisi.
- `port` wajib 1–65535.
- `pop3.tls_mode = "starttls"` **ditolak** saat save (library POP3 tidak mendukung) — pakai `tls` atau `none`.
- Certificate validation aktif default; `none` hanya untuk yang tahu risikonya (kredensial terkirim plaintext).

Contoh konfigurasi provider umum (verifikasi ulang di dokumentasi provider — xmail **tidak** mendukung OAuth2, jadi email yang mewajibkan OAuth/app-password perlu app-password):

| Provider | SMTP | IMAP | POP3 |
|---|---|---|---|
| Gmail | `smtp.gmail.com:587` / `starttls` | `imap.gmail.com:993` / `tls` | `pop.gmail.com:995` / `tls` |
| Microsoft 365 / Outlook | `smtp.office365.com:587` / `starttls` | `outlook.office365.com:993` / `tls` | (umumnya nonaktif) |
| Yahoo Mail | `smtp.mail.yahoo.com:465` / `tls` | `imap.mail.yahoo.com:993` / `tls` | `pop.mail.yahoo.com:995` / `tls` |
| Local/dev (mis. MailHog) | `localhost:1025` / `none` | `localhost:1143` / `none` | `localhost:1110` / `none` |

Password Gmail/Outlook kemungkinan besar adalah **app-password**, bukan password login biasa.

## 7. Referensi endpoint

Ringkasan:

| Method | Path | Auth | Deskripsi |
|---|---|:---:|---|
| GET | `/healthz` | tidak | Health check |
| GET | `/` | tidak | Dashboard web — aset statis HTML/CSS/JS (§12) |
| POST | `/accounts` | ya | Buat akun → `201` |
| GET | `/accounts` | ya | List akun → `200` |
| GET | `/accounts/{id}` | ya | Detail akun → `200` / `404` |
| PUT | `/accounts/{id}` | ya | Update akun (full replace) → `200` |
| DELETE | `/accounts/{id}` | ya | Hapus akun → `200` |
| POST | `/accounts/{id}/test-connection` | ya | Uji koneksi+auth → `200` |
| POST | `/accounts/{id}/send` | ya | Kirim email → `200` |
| GET | `/accounts/{id}/messages` | ya | Fetch (cache-first) → `200` |
| POST | `/accounts/{id}/check` | ya | Cek email baru → `200` |
| POST | `/accounts/{id}/messages/read` | ya | Tandai read (IMAP) → `200` |
| POST | `/mcp` | ya | MCP Streamable HTTP (lihat guide MCP) |

---

### 7.1 `GET /healthz`

- **Auth:** tidak.
- **Response `200`:**

  ```json
  { "data": { "status": "ok" }, "error": null }
  ```

Dipakai container orchestrator (Docker/k8s) untuk probe liveness tanpa perlu key.

---

### 7.2 `POST /accounts` — buat akun

- **Auth:** ya.
- **Body:** [`accountRequest`](#81-accountrequest) (`password` **wajib**).
- **Response `201`:** [`accountResponse`](#82-accountresponse) (tanpa kredensial).

Contoh request:

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

Contoh response:

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

| Code | Kapan |
|---|---|
| `201` | Berhasil dibuat |
| `400 invalid_json` | Body bukan JSON / ada field tak dikenal |
| `400 validation_failed` | Field wajib kosong, tidak ada protokol, `tls_mode` invalid, POP3 `starttls` |
| `500 internal_error` | Gagal enkripsi/simpan |

Catatan: `id` digenerate server (UUID). Kredensial dienkripsi AES-256-GCM sebelum disimpan; response **tidak pernah** memuat `password`.

---

### 7.3 `GET /accounts` — list akun

- **Auth:** ya. Tanpa parameter.
- **Response `200`:** array `accountResponse`, diurutkan berdasarkan `name`. Bisa `[]`.

```json
{ "data": [ { "id": "...", "name": "Gmail Kerja", "email": "...", "username": "...",
             "smtp": {...}, "imap": {...}, "pop3": null,
             "created_at": "...", "updated_at": "..." } ], "error": null }
```

---

### 7.4 `GET /accounts/{id}` — detail akun

- **Response `200`:** satu `accountResponse`.
- **`404 not_found`** bila id tidak ada.

---

### 7.5 `PUT /accounts/{id}` — update akun

- **Body:** [`accountRequest`](#81-accountrequest), tapi `password` **opsional**.
- **Response `200`:** `accountResponse` hasil update.

**Full replace, bukan merge.** Semua field selain `password` wajib dikirim ulang; field protokol (`smtp`/`imap`/`pop3`) yang **tidak** dikirim akan di-`NULL`-kan (efektif menghapus konfigurasi protokol itu). Satu-satunya field opsional adalah `password`:

| Nilai `password` | Efek |
|---|---|
| dikirim string | kredensial diganti dengan nilai baru |
| `null` / tidak dikirim | kredensial lama dipertahankan |

Contoh — ganti nama & hapus konfigurasi POP3, pertahankan kredensial:

```bash
curl -s -X PUT $BASE/accounts/$ID \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Gmail (renamed)","email":"me@example.com","username":"me@example.com",
       "imap":{"host":"imap.gmail.com","port":993,"tls_mode":"tls"}}'
# Catatan: smtp & pop3 tidak dikirim -> jadi null
```

Status: `200` sukses; `400` validasi; `404` bila id tidak ada.

---

### 7.6 `DELETE /accounts/{id}` — hapus akun

- **Response `200`:** `{ "data": { "deleted": true }, "error": null }`.
- **`404 not_found`** bila id tidak ada.

Menghapus akun sekaligus kredensial & seluruh `messages_cache`-nya (cascade).

---

### 7.7 `POST /accounts/{id}/test-connection` — uji koneksi

- **Body:** `{ "protocol": "smtp" | "imap" | "pop3" }` (opsional, default `imap`).
- **Response `200`:** `{ "data": { "ok": true }, "error": null }`.

Melakukan connect + login ke protokol yang dipilih **tanpa** mengirim/mengambil apa pun. Cocok untuk memverifikasi kredensial/config sebelum dipakai.

| Code | Kapan |
|---|---|
| `200` | Konek + auth sukses |
| `400 validation_failed` | `protocol` tidak dikenal, atau akun tidak punya konfigurasi protokol tsb |
| `404 not_found` | `account_id` tidak ada |
| `500 internal_error` | Gagal connect/login (host salah, kredensial salah, TLS mismatch, dst) |

---

### 7.8 `POST /accounts/{id}/send` — kirim email

- **Body:** [`sendRequest`](#84-sendrequest).
- **Response `200`:** `{ "data": { "status": "sent" }, "error": null }`.

Field body:

| Field | Wajib | Catatan |
|---|:---:|---|
| `to` | ya | array email, minimal 1 |
| `cc` / `bcc` | tidak | array email |
| `subject` | tidak | |
| `body_text` | tidak | body plain-text |
| `body_html` | tidak | body HTML |
| `attachments` | tidak | `{filename, content_type, data_base64}` |
| `headers` | tidak | `map[string]string`, custom header dikirim apa adanya |

Aturan body/From:
- **From** = `account.email` (di-set otomatis; tidak bisa di-override lewat body).
- Kalau `body_html` **dan** `body_text` diisi → dikirim `multipart/alternative` (HTML utama, plain-text alternatif).
- Kalau hanya `body_html` → body HTML. Selain itu → plain-text (`body_text`, boleh kosong).
- `data_base64` di `attachments` di-decode base64 standar; base64 tidak valid → `400 validation_failed`.
- Saat ini `content_type` pada attachment **tidak dipakai** — tipe MIME lampiran ditentukan otomatis oleh library SMTP dari nama file. Isi tetap dikirim (`data_base64`).

Contoh:

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

Status: `200` terkirim; `400` (tanpa `to`, base64 invalid, JSON invalid); `404` id tak ada; `500` gagal kirim/dial/auth.

---

### 7.9 `GET /accounts/{id}/messages` — fetch email

- **Query:** [`limit`, `offset`, `folder`, `protocol`, `refresh`](#5-paginasi--semantik-cache).
- **Response `200`:** array [`Message`](#83-message) (terbaru dulu).

Contoh:

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

Catatan per protokol:

| | IMAP | POP3 |
|---|---|---|
| Sumber urutan | UID menurun (UID naik sesuai kedatangan) | urutan UIDL, dibalik (terbaru dulu) |
| `is_read` | dari flag `\Seen` | selalu `true` (POP3 tak punya flag) |
| `attachments` | nama file dari BODYSTRUCTURE | selalu kosong (TOP tak memuat struktur body) |
| `folder` | folder yang diminta | selalu `INBOX` |
| Body email | tidak diunduh (metadata saja) | tidak diunduh (header saja) |

Status: `200` sukses (termasuk `[]` bila window kosong); `400 validation_failed` bila `protocol` SMTP/invalid atau protokol tak terkonfigurasi; `404` id tak ada; `500` gagal dial/fetch.

---

### 7.10 `POST /accounts/{id}/check` — cek email baru

- **Body:** `{ "protocol": "imap" | "pop3", "folder": "INBOX" }` (keduanya opsional; default `imap`/`INBOX`).
- **Response `200`:** [`CheckResult`](#85-checkresult).

```json
{ "data": { "unread_count": 5, "new_count": 2 }, "error": null }
```

Semantik:

- **`unread_count`** — jumlah unread yang dilaporkan server (IMAP `STATUS ... UNSEEN`). POP3 tidak punya konsep unread → `0`.
- **`new_count`** — berapa dari **50 pesan terbaru** yang belum ada di `messages_cache` xmail ("baru sejak check terakhir"). Ini **bukan** konsep protokol; dihitung lokal dari cache.
- Endpoint ini **selalu** dial ke server (tidak cache-first) dan memperbarui cache untuk 50 pesan terbaru — sehingga `GET /messages` dengan window dalam rentang itu setelahnya bisa dilayani dari cache.

Status: `200`; `400` protokol invalid/tak terkonfigurasi; `404`; `500` gagal dial.

---

### 7.11 `POST /accounts/{id}/messages/read` — tandai sudah dibaca

- **Body:**

  | Field | Wajib | Catatan |
  |---|:---:|---|
  | `uid` | ya | UID pesan (dari hasil `/messages`) |
  | `protocol` | tidak | default `imap` |
  | `folder` | tidak | default `INBOX` |

- **Response `200`:** `{ "data": { "marked_read": true }, "error": null }`.

**IMAP only** (menset flag `\Seen`). Untuk `protocol=pop3` → `400 validation_failed` karena POP3 tak punya flag per-pesan. Cache lokal ikut diperbarui sehingga fetch ber-cache berikutnya konsisten.

Status: `200`; `400` (`uid` kosong, POP3, UID imap tidak valid); `404`; `500` gagal dial/store.

## 8. Referensi tipe data

### 8.1 `accountRequest`

Body `POST`/`PUT /accounts`.

| Field | Tipe | Wajib (create) | Wajib (update) | Catatan |
|---|---|:---:|:---:|---|
| `name` | string | ya | ya | nama tampilan |
| `email` | string | ya | ya | jadi alamat **From** saat kirim |
| `username` | string | ya | ya | username login server |
| `password` | string \| null | ya | tidak | `null` di update = pertahankan kredensial lama |
| `smtp` | `Connection` \| null | salah satu protokol | ya (bila ingin dipertahankan) | |
| `imap` | `Connection` \| null | salah satu protokol | ya | |
| `pop3` | `Connection` \| null | salah satu protokol | ya | |

Minimal satu dari `smtp`/`imap`/`pop3` harus non-null.

### 8.2 `accountResponse`

Dikembalikan semua endpoint akun. **Tidak pernah** berisi kredensial.

| Field | Tipe | Catatan |
|---|---|---|
| `id` | string | UUID |
| `name` | string | |
| `email` | string | |
| `username` | string | |
| `smtp` / `imap` / `pop3` | `Connection` \| null | `null` = protokol tidak dikonfigurasi |
| `created_at` | string | RFC3339 UTC |
| `updated_at` | string | RFC3339 UTC |

`Connection`:

| Field | Tipe | Nilai |
|---|---|---|
| `host` | string | hostname |
| `port` | int | 1–65535 |
| `tls_mode` | string | `tls` \| `starttls` \| `none` |

### 8.3 `Message`

| Field | Tipe | Catatan |
|---|---|---|
| `uid` | string | UID dari server (IMAP UID / POP3 UIDL) |
| `folder` | string | folder asal (POP3 selalu `INBOX`) |
| `subject` | string | |
| `from` | string | alamat pertama dari header From |
| `to` | string | alamat pertama dari header To |
| `date` | string | RFC3339 UTC |
| `is_read` | bool | IMAP flag `\Seen`; POP3 selalu `true` |
| `attachments` | string[] | **nama file saja**; di-omit bila kosong; POP3 selalu kosong |

### 8.4 `sendRequest`

| Field | Tipe | Wajib |
|---|---|:---:|
| `to` | string[] | ya |
| `cc` / `bcc` | string[] | tidak |
| `subject` | string | tidak |
| `body_text` / `body_html` | string | tidak |
| `attachments` | `Attachment[]` | tidak |
| `headers` | object `map[string]string` | tidak |

`Attachment`: `{ "filename": string, "content_type": string, "data_base64": string }` (isi = base64 standar).

### 8.5 `CheckResult`

| Field | Tipe | Catatan |
|---|---|---|
| `unread_count` | int | IMAP saja; POP3 = 0 |
| `new_count` | int | dari 50 pesan terbaru yang belum tercache |

## 9. Walkthrough end-to-end

```bash
export BASE=http://localhost:5569
export XMAIL_API_KEY=changeme

# 1) Buat akun & tangkap id
RESP=$(curl -s -X POST $BASE/accounts \
  -H "X-API-Key: $XMAIL_API_KEY" -H "Content-Type: application/json" \
  -d '{"name":"Gmail","email":"me@example.com","username":"me@example.com","password":"app-pw",
       "smtp":{"host":"smtp.gmail.com","port":587,"tls_mode":"starttls"},
       "imap":{"host":"imap.gmail.com","port":993,"tls_mode":"tls"}}')
ID=$(echo "$RESP" | grep -o '"id":"[^"]*"' | head -1 | cut -d'"' -f4)
echo "account id = $ID"

# 2) Validasi koneksi tiap protokol
curl -s -X POST $BASE/accounts/$ID/test-connection -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"protocol":"smtp"}'
curl -s -X POST $BASE/accounts/$ID/test-connection -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"protocol":"imap"}'

# 3) Kirim email
curl -s -X POST $BASE/accounts/$ID/send -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"to":["bob@example.com"],"subject":"Halo","body_text":"Pesan percobaan."}'

# 4) Cek email baru (mengisi cache 50 terbaru)
curl -s -X POST $BASE/accounts/$ID/check -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"protocol":"imap"}'

# 5) Fetch dari cache (cepat, tanpa dial)
curl -s "$BASE/accounts/$ID/messages?limit=10" -H "X-API-Key: $XMAIL_API_KEY"

# 6) Fetch live untuk memastikan terbaru
curl -s "$BASE/accounts/$ID/messages?limit=10&refresh=true" -H "X-API-Key: $XMAIL_API_KEY"

# 7) Tandai satu pesan sudah dibaca (pakai uid dari langkah 5)
curl -s -X POST $BASE/accounts/$ID/messages/read -H "X-API-Key: $XMAIL_API_KEY" \
  -H "Content-Type: application/json" -d '{"uid":"1042"}'

# 8) Bersihkan
curl -s -X DELETE $BASE/accounts/$ID -H "X-API-Key: $XMAIL_API_KEY"
```

## 10. Troubleshooting / FAQ

| Gejala | Penyebab & solusi |
|---|---|
| `401 unauthorized` | `X-API-Key` salah/hilang. Samakan dengan env `XMAIL_API_KEY` server. |
| `400 invalid_json` padahal JSON kelihatan benar | Ada **field tak dikenal** (decoder strict), atau nama field salah huruf besar/kecil. Hanya field di dokumen ini yang diterima. |
| `400 validation_failed: at least one of smtp, imap, pop3 must be configured` | Kirim minimal satu blok protokol dengan `host`, `port`, `tls_mode`. |
| `400 ... pop3.tls_mode "starttls" is not supported` | POP3 hanya `tls` atau `none`. |
| `500` saat `/messages` atau `/check` | Server mail tidak bisa dihubungi / kredensial salah / akun tidak punya konfigurasi protokol tsb (cek pesan error). Coba `/test-connection` dulu. |
| `500` saat `/send` dengan `internal_error` | Umumnya auth SMTP gagal atau From (`account.email`) ditolak server. Verifikasi app-password & alamat From. |
| Field yang saya kirim hilang setelah `PUT` | `PUT` = full replace. Field yang tak dikirim jadi `null`. |
| Fetch kedua terasa "tidak update" | Itu perilaku cache-first. Pakai `refresh=true` untuk memaksa live. |
| `new_count` kecil/0 tapi ada email baru | `new_count` dihitung dari 50 pesan terbaru vs cache lokal, bukan total mailbox. |
| Attachment terkirim tapi tipenya aneh | `content_type` tidak dipakai; tipe ditentukan dari ekstensi `filename`. |
| `mark read` gagal untuk POP3 | Pop3 tidak mendukung flag per-pesan. Pakai IMAP. |
| Butuh key berbeda per client | Belum didukung (single static key). Multi-key/rotatable masih backlog. |

## 11. Import ke Postman

1. Postman → **Import** → pilih `docs/postman/xmail.postman_collection.json` **dan** `docs/postman/xmail.postman_environment.json`.
2. Pilih environment **xmail (local)**.
3. Set variabel `apiKey` sesuai `XMAIL_API_KEY` server.
4. Jalankan **Health → Healthz** untuk memverifikasi koneksi.
5. Jalankan **Accounts → Create Account** — test script otomatis mengisi `accountId`, sehingga request `{id}` lainnya siap dipakai.

Koleksi memuat request untuk tiap endpoint, contoh body, contoh skenario error (401/400/404), serta request MCP mentah. Lihat [`docs/postman/README.md`](./postman/README.md) untuk daftar lengkapnya.

## 12. Dashboard web

Binary xmail juga menyajikan dashboard web untuk **manajemen akun** (CRUD + uji konektivitas). Dashboard **bukan** API baru — ia klien yang memanggil endpoint di §7 lewat `fetch` dari browser.

| Item | Nilai |
|---|---|
| URL | `http://localhost:5569` (atau alamat `XMAIL_LISTEN_ADDR`) — dari tray Windows tersedia menu **Open dashboard** |
| Aset | `GET /`, `GET /app.js`, `GET /style.css` — **tanpa auth**, di-`go:embed` ke binary |
| Auth dashboard | halaman login menerima `XMAIL_API_KEY`, menyimpannya di `sessionStorage`, lalu mengirimnya sebagai header `X-API-Key` pada setiap panggilan API |
| Cakupan | list/detail/create/update/delete akun + `POST /accounts/{id}/test-connection` per protokol |
| Bukan cakupan | kirim email, fetch/check pesan, mark-read (tetap REST/MCP) |
| Header respons aset | `Content-Security-Policy: default-src 'self'`, `X-Content-Type-Options: nosniff`, `Cache-Control: no-cache` |

Yang penting untuk integrasi:

- Endpoint API (`/accounts`, `/mcp`) **tetap** wajib `X-API-Key`; mengakses dashboard tidak memberi akses API. `GET /` hanya mengembalikan aset statis (tanpa rahasia) — lihat `docs/adr/0002-dashboard-serving-and-auth.md`.
- Karena dashboard disajikan **same-origin**, tidak ada CORS; tidak perlu preflight.
- Password akun tidak pernah dikembalikan API (`§7.5`), jadi form edit selalu menampilkan field password kosong: kosongkan = pertahankan kredensial lama.
- `PUT /accounts/{id}` adalah **full replace**; form dashboard selalu mengirim ulang seluruh field, jadi perilakunya konsisten dengan §7.5.
- Path non-API yang tidak ada (mis. `/nope`) dijawab `404` oleh file server, bukan envelope JSON.

Batasan keamanan: bila `XMAIL_LISTEN_ADDR` tidak di-bind ke loopback, dashboard & API dapat dijangkau dari jaringan — batasi dengan firewall/reverse-proxy dan jaga kerahasiaan `XMAIL_API_KEY`.

