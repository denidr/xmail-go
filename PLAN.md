# Implementation Plan: xmail

Referensi requirement lengkap ada di [PRD.MD](./PRD.MD). Dokumen ini adalah rencana implementasi teknis yang detail — struktur project, dependency, skema DB, kontrak API/MCP, dan urutan kerja per fase.

Untuk referensi arsitektur & cara extend codebase (ditulis untuk manusia maupun LLM agent yang akan mengerjakan repo ini), lihat [ARCHITECTURE.md](./ARCHITECTURE.md) — dokumen ini (PLAN.md) fokus ke rencana & histori keputusan per fase, ARCHITECTURE.md fokus ke "kondisi codebase saat ini & cara menambah fitur".

## 0. Keputusan Teknis (locked-in)

| Item | Pilihan | Catatan |
|---|---|---|
| Bahasa | Go 1.26 | single static binary |
| Module path | `xmail` (local module, tanpa domain) | ganti ke `github.com/<user>/xmail` kapan saja via `go mod edit -module` kalau nanti di-publish |
| SQLite driver | `modernc.org/sqlite` | pure-Go, **tanpa CGO** → build static binary gampang, image Docker `scratch`/`distroless` bisa dipakai |
| SMTP client | `github.com/wneessen/go-mail` | pure Go, dukung TLS implicit/STARTTLS/none, attachment, aktif maintained |
| IMAP client | `github.com/emersion/go-imap/v2` | pure Go, aktif maintained |
| POP3 client | `github.com/knadh/go-pop3` | pure Go, ringan, cukup untuk fetch sederhana |
| MCP SDK | `github.com/mark3labs/mcp-go` | community Go SDK paling matang untuk MCP server saat ini — **verifikasi ulang saat mulai coding**, kalau official Go SDK dari modelcontextprotocol sudah stabil, evaluasi pindah |
| HTTP router | stdlib `net/http` (Go 1.22+ pattern matching di `ServeMux`) | tidak butuh framework eksternal, sejalan dengan goal "ringan" |
| Encryption | AES-256-GCM (stdlib `crypto/aes` + `crypto/cipher`) | key dari env var `XMAIL_ENCRYPTION_KEY` (32 byte, base64) |
| Config | env var only (12-factor), dengan `.env` opsional untuk dev via `godotenv` (dev-only, tidak untuk prod) | |
| Migration | SQL file manual + runner sederhana (bukan library berat) | cukup untuk single-node MVP |
| Windows service manager | `github.com/kardianos/service` | abstraksi cross-platform (Windows Service / systemd / launchd) untuk install/start/stop/uninstall; dipakai khusus di build Windows |
| Windows tray icon | `fyne.io/systray` (fork aktif dari `getlantern/systray`) | pure Go di Windows (tanpa CGO), cocok dengan goal binary statis |

## 0.1 Tiga Target Rilis

Satu codebase, tiga artefak rilis berbeda — logika inti selalu lewat `internal/app.Run` (lihat §1) supaya tidak ada duplikasi behavior antar target:

| Target | Platform | Entry point | Cara jalan | Catatan |
|---|---|---|---|---|
| **Docker x64** | `linux/amd64` | `cmd/xmail` | container, headless | image dari `Dockerfile`, dasar semua homelab/server x86 |
| **Docker Armbian (arm64)** | `linux/arm64` | `cmd/xmail` | container, headless | Dockerfile sama persis, cross-compile Go native (CGO_ENABLED=0) — target SBC seperti Orange Pi/Rock Pi yang jalan Armbian; kalau target board 32-bit lama, ganti platform ke `linux/arm/v7` |
| **Windows x64** | `windows/amd64` | `cmd/xmail-tray` | native `.exe`, icon di system tray, menu bisa install dirinya sendiri jadi **Windows Service** (jalan tanpa user login) | butuh dependency GUI-only (`fyne.io/systray`, `kardianos/service`) — diisolasi via build tag `xmailtray` supaya tidak masuk ke build Docker/headless |

Build tag `xmailtray`: file-file khusus Windows tray (`cmd/xmail-tray/*.go`, `internal/winservice/*.go`) diberi constraint `//go:build windows && xmailtray`. Ini sengaja **custom tag** (bukan cuma `windows`) supaya `go build ./...` tanpa flag tambahan selalu skip file-file ini di semua platform — termasuk kalau dev-nya kebetulan ngoding di mesin Windows. Tag ini baru di-set eksplisit lewat `-tags xmailtray` saat build release Windows (lihat Makefile `release-windows-amd64`).

## 1. Struktur Direktori

```
xmail/
├── cmd/
│   ├── xmail/
│   │   └── main.go                 # entrypoint headless (Docker x64 & Docker Armbian/arm64): load config → app.Run → tangani sinyal shutdown
│   └── xmail-tray/
│       └── main.go                 # entrypoint Windows x64: system tray, delegasi ke app.Run + internal/winservice (build tag: windows,xmailtray)
├── internal/
│   ├── app/
│   │   └── app.go                   # Run(ctx, cfg): wiring bersama (storage→account→mailer→api+mcp), dipakai cmd/xmail DAN cmd/xmail-tray — single source of truth behavior
│   ├── winservice/
│   │   └── service.go               # install/start/stop/uninstall sebagai Windows Service via kardianos/service (build tag: windows,xmailtray)
│   ├── config/
│   │   └── config.go                # struct Config + LoadFromEnv()
│   ├── storage/
│   │   ├── sqlite.go                 # Open(), migration runner
│   │   └── migrations/
│   │       └── 0001_init.sql         # schema awal (accounts, credentials, messages_cache, api_keys)
│   ├── cryptox/
│   │   └── secretbox.go              # Encrypt(plain []byte) / Decrypt(cipher []byte) pakai AES-GCM
│   ├── account/
│   │   ├── model.go                  # struct Account, ConnectionConfig (host/port/tls mode) per protokol
│   │   ├── repository.go             # CRUD ke sqlite (pakai cryptox utk simpan/baca credential)
│   │   └── service.go                # validasi input, orkestrasi test-connection ke 3 protokol
│   ├── mailer/
│   │   ├── types.go                  # interface umum: Sender, Fetcher, Checker + struct Message/Attachment
│   │   ├── smtp/
│   │   │   └── client.go             # implementasi Sender pakai go-mail, handle tls_mode: tls/starttls/none
│   │   ├── imap/
│   │   │   └── client.go             # implementasi Fetcher/Checker pakai go-imap/v2
│   │   └── pop3/
│   │       └── client.go             # implementasi Fetcher (subset) pakai go-pop3
│   ├── api/
│   │   ├── server.go                  # bangun *http.Server + ServeMux, wiring semua handler
│   │   ├── middleware.go              # apiKeyAuth middleware, requestLogger (tanpa log body/credential)
│   │   ├── response.go                # helper JSON envelope {data,error} + error mapping
│   │   ├── accounts_handler.go        # POST/GET/PUT/DELETE /accounts, POST /accounts/{id}/test-connection
│   │   ├── send_handler.go            # POST /accounts/{id}/send
│   │   └── messages_handler.go        # GET /accounts/{id}/messages, POST /accounts/{id}/check
│   └── mcpserver/
│       └── server.go                  # daftarkan 4 tools, delegasikan ke account.Service & mailer.*
├── scripts/
│   ├── release.sh                    # build release 3 target (docker-amd64/arm64, windows-amd64) → dist/ + SHA256SUMS.txt — Git Bash/WSL/Linux/macOS/CI, lihat §8 & ARCHITECTURE.md
│   └── release.ps1                   # sama persis, versi PowerShell native — untuk Windows tanpa Git Bash/WSL
├── assets/
│   └── icon.ico                      # icon tray Windows (placeholder, lihat catatan Fase 7)
├── go.mod
├── go.sum
├── Dockerfile
├── .dockerignore
├── .gitignore
├── Makefile
├── README.MD
├── PRD.MD
├── PLAN.md
└── ARCHITECTURE.md                   # referensi arsitektur & cara extend, untuk manusia + LLM agent
```

> Catatan: pohon direktori di atas adalah sketsa desain awal (masih akurat untuk struktur package), bukan listing lengkap tiap file — beberapa file bertambah seiring implementasi (mis. `internal/account/messages.go`, `internal/api/dto.go`, `*_test.go` di tiap package). Listing file yang benar-benar lengkap & selalu up-to-date ada di [ARCHITECTURE.md](./ARCHITECTURE.md#repository-layout).

**Prinsip desain**: `internal/mailer` mendefinisikan interface (`Sender`, `Fetcher`, `Checker`) yang diimplementasikan masing-masing oleh `smtp`, `imap`, `pop3` — supaya `api` dan `mcpserver` handler tidak perlu tahu detail protokol, cukup panggil lewat `account.Service` yang memilih implementasi sesuai config akun.

**Prinsip desain (multi-target release)**: `internal/app.Run(ctx, cfg)` adalah satu-satunya tempat yang menyalakan storage + account service + mailer + API server + MCP server. `cmd/xmail` (Docker) dan `cmd/xmail-tray` (Windows) sama-sama cuma memanggil `app.Run` — bedanya cuma cara start/stop-nya (sinyal OS vs tray menu/Windows SCM). Ini mencegah dua entrypoint punya behavior yang beda-beda seiring waktu.

## 2. Skema Database (SQLite)

File: `internal/storage/migrations/0001_init.sql`

```sql
CREATE TABLE accounts (
    id              TEXT PRIMARY KEY,          -- uuid
    name            TEXT NOT NULL,
    email           TEXT NOT NULL,
    smtp_host       TEXT,
    smtp_port       INTEGER,
    smtp_tls_mode   TEXT,                       -- 'tls' | 'starttls' | 'none'
    imap_host       TEXT,
    imap_port       INTEGER,
    imap_tls_mode   TEXT,
    pop3_host       TEXT,
    pop3_port       INTEGER,
    pop3_tls_mode   TEXT,
    username        TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE credentials (
    account_id       TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    encrypted_secret BLOB NOT NULL,              -- AES-GCM ciphertext dari password/app-password
    nonce            BLOB NOT NULL
);

CREATE TABLE messages_cache (
    id          TEXT PRIMARY KEY,                -- uuid internal
    account_id  TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    protocol    TEXT NOT NULL,                    -- 'imap' | 'pop3'
    folder      TEXT,
    uid         TEXT NOT NULL,                    -- UID dari server (unik per folder)
    subject     TEXT,
    from_addr   TEXT,
    to_addr     TEXT,
    date        TEXT,
    is_read     INTEGER DEFAULT 0,
    fetched_at  TEXT NOT NULL,
    attachments TEXT,                             -- JSON array nama file lampiran, mis. '["a.pdf"]'; NULL = tidak ada/tidak didukung (POP3). Ditambah via migrations/0002_message_attachments.sql (fix code review: requirement PRD §6.3 tadinya belum ada kolomnya)
    UNIQUE(account_id, protocol, folder, uid)
);

-- api_keys: skema disiapkan untuk multi-key/rotatable API key, TAPI
-- BELUM DIPAKAI di MVP — auth saat ini cuma 1 static key dari env
-- XMAIL_API_KEY (constant-time compare, tidak pernah disimpan di DB).
-- Klarifikasi ini menyelesaikan kontradiksi PRD.MD §8 yang tadinya
-- bilang "API key disimpan sebagai hash" seolah sudah jadi behavior —
-- itu deskripsi rencana fitur ini, bukan MVP. Lihat Fase 9 (backlog)
-- untuk implementasi konkretnya nanti.
CREATE TABLE api_keys (
    id          TEXT PRIMARY KEY,
    key_hash    TEXT NOT NULL,                    -- sha256 hex dari api key
    label       TEXT,
    created_at  TEXT NOT NULL,
    last_used_at TEXT
);

CREATE INDEX idx_messages_account ON messages_cache(account_id, folder);
```

## 3. Kontrak REST API

Semua response: `{"data": ..., "error": null}` atau `{"data": null, "error": {"code": "...", "message": "..."}}`. Header wajib: `X-API-Key`.

| Method | Path | Body/Query | Deskripsi |
|---|---|---|---|
| GET | `/healthz` | - | Health check, **tanpa auth** — ditambah saat implementasi Fase 1 untuk Docker healthcheck (lihat catatan implementasi di §5 Fase 1) |
| POST | `/accounts` | `{name, email, smtp:{host,port,tls_mode}, imap:{...}, pop3:{...}, username, password}` | Buat akun baru, password langsung dienkripsi sebelum simpan |
| GET | `/accounts` | - | List akun (tanpa field credential) |
| GET | `/accounts/{id}` | - | Detail 1 akun |
| PUT | `/accounts/{id}` | **full replace**, bukan partial | Update config akun — semua field wajib dikirim ulang (field yang tidak dikirim di-NULL-kan), `password` satu-satunya field opsional (nil = kredensial lama dipertahankan). Lihat catatan implementasi Fase 1. |
| DELETE | `/accounts/{id}` | - | Hapus akun (cascade credentials & messages_cache). Return `200 {"data":{"deleted":true},"error":null}` — **bukan `204`** (fix code review: 204 sebelumnya melanggar kontrak envelope) |
| POST | `/accounts/{id}/test-connection` | `{protocol: "smtp"\|"imap"\|"pop3"}` | Coba konek+auth, return ok/error tanpa side-effect |
| POST | `/accounts/{id}/send` | `{to[], cc[], bcc[], subject, body_text, body_html, attachments[], headers?}` | Kirim via SMTP akun tsb. `headers` opsional: `{"X-Priority":"1", ...}` custom header (fix code review: sebelumnya tidak ada) |
| GET | `/accounts/{id}/messages` | `?folder=INBOX&limit=20&offset=0&protocol=imap&refresh=false` | Fetch daftar email. **Cache-first**: kalau `messages_cache` sudah punya data untuk (account,protocol,folder), langsung dari situ (tidak dial server) — kecuali `refresh=true` yang paksa live-fetch (fix code review: sebelumnya SELALU live-fetch, cache jadi write-only/dead code) |
| POST | `/accounts/{id}/check` | `{protocol: "imap"\|"pop3", folder?}` | Trigger cek email baru, update cache, return `{unread_count, new_count}` |
| POST | `/accounts/{id}/messages/read` | `{protocol?, folder?, uid}` | **Baru** (fix code review: requirement PRD §6.3 "mark as read" tadinya hilang). Tandai 1 pesan sebagai sudah dibaca — IMAP only (`mailer.Marker`), POP3 return error validasi karena tidak ada konsep flag per-pesan. Return `{"marked_read":true}` |

## 4. Kontrak MCP Tools

Didaftarkan di `internal/mcpserver/server.go`, delegasi ke service yang sama dengan REST API (satu source of truth, tidak duplikasi logika).

| Tool | Input | Output |
|---|---|---|
| `list_accounts` | - | `[{id, name, email}]` (tanpa credential) |
| `send_email` | `{account_id, to[], cc[], bcc[], subject, body_text?, body_html?, headers?}` | `{status}` (realisasi akhir — draf awal ada `message_id?` yang tidak pernah diimplementasikan, tidak ada info message-id yang bisa dikembalikan dari SMTP; `cc[]`/`bcc[]` dan `headers` ditambah saat fix code review. Attachments tetap REST-only — base64 binary di argumen tool call MCP pengalaman yang buruk, lihat §10.6 #22) |
| `fetch_emails` | `{account_id, protocol?, folder?, limit?, refresh?}` | `[{uid, folder, subject, from, to, date, is_read, attachments?}]` (realisasi akhir — draf awal cuma `{id, subject, from, date, is_read}`, lihat `mailer.Message` di ARCHITECTURE.md §2; `protocol`, `refresh`, dan `attachments` ditambah saat fix code review) |
| `check_new_emails` | `{account_id, protocol?, folder?}` | `{unread_count, new_count}` |

## 5. Urutan Kerja (Fase)

### Fase 0 — Bootstrap (scaffolding, hari ini)
- [x] `go.mod` + struktur folder kosong dengan file stub
- [x] `Dockerfile`, `.dockerignore`, `.gitignore`, `Makefile`
- [x] `README.MD`

### Fase 1 — Core: Config, Storage, Crypto, Account CRUD ✅ SELESAI
- [x] `internal/config`: load semua env var (`XMAIL_LISTEN_ADDR`, `XMAIL_DB_PATH`, `XMAIL_ENCRYPTION_KEY`, `XMAIL_API_KEY`)
- [x] `internal/storage`: buka koneksi sqlite (`modernc.org/sqlite`), jalankan migration `0001_init.sql` on startup (idempotent, cek `schema_migrations` table)
- [x] `internal/cryptox`: `Encrypt`/`Decrypt` AES-GCM, unit test roundtrip
- [x] `internal/account`: model + repository (CRUD) + service (validasi field wajib per protokol yang diisi)
- [x] `internal/api`: server bootstrap, middleware API key, handler `/accounts` CRUD
- [x] Unit test (§6.1): `cryptox` roundtrip, `config.Load` validasi env, `account` repository (sqlite temp) + service (mailer di-mock), `api` handler `/accounts` (`httptest`) + `apiKeyAuth` middleware — **40 test, semua hijau**
- [x] Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...` hijau; smoke test manual (`curl` create/list/auth-reject) lolos; cek langsung isi file `.db` (grep binary untuk plaintext password) — **kredensial terkonfirmasi tidak tersimpan plaintext**.

> **Catatan implementasi (penyesuaian dari rencana awal, lihat juga §3/§7):**
> - **`GET /healthz`** ditambahkan (di luar tabel §3 awal) — dikecualikan dari `apiKeyAuth`, dipakai untuk Docker healthcheck di Fase 6. Ditambahkan karena murah dan langsung dibutuhkan begitu container jalan.
> - **API key auth pakai `crypto/subtle.ConstantTimeCompare`**, bukan `==` biasa — mencegah timing attack menebak API key. Tabel `api_keys` di skema (§2) belum dipakai (auth masih 1 key statis dari `XMAIL_API_KEY` env sesuai PLAN); multi-key/rotate lewat tabel itu masuk backlog Fase 9 kalau dibutuhkan.
> - **`account.Service` pakai pola `TesterFactory`** (`SetSMTPTester`/`SetIMAPTester`/`SetPOP3Tester`) untuk `TestConnection` — persis prinsip desain interface di §1, konkretnya baru di-wire pas `internal/mailer/*` ada (Fase 2-4). Sebelum di-wire, `test-connection` mengembalikan error jelas ("tester belum di-wire"), bukan silent no-op.
> - **`PUT /accounts/{id}` adalah full replace, bukan partial/PATCH** — klien wajib kirim semua field (name/email/username/smtp/imap/pop3), field yang tidak dikirim akan ke-null-kan. Hanya `password` yang opsional (nil = kredensial lama dipertahankan). Ini beda halus dari kata "partial update" di draf awal §3 — didokumentasikan di sini karena akan mengejutkan konsumen API kalau tidak disadari.
> - Ditambah `internal/api/dto.go` — layer request/response JSON terpisah dari domain model `account.Account`, supaya field seperti password tidak pernah kebawa balik di response secara tidak sengaja (sudah diuji eksplisit di `server_test.go`).
> - `storage.Open` set `db.SetMaxOpenConns(1)` — SQLite cuma boleh 1 writer, ini mencegah error "database is locked" di bawah beban concurrent.

### Fase 2 — SMTP Send ✅ SELESAI
- [x] `internal/mailer/smtp`: implementasi `Sender` pakai `go-mail`, mapping `tls_mode` → opsi library (`WithSSL()` untuk `tls`, `WithTLSPolicy(TLSMandatory)` untuk `starttls`, `WithTLSPolicy(NoTLS)` untuk `none`)
- [x] `internal/api/send_handler.go` + endpoint `POST /accounts/{id}/send` (attachment dikirim sebagai base64 di JSON, lihat catatan di bawah)
- [x] `test-connection` untuk SMTP (dial + auth tanpa kirim) — via `account.Service.TestConnection`, di-wire di `internal/app.wireMailer`
- [x] Unit test: mapping `TLSMode`→opsi `go-mail` (tanpa dial), validasi `fromAddress` wajib diisi sebelum `Send`
- [x] Integration test (§6.2, tag `integration`): server in-process `go-smtp` (plaintext) — kirim email sungguhan lewat `Client.Send`, assert backend menerima From/To/Subject/Body yang sama persis; `TestConnection` juga diuji
- [x] Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...` (48 test) dan `go test -tags integration ./...` semua hijau.

> **Catatan implementasi (penyesuaian dari rencana awal):**
> - **Integration test TLS/STARTTLS di-skip untuk sekarang**, cuma mode `none` (plaintext) yang diuji otomatis. Alasan: butuh self-signed cert + `InsecureSkipVerify` khusus test yang belum di-plumbing ke `smtp.Client` (client production sengaja tidak expose opsi skip-verify, sesuai PRD.MD §8 "TLS cert validation aktif default"). **Backlog**: tambahkan test fixture cert generator kalau mau coverage TLS/STARTTLS otomatis; untuk sekarang mode TLS/STARTTLS harus dites manual ke akun email asli (Mailtrap/Gmail app-password) sebelum rilis (masuk checklist §6.5).
> - **`go test -race` tidak bisa dijalankan di mesin dev ini** — butuh cgo/gcc yang tidak terpasang di environment Windows ini. Jalankan `-race` di CI (Linux runner ada gcc) atau di dalam Docker build stage sebelum Fase 6/8 rilis; jangan anggap "aman dari race" hanya karena `go test ./...` biasa hijau.
> - **`account.Service` dapat method baru `Send`** + tipe `SMTPSenderFactory` (paralel dengan `TesterFactory`) — `Service` sekarang import `internal/mailer` (bukan cuma dipakai lewat interface `ConnTester`), tapi tidak menimbulkan import cycle karena `internal/mailer` sendiri tidak import apa pun dari `internal/account`.
> - **Attachment di `POST /accounts/{id}/send` dikirim sebagai `data_base64`** (bukan multipart/form-data) — konsisten dengan envelope JSON `{data,error}` yang dipakai di semua endpoint lain; tidak disebutkan eksplisit di draf skema awal §3, jadi dicatat di sini.
> - `internal/app.wireMailer` jadi tempat sentral menghubungkan `account.Service` ke implementasi konkret tiap protokol — akan bertambah isi tiap Fase 3 (IMAP) dan Fase 4 (POP3), tanpa perlu ubah `Run()`.

### Fase 3 — IMAP Fetch/Check ✅ SELESAI
- [x] `internal/mailer/imap`: implementasi `Fetcher`/`Checker` pakai `go-imap/v2` — `Fetch` (ENVELOPE+FLAGS+UID, newest-first, limit/offset via sequence number range), `Check` (STATUS command, `NumUnseen`)
- [x] `internal/api/messages_handler.go` untuk `GET /accounts/{id}/messages` (`?folder=&limit=&offset=&protocol=`) dan `POST /accounts/{id}/check`
- [x] Sinkronisasi ke `messages_cache` (upsert by `(account_id, protocol, folder, uid)`) — via `account.Repository.UpsertMessages`/`ListMessages`/`ExistingUIDs`
- [x] Integration test (§6.2, tag `integration`): backend `imapserver.Session` custom (3 fixed pesan) — `TestConnection` (sukses & password salah), `Fetch` (urutan newest-first, flag `\Seen`→`IsRead`, **+ regression test `limit=0`**), `Check` (unread count) — **7 test IMAP, semua hijau**
- [x] Unit test: `account` repository (`UpsertMessages` idempoten/no-duplicate, `ExistingUIDs`), `account.Service.FetchMessages`/`CheckNew` dengan `mailer.FetcherChecker` di-mock (termasuk skenario "new since last check" 2x-panggil)
- [x] Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...` (53 test) dan `go test -tags integration ./...` (59 test) semua hijau.

> **Catatan implementasi (penyesuaian dari rencana awal):**
> - **`mailer.Checker.Check` disederhanakan** dari `(unread, new int, err error)` menjadi `(unread int, err error)` — alasan: IMAP `STATUS` command tidak punya konsep "new since last check" yang protocol-native (field `NumRecent` sudah obsolete dan tidak didukung `imapclient`). "New since last check" sekarang murni konsep aplikasi: `account.Service.CheckNew` menghitungnya sendiri dengan diff UID hasil `Fetch` terbaru terhadap `messages_cache` (lihat `ExistingUIDs`), baru meng-upsert. Ini lebih jujur secara semantik dan tidak bergantung asumsi server yang tidak reliable.
> - **`mailer.FetcherChecker`** (gabungan `Fetcher`+`Checker`) ditambahkan ke `internal/mailer/types.go` — dipakai sebagai tipe balik `account.IMAPFactory`, karena IMAP satu-satunya protokol yang mendukung keduanya (POP3 di Fase 4 cuma `Fetcher`).
> - **`account.Service` dapat 3 hal baru**: `IMAPFactory`/`POP3Factory` (tipe factory, paralel `TesterFactory`/`SMTPSenderFactory`), method `FetchMessages` dan `CheckNew`. `resolveFetcher` jadi titik dispatch tunggal protokol→factory, dipakai keduanya — sesuai prinsip desain "satu tempat tahu cara pilih implementasi per protokol" di §1.
> - **Default `protocol` untuk `GET /messages` dan `POST /check` adalah `imap`** (belum disebutkan eksplisit di draf skema awal §3) — masuk akal karena IMAP adalah protokol fetch utama (POP3 cuma fallback, lihat PRD.MD §3 goals).
> - **🐛 Bug ditemukan & diperbaiki saat crosscheck pasca-Fase 8**: `Fetch(folder, limit=0, offset=0)` di `internal/mailer/imap/client.go` mengembalikan **1 pesan, bukan 0**. Akar masalah: `imapv2.SeqSet.AddRange(start, stop)` diam-diam menukar `start`/`stop` kalau `stop < start` alih-alih memperlakukannya sebagai range kosong — jadi saat `limit=0` bikin `start > end`, hasilnya malah nyangkut jadi range valid 2-elemen. Fix: guard eksplisit `limit <= 0` sebelum membangun `SeqSet`, plus regression test `TestIntegration_Fetch_ZeroLimit` yang **dikonfirmasi gagal tanpa fix, lolos dengan fix** (diverifikasi manual dengan revert-sementara). POP3 tidak kena bug ini (logic loop-nya sudah aman terhadap `start>end` secara alami).
> - **🐛 Inkonsistensi ditemukan & diperbaiki saat menulis ARCHITECTURE.md/skill**: `mailer.Message` (§3 di file ini) tidak punya JSON tag, jadi `GET /accounts/{id}/messages` dan tool MCP `fetch_emails` mengembalikan field `PascalCase` (`UID`, `IsRead`, dst) — beda gaya dari seluruh endpoint lain yang konsisten `snake_case` (lihat `internal/api/dto.go`). Fix: tambah `json:"uid"`/`json:"is_read"`/dst ke struct di `internal/mailer/types.go`. Karena `Message` di-serialize langsung tanpa DTO terpisah di kedua tempat (REST & MCP), satu fix ini otomatis konsisten di keduanya — bukti lain §1 rule 3 (REST & MCP satu sumber kebenaran) di ARCHITECTURE.md. Tidak ada test yang patah (tidak ada test yang men-assert nama field JSON lama).
> - **🐛🔴 BUG KRITIS ditemukan & diperbaiki saat menulis skill agent**: `POST /accounts/{id}/test-connection` (dan `POST /accounts/{id}/send`!) untuk protokol **SMTP tidak pernah benar-benar melakukan SMTP AUTH**, walau `username`/`password` sudah di-set di config akun. Akar masalah: `go-mail` men-default `SMTPAuthType` ke `SMTPAuthNoAuth` — memanggil `WithUsername`/`WithPassword` saja **tidak** otomatis mengaktifkan pengiriman perintah `AUTH`, harus eksplisit `WithSMTPAuth(...)`, dan `internal/mailer/smtp/client.go` tidak pernah memanggilnya. Efeknya: `test-connection` melaporkan `{"ok":true}` walau password salah (dibuktikan langsung ke server Gmail asli sebelum fix), dan `Send` kemungkinan besar akan gagal belakangan di server (unauthenticated relay ditolak) tanpa `test-connection` pernah memperingatkan. **Ditemukan** saat menulis contoh `curl` untuk skill agent dan iseng test ke `smtp.gmail.com` beneran dengan password ngasal — hasilnya `ok:true` yang mencurigakan (padahal test IMAP dengan password salah ke `imap.gmail.com` asli sudah benar menolak). Fix: tambah `gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover)` di `buildMailClient()`. **Regression test ditambah** (`TestIntegration_Send_ActuallyAuthenticates` di `internal/mailer/smtp/integration_test.go`, server SMTP in-process dengan CRAM-MD5 auth hand-rolled karena `go-sasl` cuma nyediakan sisi client-nya) — **dikonfirmasi gagal tanpa fix, lolos dengan fix** (revert-sementara lagi, sama seperti bug IMAP `limit=0` sebelumnya). Setelah fix, re-test manual ke Gmail asli dengan password salah → benar-benar ditolak (`535 ... BadCredentials`) via IMAP dan SMTP.
>   **Pelajaran**: bug ini lolos dari review kode manual, unit test (TLS-mode mapping doang), *dan* integration test awal (yang cuma pakai plaintext tanpa credential) — baru ketahuan saat benar-benar mencoba dari sudut pandang "pengguna/agent" terhadap server nyata. Ini alasan kenapa §7 ARCHITECTURE.md ("Testing") sekarang eksplisit menyebut verifikasi manual `curl` sebagai langkah wajib, bukan cuma nice-to-have.
>   **Total test setelah fix ini**: `go test ./...` = 60, `go test -tags integration ./...` = 76 (naik dari 72 — 3 subtest baru + 1 test induk).

### Fase 4 — POP3 Fetch ✅ SELESAI
- [x] `internal/mailer/pop3`: implementasi `Fetcher` (subset, tanpa folder) pakai `go-pop3` — `Uidl(0)`+`Top(id,0)` (header-only, tidak download body), newest-first dengan limit/offset persis pola IMAP
- [x] `messages_handler.go`/`accounts_handler.go` sudah generik lewat query param `protocol` — **tidak perlu perubahan kode**, otomatis kepakai begitu `account.Service.SetPOP3Factory`/`SetPOP3Tester` di-wire di `internal/app`
- [x] Integration test (§6.2, tag `integration`): fake POP3 server minimal (`integration_test.go`, bukan `testserver_test.go` — lihat catatan) — `USER`/`PASS`/`NOOP`/`UIDL`/`TOP`/`QUIT`, assert `Fetcher.Fetch` (urutan, limit+offset, UID/From/Subject) dan `TestConnection` (sukses & password salah) — **5 test POP3, semua hijau**
- [x] Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...` (53 test) dan `go test -tags integration ./...` (64 test) semua hijau.

> **Catatan implementasi (penyesuaian dari rencana awal):**
> - **`tls_mode: "starttls"` untuk POP3 sengaja mengembalikan error eksplisit**, bukan diam-diam di-treat sebagai `tls` (beda dari opsi awal yang disebut di TODO stub Fase 0). Alasan: `go-pop3` tidak punya implementasi STARTTLS sama sekali (`Opt` cuma punya `TLSEnabled` on/off) — menganggapnya sebagai implicit TLS bisa gagal connect (port salah) atau, lebih parah, salah asumsi soal keamanan koneksi. Pesan error-nya eksplisit menyuruh pakai `tls` atau `none`. **Test `TestStartTLS_NotSupported` mengunci perilaku ini.**
> - **POP3 `IsRead` selalu `true`** — protokol POP3 tidak punya konsep flag \Seen seperti IMAP, jadi tidak ada cara mengetahui status baca/belum dari server. Didokumentasikan di komentar `Fetch`, bukan bug.
> - **File test dinamai `integration_test.go`**, bukan `testserver_test.go` seperti disebut di draf awal — konsisten dengan penamaan yang sudah dipakai di `internal/mailer/smtp` dan `internal/mailer/imap` (satu file per package berisi fake server + test case-nya, bukan dipisah).
> - Tidak ada perubahan pada `messages_handler.go`/`accounts_handler.go`/`internal/api` sama sekali di fase ini — bukti desain dispatch protokol generik dari Fase 3 (`account.Service.resolveFetcher`) sudah benar sejak awal.

### Fase 5 — MCP Server ✅ SELESAI
- [x] `internal/mcpserver`: setup `mcp-go` server — **HTTP (Streamable HTTP transport) sebagai default**, di-mount di `/mcp` pada HTTP server yang sama dengan REST API (bukan port terpisah); **stdio opsional** via `XMAIL_MCP_STDIO=true` (lihat catatan). 4 tools terdaftar (`list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`), semuanya memanggil `account.Service` yang sama dengan REST handler — tidak ada logic terduplikasi.
- [x] MCP HTTP handler dan (kalau diaktifkan) stdio server jalan di `internal/app.Run`, sejalan dengan HTTP API server
- [x] Unit test: tiap tool handler (input valid/invalid, `account.Service` pakai mailer mock yang sama polanya dengan Fase 3) — **7 test, semua hijau**
- [x] Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...` (60 test), `go test -tags integration ./...` (71 test) hijau; **smoke test end-to-end via `curl` ke binary asli** — `initialize` → `tools/list` (4 tools dengan schema benar) → `tools/call list_accounts` — semua sukses lewat HTTP JSON-RPC beneran, bukan cuma unit test.

> **Catatan implementasi (penyesuaian dari rencana awal):**
> - **Transport default HTTP, bukan stdio** — beda dari asumsi awal "stdio &/atau HTTP, putuskan saat implementasi". Alasan: xmail didesain jalan sebagai service headless di Docker (§0.1), di mana stdio MCP tidak berguna (tidak ada proses interaktif attached). MCP di-mount di `/mcp` pada `http.Server` yang sama dengan REST API — satu port, satu proses, **dilindungi `X-API-Key` yang sama** (karena dipasang lewat `api.NewServer`'s mux, bukan handler terpisah di luar middleware). Stdio tetap tersedia via env `XMAIL_MCP_STDIO=true` untuk skenario MCP client yang nge-spawn `xmail` sebagai subprocess langsung (mis. dari Windows tray, Fase 7).
> - **`api.NewServer` tanda tangannya berubah**: `NewServer(apiKey string, service *account.Service, mcpHandler http.Handler)` — parameter ketiga baru, `nil` kalau MCP tidak mau di-mount (dipakai di semua test `internal/api` yang tidak butuh MCP).
> - **Contract test formal §6.3 (REST vs MCP hasil identik) belum ditulis sebagai test otomatis** — yang ada baru smoke test manual (`curl`) yang membuktikan jalur MCP bekerja end-to-end, dan unit test yang membuktikan MCP handler & REST handler sama-sama panggil `account.Service`. Test otomatis yang benar-benar assert "response REST == response MCP untuk akun yang sama" masuk **backlog Fase 9** kalau dibutuhkan bukti lebih kuat lagi (saat ini risikonya rendah karena keduanya literally memanggil method service yang sama, jadi divergence nyaris mustahil kecuali ada mapping field yang salah).
> - Skema tool (`inputSchema`) dibangun manual pakai `mcp.WithString`/`WithArray`/`WithNumber` + `mcp.Required()`, bukan `mcp.WithInputSchema[T]()` (auto-generate dari struct tag) — supaya kontrak persis sama dengan draf skema di §4 tanpa bergantung pada perilaku default library `jsonschema-go` yang belum ditest.

### Fase 6 — Docker & Hardening ✅ SELESAI
- [x] `Dockerfile` multi-stage: build stage (`golang:1.26-alpine`) → runtime stage (`gcr.io/distroless/static-debian12:nonroot`, karena CGO-free)
- [x] `CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w"` — diverifikasi cross-compile lokal untuk `linux/amd64`/`linux/arm64` (tanpa cgo), **dan lewat `docker build` beneran** (lihat di bawah)
- [x] Volume mount untuk `xmail.db` (persist antar restart) — **diverifikasi nyata**: `docker run` dengan named volume, isi akun, `docker restart`, akun masih ada
- [x] Review: tidak ada log yang bocorkan credential/body email (cek `grep -rn "log\."`), TLS cert validation default ON (`grep -rn "InsecureSkipVerify\|TLSSkipVerify"` → nol hasil di kode produksi), `tls_mode` wajib eksplisit per protokol
- [~] `go test -race ./...` — **masih tidak bisa dijalankan** di mesin dev ini (butuh cgo/gcc, tidak terpasang). Jalankan di CI/Linux sebelum rilis produksi pertama.
- [x] `docker build` → cek ukuran image, `docker run` → smoke test endpoint dasar — **dieksekusi & diverifikasi penuh** (lihat catatan; awalnya **gagal**, ada bug nyata di `Dockerfile` yang ketahuan & diperbaiki di sesi ini).

> **Catatan implementasi — Docker akhirnya berhasil dijalankan (setelah sempat gagal 2x karena masalah berbeda):**
> - **Docker Desktop awalnya tidak mau start otomatis** — percobaan pertama pakai path salah (`C:\Program Files\Docker\...` yang tidak ada di mesin ini; instalasi sebenarnya ada di `%LOCALAPPDATA%\Programs\DockerDesktop\Docker Desktop.exe`), jadi proses-nya tidak pernah benar-benar jalan meski tidak error eksplisit. Setelah path yang benar ditemukan, Docker Desktop start normal dan daemon siap dalam ~10 detik.
> - **🐛 Bug ditemukan & diperbaiki: `Dockerfile` sebelumnya bikin container langsung crash** — `docker run` pertama exit dengan `storage: migrate: create schema_migrations: unable to open database file (14)` (SQLite `SQLITE_CANTOPEN`). Akar masalah: `VOLUME ["/app/data"]` doang tidak membuat direktori itu dengan permission yang benar untuk base image `distroless/static-debian12:nonroot` (jalan sebagai UID/GID 65532, bukan root) — mountpoint volume default dimiliki root, jadi proses nonroot tidak bisa nulis/create file DB di situ. Fix: tambah `RUN mkdir -p /out/data` di build stage (yang masih punya shell Alpine) lalu `COPY --from=build --chown=nonroot:nonroot /out/data /app/data` di stage distroless, supaya direktori `/app/data` sudah ada dengan ownership benar sebelum `VOLUME` di-declare. **Ini persis jenis bug yang cuma ketahuan kalau image-nya benar-benar dijalankan** — `docker build` sendiri sukses tanpa keluhan; masalahnya baru muncul saat runtime.
> - **Setelah fix, verifikasi penuh berhasil**: `docker build` → image `xmail:dev-amd64` (25.5MB disk usage, 6.48MB content, tarball gzip 6.2MB) → `docker run` (container tetap `Up`, tidak crash) → `curl /healthz` → `curl POST /accounts` (create akun beneran) → `curl GET /accounts` (list, data ada) → `docker restart` dengan named volume → data akun **masih ada setelah restart** (persistence terbukti, bukan cuma didesain).
> - Detail lengkap build Docker (amd64 + arm64) & smoke test ada di catatan Fase 8 di bawah — status "selesai" untuk Fase 6 di sini spesifik ke Dockerfile/hardening-nya sendiri, bukan ke eksekusi rilis per-arsitektur (itu domain Fase 8).

### Fase 7 — Windows Tray & Service ✅ SELESAI (kecuali instalasi service beneran, lihat catatan)
- [x] `go get github.com/kardianos/service fyne.io/systray` — sebenarnya sudah ter-fetch otomatis sejak `go mod tidy` di Fase 3 (lihat catatan)
- [x] `internal/winservice`: implementasi `program.Start`/`program.Stop` — `Start` menyalakan `app.Run(ctx, cfg)` di goroutine, `Stop` cancel context + tunggu shutdown dengan timeout 10 detik (`stopTimeout`), return error kalau `app.Run` tidak selesai tepat waktu (SCM kill process kalau `Stop` kelamaan)
- [x] `cmd/xmail-tray`: `assets/icon.ico` (16x16, digenerate programatik — placeholder visual, bukan branding final) + `go:embed` (file-nya di-copy ke `cmd/xmail-tray/icon.ico` karena `go:embed` cuma bisa embed file di dalam/di bawah folder package). Menu tray lengkap: Status (label), Start/Stop (jalankan `app.Run` foreground di proses tray), Install/Uninstall as Windows Service, Open dashboard (`rundll32 url.dll,FileProtocolHandler`), Quit. Deteksi mode via `winservice.Interactive()` — kalau proses di-launch oleh Windows SCM (bukan interaktif), tray UI di-skip total dan langsung `svc.Run()`.
- [x] Build & jalan **langsung di mesin dev ini** (Windows native, bukan cross-compile): `go build -tags xmailtray ./...` sukses, `go vet -tags xmailtray ./...` bersih, binary tray (`19.6MB` build lokal / `13.5MB` dengan `-ldflags="-s -w -H=windowsgui"` persis command `make release-windows-amd64`) **dijalankan sungguhan** — proses tetap hidup >3 detik tanpa crash (icon + menu ter-render, tidak ada error di `onReady`).
- Verifikasi: build default (`go build ./...`, tanpa tag) tetap bersih setelah semua perubahan ini — file Windows-tray tetap ter-exclude seperti didesain di Fase 0.

> **Catatan implementasi (penyesuaian dari rencana awal, & apa yang SENGAJA belum diverifikasi):**
> - **Dependency `kardianos/service`+`fyne.io/systray` ternyata sudah masuk `go.sum` sejak `go mod tidy` di Fase 3** — Go tooling menariknya karena file-file berlabel `//go:build windows && xmailtray` tetap dipertimbangkan saat `go mod tidy` menghitung graph dependency lengkap ("all" pattern lintas platform), meskipun tetap **tidak ikut ter-compile** di build default (dibuktikan dengan `go list ./...` yang konsisten mengecualikan `cmd/xmail-tray`/`internal/winservice` sepanjang Fase 1-6). Jadi tidak ada `go get` eksplisit terpisah yang perlu dijalankan lagi di fase ini.
> - **Icon `assets/icon.ico` digenerate programatik** (16x16 solid-color + garis amplop sederhana lewat script Go sekali-jalan, disimpan ke `assets/icon.ico` lalu di-copy ke `cmd/xmail-tray/icon.ico` untuk `go:embed`) — bukan aset desain final. Ganti file ini kapan saja sebelum rilis publik kalau branding sudah ada; format/ukuran (ICO 32bpp+AND-mask) sudah teruji jalan di `systray.SetIcon`.
> - **Tidak ada dialog/MessageBox untuk feedback Install/Uninstall/error** — hasil aksi cuma di-log (`log.Println`/`log.Printf`), bukan popup. `fyne.io/systray` tidak menyediakan dialog primitive; implementasi MessageBox asli butuh syscall `user32.dll` tambahan (`golang.org/x/sys/windows`) — **masuk backlog Fase 9** kalau UX ini penting untuk end-user non-teknis.
> - **⚠️ `Install as Windows Service`/`Uninstall` SENGAJA TIDAK dieksekusi sungguhan di sesi ini** — install service Windows adalah perubahan level-sistem (butuh Administrator, mendaftar entry baru di Service Control Manager) yang jauh lebih sulit dibalik daripada sekadar menjalankan/mematikan proses biasa. Ini di luar scope "jalankan & verifikasi" yang aman dilakukan otomatis tanpa persetujuan eksplisit pengguna — beda dengan menjalankan `.exe` secara interaktif lalu langsung dimatikan (yang sudah dilakukan & terbukti aman/reversibel dalam hitungan detik). **Kode `program.Start`/`program.Stop`/`svc.Install()`/`svc.Uninstall()` sudah lengkap dan mengikuti API `kardianos/service` standar** (contoh resminya identik polanya), tapi verifikasi "muncul di `services.msc` dan bisa di-start/stop dari sana" masih **perlu dilakukan manual oleh user** (jalankan `.exe`, klik "Install as Windows Service" dari tray, cek `services.msc`/`sc query XmailService`).

### Fase 8 — Release Packaging (3 Target) ✅ SELESAI — SEMUA 3 TARGET DIBUILD & DIVERIFIKASI NYATA
- [x] **`scripts/release.sh`** — script build release nyata (bukan cuma inline command di Makefile), dispatcher untuk ketiga target (`docker-amd64`/`docker-arm64`/`windows-amd64`/`all`), auto-detect versi dari `git describe --tags --always --dirty` (fallback `dev`), output ke `dist/` (bukan `bin/`), dan tulis `dist/SHA256SUMS.txt` di akhir. `Makefile` `release-*` sekarang cuma wrapper tipis yang panggil script ini — logic aslinya cuma di satu tempat, dan script-nya jalan standalone tanpa `make` (penting untuk CI nanti). Detail lengkap ada di [ARCHITECTURE.md](./ARCHITECTURE.md#build--release).
- [x] `version` var ditambahkan ke `cmd/xmail` & `cmd/xmail-tray` (`var version = "dev"`), di-inject via `-ldflags -X main.version=$VERSION` oleh script, di-log saat startup — **diverifikasi**: binary hasil build script menampilkan versi yang benar saat dijalankan.
- [x] **`scripts/release.sh docker-amd64`** — **berhasil, diverifikasi penuh sampai runtime**. Docker Desktop akhirnya berhasil dinyalakan (path launcher sebelumnya salah, lihat catatan Fase 6) → `docker build` sukses → **ketahuan bug crash saat container beneran dijalankan** (`/app/data` tidak writable, lihat catatan Fase 6, sudah diperbaiki) → setelah fix: `docker run` tetap `Up`, `curl /healthz` + `POST /accounts` + `GET /accounts` semua sukses, `docker restart` dengan named volume → **data akun masih ada** (persistence terbukti beneran, bukan cuma didesain). Image: `xmail:dev-amd64`, 25.5MB disk / 6.48MB content, tarball `dist/xmail-dev-linux-amd64-docker.tar.gz` 6.2MB.
- [x] **`scripts/release.sh docker-arm64`** — **berhasil, diverifikasi penuh via emulasi QEMU** (buildx `linux/arm64` di host `linux/amd64`, proxy realistis untuk board Armbian sungguhan). Build makan waktu jauh lebih lama dari amd64 (compile Go under emulasi CPU berat, >5 menit, jalan di background) tapi selesai bersih. Container jalan (`docker run` dengan warning normal "platform mismatch, emulated" dari Docker, bukan error), `curl /healthz` + create + list akun semua sukses — **`modernc.org/sqlite` (pure-Go) terbukti benar-benar jalan di arsitektur ARM64**, bukan cuma lolos cross-compile. Image: `xmail:dev-arm64`, 6.11MB, tarball `dist/xmail-dev-linux-arm64-docker.tar.gz` 5.9MB.
- [x] **Round-trip distribusi dibuktikan**: `docker rmi` kedua image → `docker load < dist/*.tar.gz` untuk keduanya → image kembali utuh dengan image ID identik. Artefak di `dist/` terbukti benar-benar portable (bisa didistribusikan & dipakai di mesin lain), bukan cuma "ada file"-nya.
- [x] `scripts/release.sh windows-amd64` — **berhasil, diverifikasi 3x** (langsung lewat script, lewat command yang identik `make` — `make` sendiri tidak terpasang di mesin ini, limitasi tooling terpisah dari kebenaran Makefile-nya — dan lewat `scripts/release.ps1` dari PowerShell native). Output: `dist/xmail-tray-windows-amd64-<version>.exe` + `dist/SHA256SUMS.txt`, checksum diverifikasi cocok, binary dijalankan dan log versi benar.
- [x] **`scripts/release.ps1`** ditambahkan — `.sh` cuma jalan di Git Bash/WSL/Linux/macOS, jadi dibuatkan versi PowerShell native (fungsinya identik, `-Target`/`-Version`, output `.tar` biasa bukan `.tar.gz` untuk Docker target supaya tidak butuh binary `gzip` eksternal di Windows polos) supaya user Windows tanpa Git Bash/WSL tetap bisa build release tanpa install apa pun tambahan selain Go (dan Docker untuk target Docker). **Diverifikasi end-to-end dari PowerShell asli** (bukan Git Bash): build → checksum cocok (`Get-FileHash`) → binary dijalankan → log versi benar.
- [x] `dist/SHA256SUMS.txt` untuk ketiga artefak (2 Docker tarball + 1 Windows exe) — **diverifikasi cocok** (`sha256sum -c` → `OK` untuk semua).
- [ ] (opsional) Installer Windows (Inno Setup/NSIS) — **tidak dikerjakan**, tetap backlog sesuai rencana awal (bukan wajib rilis pertama).
- [ ] Tag versi di git — **sengaja tidak dilakukan otomatis**: `git tag`+push adalah operasi yang berdampak ke riwayat repo bersama, di luar scope "implement & verify" yang aman tanpa persetujuan eksplisit. Jalankan manual kapan pun siap rilis: `git tag v0.1.0 && scripts/release.sh all v0.1.0`.
- [ ] `.github/workflows/release.yml` — **tidak dibuat**: repo ini belum ada remote GitHub (`git remote -v` kosong), jadi CI otomatis belum relevan. Backlog begitu repo di-push ke GitHub — begitu ada, workflow-nya tinggal panggil `scripts/release.sh all "$GITHUB_REF_NAME"` per OS runner, karena semua logic sudah di script, bukan di Makefile/CI config.
- [x] Verifikasi lintas-3-target dengan operasi yang sama: **Docker x64, Docker arm64, dan (dari sesi sebelumnya) Windows x64 semuanya sudah dibuktikan bisa create+list akun via REST API dengan hasil identik** — bukti langsung §1 rule 1 ARCHITECTURE.md (satu `internal/app.Run` untuk semua target) bekerja sesuai desain, bukan cuma klaim di atas kertas.

> **Ringkasan status akhir**: **ketiga target rilis selesai & terverifikasi jalan sungguhan** — bukan cuma "siap secara kode". Docker Desktop yang tadinya dikira tidak tersedia ternyata cuma salah launcher path; setelah dibetulkan, kedua target Docker berhasil dibuild, dan proses verifikasi ini sendiri menemukan + memperbaiki 1 bug produksi yang serius (container crash on startup karena permission `/app/data`) yang tidak mungkin ketahuan tanpa benar-benar menjalankan container-nya. Artefak akhir di `dist/`: `xmail-dev-linux-amd64-docker.tar.gz` (6.2MB), `xmail-dev-linux-arm64-docker.tar.gz` (5.9MB), `xmail-tray-windows-amd64-dev.exe` (13.5MB), semua dengan checksum terverifikasi.

### Fase 9 (opsional, backlog)
- MCP client capability (xmail memanggil MCP server eksternal)
- OAuth2 provider (Gmail/Outlook modern auth)
- IMAP IDLE / push notification
- Rate limiting per API key

## 6. Testing Strategy

Prinsip: piramida test — banyak unit test cepat (stdlib `testing`, tanpa network), integration test secukupnya (pakai fake/in-process server dulu, baru docker kalau perlu realistis), dan checklist manual buat hal yang gak bisa diotomasi penuh (tray icon, Windows Service beneran).

Tidak pakai testing framework tambahan (`testify`, dll) — stdlib `testing` + table-driven test cukup, sejalan dengan prinsip "minim dependency" project ini. Kalau assertion jadi berulang-ulang, boleh dipertimbangkan lagi belakangan.

### 6.1 Unit Test (jalan di `go test ./...`, tanpa network/docker)

Ditulis di file `_test.go` bersebelahan dengan kode yang diuji, jalan sebagai bagian dari `make test` default — **wajib hijau sebelum tiap fase dianggap selesai**.

| Package | Yang diuji | Teknik |
|---|---|---|
| `internal/cryptox` | Roundtrip `Encrypt`→`Decrypt` menghasilkan plaintext yang sama; ciphertext beda tiap panggilan (nonce random); gagal decrypt kalau key/nonce salah | table-driven, langsung |
| `internal/config` | `Load()` parse semua env var dengan benar; error jelas kalau `XMAIL_ENCRYPTION_KEY`/`XMAIL_API_KEY` kosong atau key bukan base64 32-byte | `t.Setenv` per case |
| `internal/account` (repository) | CRUD akun, credential tersimpan terenkripsi (baca kolom `credentials.encrypted_secret` langsung, pastikan bukan plaintext password), cascade delete ke `credentials`/`messages_cache` | sqlite in-memory (`modernc.org/sqlite` dengan DSN `:memory:` atau file temp via `t.TempDir()`), jalankan migration yang sama dengan production |
| `internal/account` (service) | Validasi input (field wajib per protokol yang diisi), `TestConnection` dispatch ke implementasi yang benar sesuai protokol | **mock** `mailer.Sender`/`Fetcher`/`Checker` — karena semua sudah didesain sebagai interface (lihat §1), tinggal buat struct mock kecil di test file, tidak butuh network sama sekali |
| `internal/mailer/smtp`, `imap`, `pop3` | Mapping `account.TLSMode` → opsi TLS library yang benar (unit, tanpa dial beneran); parsing/format pesan | unit murni untuk logic mapping; koneksi network masuk ke §6.2 integration |
| `internal/api` | Tiap handler: status code & body JSON envelope yang benar untuk kasus sukses & error (400/401/404/500), `apiKeyAuth` middleware menolak key salah/kosong | `net/http/httptest` (`httptest.NewServer` / `httptest.NewRecorder`), service di-mock lewat interface yang sama dengan di atas |
| `internal/mcpserver` | Tiap tool (`list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`) mengembalikan schema/error yang benar untuk input valid & invalid | panggil tool handler langsung (in-process), service di-mock |

### 6.2 Integration Test (butuh "server" beneran, tapi tetap lokal — tanpa akun email asli)

Ditandai build tag `//go:build integration` supaya tidak ikut jalan di `go test ./...` biasa (jaga `make test` tetap cepat & tanpa dependency eksternal). Dijalankan terpisah via `make test-integration`.

- **SMTP**: pakai `github.com/emersion/go-smtp` sebagai server in-process (jalan di goroutine dalam test, listen di `127.0.0.1:0`) — kirim email lewat `internal/mailer/smtp.Client` ke server ini, assert server menerima envelope/body yang benar. Uji ketiga `tls_mode` (`tls` pakai self-signed cert dari `crypto/tls`+`crypto/x509` yang di-generate saat test, `starttls`, `none`).
- **IMAP**: pakai `github.com/emersion/go-imap/v2/imapserver` dengan in-memory backend sebagai server in-process — `internal/mailer/imap.Client` connect, fetch, mark-as-read, assert hasil. Uji folder selain INBOX.
- **POP3**: tidak ada server in-process Go yang umum dipakai — tulis fake POP3 server minimal sendiri di `internal/mailer/pop3/testserver_test.go` (cuma implement command yang dipakai: `USER`, `PASS`, `STAT`, `LIST`, `RETR`, `QUIT`), cukup untuk uji `internal/mailer/pop3.Client`.
- **Storage**: migration runner jalan idempoten kalau dipanggil 2x ke DB yang sama (tidak error, tidak duplikat).
- (Opsional, kalau butuh realisme lebih — mis. sebelum rilis) `docker-compose.test.yml` dengan [Mailpit](https://github.com/axllent/mailpit) sebagai mail server sungguhan (SMTP+IMAP via container) untuk smoke test manual sebelum tag rilis; lihat §6.5.

### 6.3 API & MCP Contract Test (end-to-end dalam 1 proses, DB temp file asli)

- `internal/api`: satu test suite yang boot `api.NewServer` beneran di atas sqlite temp file (bukan mock repository) + `mailer.Sender`/`Fetcher` versi in-process dari §6.2 → jalankan skenario penuh: buat akun → kirim email → fetch email → cek `messages_cache` terisi. Ini jadi regression test utama tiap kali ubah kontrak API di PLAN.md §3.
- `internal/mcpserver`: skenario yang sama tapi lewat tool MCP, pastikan hasilnya konsisten dengan REST API (keduanya manggil `account.Service` yang sama — test ini yang membuktikan itu benar).

### 6.4 Race & Static Checks

- `go test -race ./...` — wajib, karena API server + MCP server + koneksi mailer jalan concurrent (goroutine).
- `go vet ./...` — sudah dipakai sejak Fase 0, tetap wajib tiap fase.
- (opsional) `golangci-lint run` kalau mau linting lebih ketat; tidak wajib untuk MVP supaya tidak menambah friction di awal.

### 6.5 Testing per Target Rilis (manual/E2E)

Karena ada 3 artefak rilis (§0.1) dengan cara start/stop berbeda, unit+integration test di atas tidak cukup — perlu checklist manual sebelum tag versi baru (bagian dari Fase 8, lihat §5):

- [ ] Docker x64: `docker run` image hasil `release-docker-amd64`, hit semua endpoint via `curl`, kirim+fetch ke 1 akun email test asli.
- [ ] Docker Armbian (arm64): sama seperti di atas, tapi jalan di board ARM asli (atau minimal via QEMU emulation kalau tidak ada board fisik) — pastikan `modernc.org/sqlite` (pure Go, tanpa CGO) tidak punya masalah performa/kompatibilitas di arch ini.
- [ ] Windows x64: checklist sudah ada di Fase 7 (icon tray muncul, menu Start/Stop, Install/Uninstall sebagai Windows Service, `sc query XmailService`).
- [ ] Ketiga target dites terhadap **akun email yang sama** untuk memastikan hasil identik (bukti `internal/app.Run` benar-benar jadi satu-satunya sumber behavior).

### 6.6 Coverage Target

Tidak menetapkan angka coverage keras untuk MVP (bisa jadi gaming metric yang salah arah untuk project sekecil ini). Yang wajib: setiap fungsi publik yang menyentuh **kredensial atau enkripsi** (`internal/cryptox`, jalur encrypt/decrypt di `internal/account`) harus punya unit test — ini bagian paling kritis untuk di-regress-i diam-diam.

## 7. Dependency List (untuk `go.mod` saat implementasi)

```
modernc.org/sqlite       ✅ terpasang (Fase 1)
github.com/google/uuid   ✅ terpasang (Fase 1)
github.com/wneessen/go-mail  ✅ terpasang (Fase 2)
github.com/emersion/go-imap/v2   ✅ terpasang (Fase 3)
github.com/knadh/go-pop3         ✅ terpasang (Fase 4)
github.com/mark3labs/mcp-go      ✅ terpasang (Fase 5)
```

Khusus target Windows x64 (Fase 7 — dipisah karena hanya dibutuhkan saat build dengan `-tags xmailtray`, tidak pernah ikut ke Docker):

```
github.com/kardianos/service   ✅ terpasang (Fase 7)
fyne.io/systray                ✅ terpasang (Fase 7)
```

Test-only (dipakai di file `_test.go` untuk integration test §6.2, **tidak pernah masuk ke binary rilis** karena `_test.go` tidak ikut ter-compile oleh `go build`):

```
github.com/emersion/go-smtp   ✅ terpasang (Fase 2, integration test)
github.com/emersion/go-sasl   ✅ terpasang (Fase 8 crosscheck — CRAM-MD5 auth server hand-rolled untuk regression test bug SMTP AUTH, sub-dependency dari go-smtp)
github.com/emersion/go-imap/v2/imapserver  ✅ terpasang (Fase 3, integration test — sub-paket dari dependency yang sama dengan client)
```

> Catatan: versi exact di-pin saat `go get` pertama kali dijalankan (Fase 1 untuk deps inti, Fase 7 untuk deps Windows tray, Fase 2/3 untuk test-only deps saat integration test SMTP/IMAP ditulis), bukan di-hardcode di sini supaya selalu ambil rilis stabil terbaru saat itu.

## 10. Resolusi CODE_REVIEW.md

Setelah Fase 1-8 selesai, sebuah review dua-axis independen (Standards vs Spec, dibandingkan ke `PRD.MD`/`PLAN.md`/`ARCHITECTURE.md`) dijalankan atas seluruh worktree — hasilnya di `CODE_REVIEW.md` (tetap disimpan di repo sebagai arsip). Semua temuan diverifikasi manual ke kode asli dulu sebelum diperbaiki (bukan langsung percaya), lalu semua di-fix dengan regression test. Ringkasan:

### 10.1 Bug nyata (Spec: "terimplementasi tapi salah")

| # | Temuan | Fix | Test pembukti |
|---|---|---|---|
| 1 | `DELETE /accounts/{id}` return `204` tanpa body — melanggar kontrak envelope `{data,error}` yang didokumentasikan berlaku untuk **semua** endpoint | Ganti ke `200 {"data":{"deleted":true},"error":null}` (`internal/api/accounts_handler.go`) | `TestAccountsCRUD_EndToEnd` di-update untuk assert envelope, bukan status 204 |
| 2 | POP3 `tls_mode: starttls` lolos validasi (`Validate`) padahal `pop3.Client.connect()` menolaknya secara eksplisit — akun bisa ke-save (201) padahal semua operasi POP3-nya pasti gagal | `Validate` sekarang menolak `pop3`+`starttls` secara spesifik (`internal/account/service.go`) | `TestValidate/pop3_starttls_rejected...` + `pop3_tls_accepted` |
| 3 | **`messages_cache` write-only** — `Repository.ListMessages` cuma dipanggil dari test, `Service.FetchMessages` SELALU live-fetch ke server. Requirement caching PRD.MD §6.3 ("fetch berikutnya lebih cepat") sebenarnya tidak pernah terpenuhi | `FetchMessages` sekarang cache-first: cek `ListMessages` dulu, cuma dial server kalau cache kosong; parameter `refresh=true`/`bool` (REST query param & MCP arg) buat paksa live-fetch | `TestService_FetchMessages_ServesFromCacheOnSecondCall` (pakai fetch-call-counter, bukan cuma cek isi tabel) + `TestMessagesList_Refresh` (level REST) |

### 10.2 Requirement PRD yang hilang (Spec: "hilang/parsial") — semua diimplementasikan penuh, bukan cuma dicoret dari PRD

| # | Requirement | Implementasi |
|---|---|---|
| 4 | Custom headers di send (`PRD.MD §6.2`) | `mailer.OutgoingMessage.Headers map[string]string` → `smtp.Client.Send` pakai `gomail.SetGenHeaderPreformatted`; exposed di `sendRequest.Headers` (REST) & `sendEmailArgs.Headers` (MCP, schema object) |
| 5 | IMAP mark-as-read (`PRD.MD §6.3`) | Interface baru `mailer.Marker` (cuma IMAP implement — POP3 tidak ada konsep flag per-pesan), `imap.Client.MarkRead` (STORE +FLAGS \Seen via UID), `Service.MarkRead`, endpoint baru `POST /accounts/{id}/messages/read`, `Repository.MarkMessageRead` buat sinkron cache |
| 6 | Attachment list di cache (`PRD.MD §6.3`) | `mailer.Message.Attachments []string`, `imap.Client.Fetch` sekarang minta `BODYSTRUCTURE` (dengan `Extended: true` — sempat 3x gagal karena detail wire-protocol IMAP: butuh `Extended` non-nil di semua part + `Encoding`/`Size`/`Text.NumLines` terisi, baru ketahuan lewat integration test beneran, bukan cuma baca dokumentasi API), fungsi `attachmentNames` walk body structure cari part dengan filename+disposition bukan-inline. Migration baru `0002_message_attachments.sql` (kolom `attachments TEXT`, JSON array) |
| 7 | MCP Client extensible interface (`PRD.MD §3/§6.4`) | ~~`internal/mcpserver/client.go`: interface `ExternalToolCaller`~~ — sempat dibuat sebagai stub murni, **lalu dihapus lagi** setelah putaran review kedua menandainya sebagai dead abstraction (nol pemanggil). Lihat §10.5 #16 untuk keputusan finalnya: backlog penuh ke Fase 9, tanpa stub apa pun sampai ada use case konkret |

### 10.3 Kontradiksi PRD↔kode (didokumentasikan, bukan dipaksa jadi kode)

| # | Temuan | Resolusi |
|---|---|---|
| 8 | PRD.MD §8 bilang "API key disimpan sebagai hash di tabel `api_keys`" — kenyataannya 1 static key dari env, tidak pernah disimpan sama sekali | **PRD.MD diperbaiki** (bukan kode) — diklarifikasi bahwa itu deskripsi rencana fitur multi-key (tabel `api_keys` disiapkan untuk itu), bukan behavior MVP. Memaksakan hashing untuk 1 key statis yang tidak pernah disimpan tidak masuk akal; multi-key beneran tetap Fase 9 backlog |

### 10.4 Code smell (Standards, semua judgement call — didokumentasikan mana yang di-fix vs sengaja dibiarkan)

| # | Temuan | Keputusan |
|---|---|---|
| 9 | Windowing arithmetic (newest-first, limit/offset) terduplikasi persis di `imap/client.go` dan `pop3/client.go` | **Di-fix**: diekstrak jadi `mailer.WindowRange(total, limit, offset) (start, end int, ok bool)` di `internal/mailer/types.go`, dipakai keduanya |
| 10 | Default `protocol`/`folder`/`limit` diimplementasikan ulang di 3 tempat (`internal/api`, `internal/mcpserver`, `account.Service`) | **Di-fix**: `account.DefaultProtocol`/`DefaultFetchLimit` jadi satu-satunya sumber, `folder`/`protocol` sekarang di-default cuma di `Service` (handler REST/MCP pass-through string kosong apa adanya). **Efek samping ketemu saat fix ini**: `CheckNew`/`FetchMessages` dulu default protocol di `resolveFetcher` doang tapi tetap pakai variabel `protocol` asli (bisa `""`) buat key cache `ExistingUIDs`/`UpsertMessages` — kalau caller-side defaulting dihapus, ini jadi bug nyata (cache key `""` vs fetcher yang dipakai `"imap"`, mismatch). Diperbaiki dengan default juga di awal `FetchMessages`/`CheckNew` sendiri |
| 11 | `(cfg, username, secret)` dan `(accountID, protocol, folder)` "data clump" mengalir lewat banyak factory/method | **Sengaja tidak di-refactor jadi struct wrapper** — Go idiomatically oke dengan parameter list pendek eksplisit; bikin struct pembungkus buat 3 field yang selalu dipakai bareng cuma nambah indirection tanpa manfaat jelas, dan bakal nyentuh ~10 file (semua factory type + semua mock di test). Tidak sepadan untuk smell tingkat "judgement call" |
| 12 | `protocol` di-switch dua kali (`TestConnection`, `resolveFetcher`) untuk pilih config+factory | **Di-fix sebagian**: bagian "pilih `ConnectionConfig` dari protocol" diekstrak jadi `connConfigForProtocol` (satu tempat). Bagian "pilih factory" TETAP dua switch terpisah — `TestConnection` butuh `TesterFactory` (3 protokol: smtp/imap/pop3), `resolveFetcher` butuh factory fetch (2 protokol: imap/pop3) — beda interface & beda set protokol, unifikasi penuh bakal butuh abstraksi generik yang kurang type-safe |
| 13 | `Service.Update` pakai `validateSecret = "unchanged"` — magic string cuma buat lolos cek non-empty | **Di-fix**: `Validate` sekarang `Validate(a Account, requireSecret bool, secret string)` — `Update` kirim `secret != nil` sebagai `requireSecret`, tidak butuh nilai palsu apa pun |
| 14 | `connHost`/`connPort`/`connTLSMode` — 3 fungsi nil-guard identik bentuknya, beda 1 field | **Di-fix**: digabung jadi `connFields(c *ConnectionConfig) (host, port, tlsMode any)` |

**Hasil akhir (putaran pertama)**: 68 unit test (naik dari 64) + 87 integration test (naik dari 76) — semua hijau. Migration baru `0002_message_attachments.sql`. Tidak ada fitur yang dihapus dari PRD untuk "menyelesaikan" kontradiksi — satu-satunya penyesuaian dokumen (bukan kode) adalah klarifikasi API key hashing (#8), karena itu memang murni salah deskripsi rencana vs realita, bukan requirement yang keliru.

### 10.5 Putaran kedua — hasil review atas fix putaran pertama

Fix di §10.1-§10.4 sendiri di-review lagi (manusia/agent lain), dan menemukan 1 bug baru + 3 hal susulan:

| # | Temuan | Resolusi |
|---|---|---|
| 15 | **🔴 Bug baru, serius**: urutan hasil `ListMessages` **terbalik setelah cache terisi**. `UpsertMessages` pakai satu `now` untuk seluruh batch → semua row dalam 1 batch punya `fetched_at` identik → `ORDER BY fetched_at DESC, rowid DESC` jatuh ke tie-break `rowid DESC`. Tapi `UpsertMessages` insert `msgs` apa adanya (newest-first, sesuai urutan `Fetch`), dan SQLite kasih rowid naik sesuai urutan insert — jadi pesan terbaru (elemen pertama, di-insert duluan) dapat rowid **terkecil** di batch itu, dan `rowid DESC` menaruhnya di **akhir**. Efeknya: `GET /messages` newest-first cuma pas cache masih kosong (baru sekali fetch), begitu cache "hangat" (fetch kedua dst.) urutannya kebalik jadi oldest-first — arah paginasi jadi salah. `TestRepository_UpsertAndListMessages` yang ada cuma assert `len`, tidak pernah assert urutan, jadi lolos begitu saja. Fix: kolom baru `sort_rank` (`migrations/0003_message_sort_rank.sql`), diisi eksplisit dari posisi tiap pesan di `msgs` (`len(msgs)-i`, independen dari rowid/urutan insert SQLite atau format string `fetched_at`), dipakai sebagai tie-breaker pengganti `rowid`. Regression test `TestRepository_UpsertMessages_PreservesOrder` — **dikonfirmasi gagal tanpa fix** (assert order `[3,2,1]`, dapat `[1,2,3]` persis seperti laporan), **lolos dengan fix**, termasuk skenario re-upsert batch yang sama (row lama di-`UPDATE`, bukan di-`INSERT`, yang mana rowid-nya tidak berubah — kasus yang bikin fix berbasis "urutan insert ulang" saja tidak cukup, harus kolom eksplisit). |
| 16 | `ExternalToolCaller` (§10.2 #7) adalah abstraksi mati — tidak ada satu pun pemanggil `.CallTool()` di mana pun | **Dihapus total** (`internal/mcpserver/client.go` dibuang), bukan dipertahankan sebagai stub. Interface spekulatif tanpa consumer nyata tidak benar-benar "menyiapkan" apa-apa — bentuknya cuma tebakan yang kemungkinan besar salah dan harus diubah lagi begitu ada use case sungguhan. PRD.MD direvisi: fitur MCP Client backlog penuh ke Fase 9, termasuk desain interface-nya nanti, bukan sekarang |
| 17 | Guard `if protocol == "" { protocol = DefaultProtocol }` terduplikasi di 4-5 method (`TestConnection`, `resolveFetcher`, `FetchMessages`, `CheckNew`, `MarkRead`) — regresi dari fix §10.4 #10 yang niatnya justru menghilangkan duplikasi begini | `resolveFetcher` sekarang **mengembalikan** protocol yang sudah di-default (`(fetcher, resolvedProtocol, err)`, ganti dari `(fetcher, Account, secret, err)` yang 2 return value-nya tidak pernah dipakai caller manapun — sekalian dibersihkan). `FetchMessages`/`CheckNew`/`MarkRead` pakai nilai balik ini, tidak guard sendiri lagi. Sisa 2 guard (`TestConnection` + `resolveFetcher`) tidak bisa disatukan lebih jauh karena `TestConnection` genuinely jalur kode terpisah (tidak lewat `resolveFetcher`, butuh `TesterFactory` bukan `Fetcher`) |
| 18 | Doc drift: PRD.MD §6.5 (skema messages_cache belum ada `attachments`/`sort_rank`), §7 (endpoint sketch belum ada `POST .../messages/read`, belum sebut `refresh`/`headers`) | Diperbaiki — lihat §6.5/§7 di PRD.MD, sudah mengacu ke file migrasi asli alih-alih skema "indikatif" |

**Hasil akhir (putaran kedua)**: `go test ./...` = 69 (naik dari 68), `go test -tags integration ./...` = 88 (naik dari 87) — migration baru `0003_message_sort_rank.sql`, 1 file dihapus (`mcpserver/client.go`), `resolveFetcher` return signature disederhanakan. Semua hijau, dites 2x pola sama (fail-tanpa-fix → pass-dengan-fix) seperti bug-bug sebelumnya.

### 10.6 Putaran ketiga — 3 pelanggaran keras + smell + spec gap tambahan

Review ketiga menemukan level pelanggaran yang lebih tinggi dari putaran sebelumnya: **3 "hard violation"** (bukan cuma judgement call), salah satunya bikin fitur inti (Windows Service) tidak pernah bisa jalan sama sekali.

| # | Temuan | Resolusi |
|---|---|---|
| 19 | **🔴 Hard, terburuk**: Windows Service yang di-install **tidak akan pernah start**. `internal/winservice/service.go` (`New`) menyusun `service.Config` tanpa `EnvVars`, jadi proses yang di-launch Windows Service Control Manager (SCM) — proses **baru**, environment **kosong**, tidak mewarisi apa pun dari sesi tray interaktif — tidak pernah melihat `XMAIL_API_KEY`/`XMAIL_ENCRYPTION_KEY`. `cmd/xmail-tray/main.go` manggil `config.Load()` di baris pertama `main()`, sebelum cek `winservice.Interactive()` — jadi service yang baru di-install langsung `log.Fatalf` begitu SCM start dia, tanpa sempat masuk ke logic service sama sekali. Fix: `buildServiceConfig` (diekstrak dari `New` biar testable) sekarang mengisi `EnvVars` dari `cfg` yang sudah ter-load (termasuk re-encode `EncryptionKey` balik ke base64) — dikonfirmasi `kardianos/service` memang support `EnvVars` di Windows (nulis ke registry service). **Tidak bisa instal service beneran buat verifikasi** (butuh Administrator, tidak tersedia di sesi ini) — sebagai gantinya, test `TestBuildServiceConfig_EnvVarsRoundTrip` mensimulasikan persis kondisi kegagalannya: build `EnvVars` dari cfg hasil `config.Load()` sesi interaktif, lalu **kosongkan total** env (simulasi proses SCM baru), set ulang cuma dari `EnvVars` itu, panggil `config.Load()` lagi — **dikonfirmasi gagal tanpa fix** (`XMAIL_API_KEY is required`, persis error yang akan dialami service asli), **lolos dengan fix**. |
| 20 | **🔴 Hard**: `.env`/`godotenv` yang dikunci `PLAN.md` §0 ternyata tidak ada kodenya sama sekali — `go.mod` tidak punya `godotenv`, `config.Load` cuma `os.Getenv`. Alur dev di README (`cp .env.example .env; make run`) tidak pernah benar-benar membaca file itu. | **Diimplementasikan beneran** (bukan cuma didokumentasikan sebagai keterbatasan) — `github.com/joho/godotenv` ditambahkan, `config.Load()` panggil `godotenv.Load()` duluan (best-effort, tidak error kalau file tidak ada; tidak pernah override env var asli yang sudah di-set — itu perilaku default `godotenv`, cocok untuk semantik "opsional untuk dev, tidak untuk prod"). Diverifikasi 2 cara: (a) unit test `TestLoad_ReadsDotEnv`/`TestLoad_RealEnvOverridesDotEnv` (harus pakai `os.Unsetenv` beneran, bukan `t.Setenv(k,"")`, karena `godotenv` cek keberadaan key di `os.Environ()` yang tetap true walau value-nya string kosong — ini sempat bikin test pertama gagal duluan sebelum saya sadar akar masalahnya ada di test-nya, bukan implementasinya); (b) live: build binary, jalankan dengan `env -i` (environment benar-benar kosong kecuali `PATH`) + `.env` di working directory — server jalan & `curl` sukses pakai API key dari `.env`. |
| 21 | **Hard→sekarang selesai**: `PRD.MD` §7 belum menyebut `GET /healthz`, §6.6 masih bilang "satu script" padahal `scripts/release.ps1` sudah ada sejak beberapa fase lalu. | Ditambahkan ke §7 (baris `GET /healthz`) dan §6.6 (frasa "dua script setara"). |
| 22 | MCP `send_email` membuang `cc`/`bcc` yang REST punya (`sendRequest` vs `sendEmailArgs`) | **Di-fix**: `sendEmailArgs` dapat field `CC`/`BCC`, schema tool dapat `mcp.WithArray("cc"/"bcc", ...)`, handler teruskan ke `mailer.OutgoingMessage`. Test baru di `TestHandleSendEmail` assert `sent.CC`/`sent.BCC`. **Attachments tetap sengaja REST-only** (didokumentasikan di kode: base64 attachment di argumen tool call MCP itu pengalaman yang buruk buat kebanyakan client, beda dengan REST yang JSON body-nya memang harus dukung itu apapun yang terjadi) |
| 23 | Baseline smell — 4 ditemukan, semua di-fix: (a) guard `folder == "" → "INBOX"` ditulis ulang 6× di `account/service.go` + `mailer/imap/client.go`; (b) `queryOr` di `api/messages_handler.go` didefinisikan tapi tidak pernah dipanggil (dead code — `handleMessagesList` sudah pakai `r.URL.Query().Get` langsung); (c) `timeFormat` di `api/dto.go` cuma nulis ulang string literal `time.RFC3339`; (d) `serverVersion = "0.1.0"` di `mcpserver/server.go` hardcoded, lepas total dari mekanisme stamping `-X main.version` yang sudah dipakai `cmd/xmail`/`cmd/xmail-tray` | (a) `mailer.DefaultFolder(folder)` — helper baru di `internal/mailer/types.go`, dipakai di 6 tempat itu; (b) `queryOr` dihapus; (c) langsung pakai `time.RFC3339`, const `timeFormat` dihapus; (d) `mcpserver.New` sekarang terima parameter `version string` (dipakai buat `server.NewMCPServer(serverName, version)`), di-thread dari `cmd/xmail`/`cmd/xmail-tray`'s `version` var lewat `internal/app.Run(ctx, cfg, version)` → `internal/winservice.New(cfg, version)` (sekalian field baru `program.version`). **Diverifikasi live**: build dengan `-ldflags "-X main.version=v9.9.9-test"`, panggil MCP `initialize` via `curl`, `serverInfo.version` di response persis `"v9.9.9-test"`. |
| 24 | Spec — `PRD.MD` §6.3 frasa "Body & metadata email (...)" ambigu, bisa dibaca seolah body ikut di-cache padahal daftar field yang disebut cuma metadata (realitanya memang cuma metadata+attachment list, sesuai desain `ARCHITECTURE.md`) | Diperjelas jadi "**Metadata email** (...)" + kalimat eksplisit "isi/body email sengaja tidak di-cache" |
| 25 | Spec — `PRD.MD` §6.1 "test-connection ... sebelum disimpan permanen" tidak match realita (endpoint butuh `{id}` yang sudah tersimpan) | Ditambah catatan "ketegangan desain yang disadari" menjelaskan urutan realistisnya (`POST /accounts` → `test-connection` → `PUT`/`DELETE` kalau gagal) dan alasan tidak diubah jadi validasi-sebelum-simpan (akan duplikasi logic config-parsing, tidak sepadan untuk MVP) |

**Hasil akhir (putaran ketiga)**: `go test ./...` = 71 (naik dari 69), `go test -tags integration ./...` = 90 (naik dari 88), plus `go test -tags xmailtray ./...` sekarang mencakup 1 test baru (`internal/winservice`, tidak pernah ter-hitung di angka default karena butuh build tag `xmailtray`). Dependency baru: `github.com/joho/godotenv`. File baru: `internal/winservice/service_test.go`. Tidak ada penghapusan fitur — semua "hard violation" diperbaiki dengan kode nyata (bukan diturunkan jadi catatan dokumentasi), kecuali #21/#24/#25 yang memang murni soal dokumen tidak sinkron dengan realita kode yang sudah benar.

### 10.7 Putaran keempat — follow-up Windows Service (env var & DB path)

Verifikasi ulang atas perbaikan putaran 4 menemukan bahwa fix `EnvVars` (#19) perlu tetapi belum cukup untuk service yang benar-benar jalan, plus satu setelan yang tercecer dan drift dokumen.

| # | Temuan | Resolusi |
|---|---|---|
| 26 | **Latent, sedang**: `buildServiceConfig` meneruskan `XMAIL_DB_PATH` apa adanya, dan defaultnya relatif (`xmail.db`, lihat `.env.example:2`). Windows SCM meluncurkan service dengan cwd `%SystemRoot%\System32`, jadi service terinstall berkonfigurasi default akan mencoba membuat/membuka DB di `System32` — gagal (access denied), atau memakai DB yang berbeda dari tray. `service.Config.WorkingDirectory` tidak bisa menambal: `go doc github.com/kardianos/service.Config` menyatakannya *"not supported on Windows"* | `buildServiceConfig` sekarang meng-absolutkan `DBPath` (`filepath.Abs`) sebelum ditulis ke `EnvVars`; regression test `TestBuildServiceConfig_AbsolutizesDBPath`. Absolutisasi memakai cwd proses interaktif saat install (didokumentasikan di fungsi) — tray foreground dan service konsisten selama dijalankan dari direktori yang sama |
| 27 | **Minor**: `XMAIL_MCP_STDIO` tidak ikut masuk `EnvVars`, jadi service yang di-install dari sesi dengan `XMAIL_MCP_STDIO=true` diam-diam kehilangan setelan itu | Dipropagasi lewat `strconv.FormatBool(cfg.MCPStdioEnable)`; di-assert di `TestBuildServiceConfig_EnvVarsRoundTrip` |
| 28 | **Minor, doc**: penomoran putaran review tidak konsisten — `README.MD` bilang "3 putaran", `CODE_REVIEW.md` punya 4 sesi (`putaran 1`–`4`), `PLAN.md` menomori siklus resolusi berbeda. Plus `ARCHITECTURE.md` §8 paragraf pembuka masih meringkas "dua putaran" saja | `README.MD` tidak lagi menyebut angka putaran/temuan yang rapuh (menunjuk ke `CODE_REVIEW.md` + §10); `ARCHITECTURE.md` §8 diperbarui; catatan pemetaan nomor di bawah |

**Catatan pemetaan nomor**: `CODE_REVIEW.md` menomori tiap **sesi review** (`putaran 1`..`putaran 4`); `PLAN.md` §10 menomori tiap **siklus resolusi** (`§10.1-10.4` = putaran pertama, `§10.5` = kedua, `§10.6` = ketiga, `§10.7` = keempat). Karena satu sesi review ("putaran 3") diverifikasi ulang tanpa siklus resolusi terpisah, kedua penomoran berselisih satu setelah putaran kedua — dua dokumen menghitung hal berbeda, bukan bug.

**Hasil akhir (putaran keempat)**: `go build`/`go vet` (default + `-tags xmailtray`) bersih; `go test ./...` = 71; `go test -tags integration ./...` = 90; `go test -tags xmailtray ./internal/winservice/` = 2. Tidak ada perubahan perilaku di luar jalur Windows Service (`internal/winservice`, di bawah build tag `xmailtray`, tidak pernah masuk build Docker/headless).

### 10.7b Catatan pemetaan nomor (putaran 5)

`CODE_REVIEW.md` menomori **sesi review** (`putaran 1`..`putaran 6`); `PLAN.md` §10 menomori **siklus resolusi** (`§10.1-10.4` = pertama, `§10.5` = kedua, `§10.6` = ketiga, `§10.7` = keempat, `§10.8` = kelima). Satu sesi review bisa diverifikasi ulang tanpa siklus resolusi terpisah, jadi kedua penomoran bisa berselisih — itu dua dokumen menghitung hal berbeda, bukan bug.

### 10.8 Putaran kelima — bug window cache, drift kontrak MCP/healthz, smell

Review kelima (dua-axis, titik tetap `main`) menemukan 1 bug perilaku nyata plus satu set drift dokumen/smell. 3 requirement PRD yang tadinya ditandai "hilang/parsial" (peran MCP Client, attachment di MCP `send_email`, `test-connection` sebelum save) sudah didokumentasikan sendiri sebagai keputusan sadar di PRD, jadi **dibiarkan apa adanya** (bukan bug).

| # | Temuan | Resolusi |
|---|---|---|
| 29 | **🔴 Bug, terburuk**: `FetchMessages` menyajikan cache setiap kali cache berisi *baris apa pun*, mengabaikan window yang diminta. Setelah cache `limit=20`, request `?limit=50` mengembalikan 20 baris cache tanpa dial lagi — pemotongan diam-diam, melanggar pagination PRD §6.3. | **Di-fix**: migrasi `0004_message_cache_state.sql` (`coverage` + `exhausted` per account/protocol/folder), `Repository.CacheState`/`RecordFetch`, dan `Service.FetchMessages` hanya menyajikan cache kalau `exhausted` atau `offset+limit <= coverage` — kalau tidak, dial lalu catat coverage. `CheckNew` ikut mencatat coverage-nya. Regression test: `TestService_FetchMessages_LargerWindowRefetches`, `TestRepository_CacheState`. |
| 30 | **Hard/borderline**: `GET /healthz` menulis `"ok"` mentah — melanggar kontrak envelope `{data,error}` (PLAN §3, ARCHITECTURE §3.4, PRD §7). | **Di-fix**: sekarang `writeData(w, 200, {"status":"ok"})`. `TestHealthz_NoAuthRequired` assert envelope + `data.status`. Eksempasi auth tetap (satu-satunya yang istimewa). |
| 31 | **Hard**: kontrak tool MCP di §4 basi vs kode — `send_email` tidak mencantumkan `cc[]`/`bcc[]`, `fetch_emails` tidak mencantumkan `protocol?`, `check_new_emails` salah (bilang `{account_id}` saja, padahal terima `protocol?`/`folder?`). | **Di-fix**: tabel §4 disinkronkan dengan `internal/mcpserver/server.go`. |
| 32 | Default `limit` digandakan antar-adapter (`api/messages_handler.go` vs `mcpserver/server.go`), dan `limit=0` eksplisit berbeda perilaku antara REST (return kosong) vs MCP (pakai default). | **Di-fix**: normalisasi `limit <= 0 -> DefaultFetchLimit` pindah ke `Service.FetchMessages` (satu sumber); REST meneruskan `queryIntOr(...,0)`, MCP meneruskan `args.Limit` apa adanya. Test: `TestService_FetchMessages_ZeroLimitUsesDefault`. |
| 33 | `Validate` mengiterasi map `{smtp,imap,pop3}` → protokol mana yang error-nya muncul non-deterministik. | **Di-fix**: iterasi slice berurutan tetap (smtp→imap→pop3). |
| 34 | Komentar `resolveFetcher` mengklaim mengembalikan secret, padahal signature-nya tidak. | **Di-fix**: komentar merge, klaim secret dihapus. |
| 35 | Komentar `defaultFetchLimit bounds…` nyasar di atas `const defaultCheckFetchLimit`. | **Di-fix**: komentar menunjuk `defaultCheckFetchLimit`. |
| 36 | `imap.Fetch` berkomentar "sort newest-first by UID" padahal kode cuma membalik slice (mengasumsikan server mengembalikan ascending). | **Di-fix**: benar-benar `sort.Slice` descending UID (UID naik seiring kedatangan), tidak lagi bergantung urutan respons server. |
| 37 | Scope creep: `CLAUDE.md` dan `.claude/skills/xmail/SKILL.md` tidak diminta PRD/PLAN. | **Didokumentasikan** (bukan dihapus): dua file itu sengaja ada sebagai panduan agent (ringkasan arsitektur + skill `xmail`); dicatat di sini agar tidak lagi terbaca sebagai tambahan liar. |

**Dibiarkan sadar (bukan bug — sudah didokumentasikan di PRD):** peran MCP Client (PRD §6.4 backlog penuh), attachment di MCP `send_email` (PRD §6.2 terpenuhi via REST; §10.6 #22), dan `test-connection` "sebelum disimpan permanen" (PRD §6.1 ketegangan desain yang disadari).

**Hasil akhir (putaran kelima)**: `go build`/`go vet` (default + `-tags xmailtray`) bersih; `go test ./...` = **74** (naik dari 71); `go test -tags integration ./...` = **93** (naik dari 90); `go test -tags xmailtray ./internal/winservice/` = 2. Migrasi baru: `0004_message_cache_state.sql`. Tidak ada fitur yang dihapus.

### 10.9 Deepening arsitektur (architecture review — bukan siklus code review)

Review arsitektur (command `improve-codebase-architecture`) menemukan 5 kandidat *deepening* (modul dangkal → dalam), disajikan sebagai laporan HTML, lalu digrill satu per satu. Kelimanya diimplementasikan, berurutan B → E → A → C → D.

| Kandidat | Isi | Status |
|---|---|---|
| B | State machine window cache (`coverage`/`exhausted`) terbelah antara `Service.FetchMessages` (predikat) dan `Repository.RecordFetch` (yang memproduksinya) — hanya bisa diuji dengan menjalankan seluruh Service + SQLite nyata | **Diimplementasikan**: modul `MessageCache` (`internal/account/messages.go`) dengan `CacheKey`/`Window` + `Get`/`Upsert`/`Record`/`ExistingUIDs`/`MarkRead`. Predikat `offset+limit <= coverage` (termasuk guard "coverage mengklaim tapi baris kosong") pindah ke dalam `Get`; `Repository` kini hanya akun + kredensial. Test `TestMessageCache_Coverage` meng-assert predikat langsung lewat `Get`'s `served`, tanpa Service. **Crosscheck menemukan bug tambahan** (bukan regresi — diwarisi dari HEAD, cuma tidak pernah tertutup test): `Record` menggelembungkan `coverage` dan salah mengunci `exhausted` untuk fetch dengan `offset > 0`, sehingga `?offset=` diikuti `?limit=` besar diam-diam terpotong di jalur REST. Diperbaiki: fetch yang mulai di luar region kontigu-dari-atas tidak dicatat sama sekali; regression test `TestMessageCache_Record_OffsetDoesNotInflateCoverage` + `TestService_FetchMessages_OffsetDoesNotTruncateLaterWindow` (dibuktikan gagal saat guard-nya sengaja dimatikan). |
| E | "Apa yang dibutuhkan `config.Load`" didefinisikan dua kali — konstanta di `internal/config` dan literal di `internal/winservice.buildServiceConfig`, plus encode base64-nya | **Diimplementasikan**: `Config.Environ()` (inverse `Load`) di `internal/config`; `winservice` memanggilnya lalu meng-absolutkan `XMAIL_DB_PATH` (quirk cwd SCM tetap di winservice). Nama env var kini cuma di `config`. Test round-trip `TestEnviron_RoundTripsThroughLoad`. |
| A | `account.Service` dangkal: 6 field factory + 6 setter + 5 tipe factory; `ConnTester` menduplikasi `TestConnection` yang sudah ada di `Sender`/`Fetcher` | **Diimplementasikan**: satu record `account.Protocol{Sender, Fetcher}` + `Service.RegisterProtocol` (`internal/account/protocol.go`); `ConnTester`/`TesterFactory`/`SMTPSenderFactory`/`IMAPFactory`/`POP3Factory` + 6 setter dihapus. `wireMailer` jadi 3 registrasi; test-connection lewat `TestConnection` milik sender/fetcher itu sendiri. Mereverse keputusan tercatat `§10.4 #11/#12` → `docs/adr/0001-unify-protocol-dispatch.md`. |
| C | Invarian "newest-first" pada `mailer.Fetcher.Fetch` tidak dinyatakan di interface-nya; POP3 mengabaikan `folder` padahal cache memakai folder pemanggil sebagai key | **Diimplementasikan**: kontrak ordering (newest-first + `Message.Folder` cocok) ditulis di doc `mailer.Fetcher`; `canonicalFolder` di seam `Service` memaksa POP3 (folder-less) selalu `INBOX`, sehingga key cache = folder yang tersimpan. Test `TestService_FetchMessages_POP3FolderCanonicalized`; assert ordering per-protokol sudah ada di integration test `TestIntegration_Fetch*`. |
| D | REST & MCP membangun ulang `OutgoingMessage` dan bentuk hasil `check` dua kali; tool MCP tak diuji lewat transport aslinya | **Diimplementasikan**: `account.CheckResult` dipakai REST **dan** MCP (satu bentuk, satu tipe); nama tool jadi konstanta bersama (`toolSendEmail` dll) yang dipakai `registerTools` **dan** prefix error tiap handler; test transport baru `TestTransport_SendEmailBindsArguments` (client MCP in-process ↔ `Server.HTTPHandler()`) menutup celah `BindArguments`. Struct request per-skin tetap terpisah — asimetri attachment MCP memang sengaja (§10.6 #22). |

**Crosscheck ronde 2 menemukan 2 bug perilaku lagi di area cache** (keduanya diwarisi dari HEAD, bukan regresi, dan tidak tertutup test lama):

- **Urutan cache per-batch** — `sort_rank` dulu rank per-batch (`len(msgs)-i`) dan `list` mengurutkan `ORDER BY fetched_at DESC, sort_rank DESC`. Begitu cache berisi dua halaman yang di-fetch di waktu berbeda, batch terbaru mengapung ke atas: jendela yang disajikan bisa salah urutan (mis. `[u20..u39, u0..u19]`) dan bahkan menjatuhkan baris. Diperbaiki: `sort_rank` = posisi absolut dari atas mailbox (`offset+i`), `Upsert` menerima `offset`, `list` jadi `ORDER BY sort_rank ASC`; migrasi baru `0005_message_cache_position.sql` membersihkan cache lama (rank skema lama tak bermakna di aturan baru). Regression test: `TestMessageCache_ListOrdersAcrossBatches`, `TestService_FetchMessages_AdjacentPagesServeInMailboxOrder` (dibuktikan gagal saat `ORDER BY` lama dipakai).
- **`exhausted` mengunci permanen** — mailbox yang bertambah setelah cache dianggap habis akan terus disajikan terpotong tanpa dial lagi. Diperbaiki: halaman penuh yang melewati ujung yang sudah diketahui membersihkan latch (`exhausted = excluded.exhausted`, bukan `MAX`); `CheckNew` (selalu dial) jadi jalur pemulihannya. Regression test: `TestService_FetchMessages_ExhaustedCacheRecoversWhenMailboxGrows`.

**Crosscheck ronde 3 menemukan 1 bug perilaku lagi** (akar yang sama, diwarisi dari skema posisi absolut yang baru saja dipasang di ronde 2): `sort_rank` menyimpan posisi absolut, tapi tidak ada yang membatalkan cache saat top mailbox bergeser (mail baru datang / mail dihapus) — sehingga jendela yang disajikan mencampur baris basi: salah urutan, dan pada kasus lebih dalam mengembalikan **himpunan** pesan yang salah (termasuk di jalur `?offset=`, yang bahkan meninggalkan gap rank tapi tetap menaikkan coverage). Diperbaiki: `MessageCache.Upsert` memeriksa anchor (`sort_rank == offset` atau UID halaman sudah tercache di rank lain) dan membuang baris + state-nya bila mailbox bergeser. Regression test: `TestService_FetchMessages_NewMailAtTopDoesNotServeStaleRows`, `TestService_FetchMessages_OffsetPageAfterNewMailDoesNotServeStaleRows` (keduanya dibuktikan gagal saat invalidasi dimatikan).

**Crosscheck ronde 4** menutup celah yang tersisa dari ronde 3: cek anchor hanya melihat dua baris (rank `offset` dan UID pertama halaman), jadi pergeseran yang tidak menggerakkan keduanya lolos — hapus/sisip di tengah halaman meninggalkan **rank duplikat** (`sort_rank` tidak punya unique index), dan halaman dalam yang mulai setelah prefix tetap ditulis sebagai *orphan* yang bisa tersaji begitu `exhausted` terkunci. Diperbaiki: `Upsert` membandingkan **seluruh** halaman dengan posisi cache (rank-by-rank + UID halaman yang tercache di rank lain) dan menolak menulis halaman yang mulai di luar prefix. Regression test: `TestService_FetchMessages_DeepPageAfterLargeShiftDoesNotHoleTheCache`, `TestService_FetchMessages_MidMailboxDeletionDoesNotLeaveDuplicateRanks` (keduanya dibuktikan gagal saat invalidasi dimatikan). Sisa yang disadari: perubahan mailbox **di bawah** halaman yang di-fetch tidak terdeteksi dari halaman itu — cache bisa menyajikan data basi sampai `refresh=true`; itu sifat cache (bukan inkonsistensi rank), didokumentasikan di ARCHITECTURE §4.

**Crosscheck ronde 5** menemukan 1 bug sisa (pre-existing, bukan dari ronde 4): halaman **kosong** tidak membatalkan apa pun, jadi mailbox yang dikosongkan tetap menyajikan baris lama — dan `CheckNew` bahkan mengunci `exhausted` di atasnya. Diperbaiki: fetch dari atas yang mengembalikan nol pesan membuktikan mailbox kosong, jadi baris + state key itu dibuang. Sekaligus: setengah fix ronde 4 (menolak menulis halaman di luar prefix) tadinya **tak teruji** — kini dipin oleh `TestService_FetchMessages_DeepPagePastPrefixIsNotServedAsOrphans`. Regression test baru: `TestService_FetchMessages_EmptyMailboxDoesNotServeStaleRows`, `...DeepPagePastPrefixIsNotServedAsOrphans` (keduanya dibuktikan gagal saat fix-nya dimatikan).

**Crosscheck ronde 6** tidak menemukan bug baru yang terjangkau (sapuan acak 8000 langkah lulus; `dfc193a` gagal ≤99 langkah pada kelas bug yang sama). Dua hal ditutup: (a) cabang koherensi `uidAtRank` ("rank yang terisi harus memegang pesan dari halaman itu") ternyata **tak teruji** — mailbox yang diganti utuh tanpa pernah terlihat kosong hanya terdeteksi oleh cabang ini; kini dipin `TestService_FetchMessages_FullyReplacedMailboxDoesNotServeDuplicateRanks` (gagal tanpa cabang itu). (b) halaman kosong ber-`offset > 0` tidak lagi membuka transaksi (kembali early-return seperti sebelumnya). Ditambah test negatif: POP3 tanpa Checker/Marker (`TestService_POP3_HasNoCheckerOrMarker`) dan `Send` memakai email akun sebagai From (`TestService_Send_UsesAccountEmailAsFrom`).

**Crosscheck ronde 7** menemukan 2 bug nyata pada jalur baca utama, keduanya dari akar yang sama: `Record` dulu hanya menaikkan `coverage` (`MAX`) dan tak pernah memangkas baris, sementara cek koherensi `Upsert` hanya melihat rank yang benar-benar dicakup halaman. (a) **Mailbox menyusut di ekor** — 3 pesan terlama dihapus di server, top tak berubah; halaman pendek membuktikan ujung mailbox, tapi baris lama (termasuk yang sudah terhapus) tetap disajikan. Diperbaiki: halaman pendek memangkas baris `sort_rank >= offset+returned` dan menurunkan `coverage` ke ujung itu, dalam transaksi yang sama dengan penulisan state. (b) **`exhausted` basi menyembunyikan mail** — window yang mulai di luar prefix yang tercache dulu dilayani sebagai halaman kosong; sekarang dial. Ditutup juga: `offset` negatif ditolak di `MessageCache` (`Get`/`Upsert`/`Record`), dan `storage.Open` menyetel `busy_timeout(5000)` supaya proses kedua pada DB yang sama menunggu penulis alih-alih gagal `SQLITE_BUSY` (race migrasi antar-proses yang tersisa dicatat sebagai batasan). Regression test: `TestMessageCache_Record_ShortPageDropsRowsPastTheEnd`, `TestService_FetchMessages_ShrunkMailboxDoesNotServeDeletedMessages`, `TestService_FetchMessages_StaleExhaustedDoesNotHideNewMail` (ketiganya dibuktikan gagal saat fix-nya dimatikan).

**Crosscheck ronde 8** tidak menemukan regresi (sapuan acak 32.000 langkah / 400 seed lulus di `62a4cbc`; parent `b85ed94` gagal pada kedua kelas bug ronde 7). Yang ditutup: (a) `Get` membandingkan `offset+limit` yang bisa **overflow** untuk `limit` absurd (mis. `math.MaxInt` lewat REST) sehingga window terpotong disajikan dari cache — perbandingannya kini bebas overflow; (b) `Get` juga menolak melayani window yang jumlah barisnya tidak sesuai (`min(Limit, Coverage-Offset)`), jadi state `coverage > rows` — mungkin karena `Upsert` dan `Record` adalah dua transaksi terpisah yang bisa saling menyisip antar-request — berujung dial, bukan halaman kosong atau terpotong; (c) DSN juga memakai `_txlock=immediate`, karena `busy_timeout` saja tidak menutup transaksi baca-lalu-tulis (bentuk `Upsert`). Regression test: `TestService_FetchMessages_MaxLimitDoesNotTruncate`, `TestMessageCache_GetDialsWhenRowsFellBehindCoverage`, `TestMessageCache_EmptyTopPageInvalidatesStaleRows` (ketiganya dibuktikan gagal saat fix-nya dimatikan). Catatan ronde ini juga menemukan satu kebocoran scope: perubahan port `:8080 → :5569` milik pekerjaan lain ikut ter-commit lewat `SKILL.md` di `62a4cbc`; baris itu dikembalikan ke `:8080` di sini supaya HEAD konsisten dengan `internal/config/config.go`.

File baru: `CONTEXT.md` (glosarium domain), `docs/adr/0001-unify-protocol-dispatch.md`, `internal/account/protocol.go`, `internal/storage/migrations/0005_message_cache_position.sql`. Catatan perilaku: satu efek samping tak sengaja dari B (dan dipertegas C) — `MessageCache.Upsert` menyimpan baris di bawah `key.Folder` (folder ter-resolve), bukan `m.Folder` per pesan; untuk IMAP keduanya selalu sama, untuk POP3 kini selalu `INBOX`. Satu-satunya perubahan perilaku HTTP yang disengaja di §10.9 adalah perbaikan cache `offset` di atas; perubahan body `/healthz` berasal dari §10.8 #30, bukan dari deepening ini.

**Hasil akhir (§10.9)**: `go build`/`go vet` (default + `-tags xmailtray`) bersih; `go test ./...` = **101**; `go test -tags integration ./...` = **120**; `go test -tags xmailtray ./internal/winservice/` = 2 (semua dihitung sebagai baris `=== RUN`, termasuk subtest).
