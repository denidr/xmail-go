# PRD: xmail — Email-as-a-Service dengan MCP Support

## 1. Ringkasan

xmail adalah service backend yang membungkus operasi email (kirim, terima, cek) di balik REST API, mendukung banyak akun email sekaligus, dan bisa diakses juga lewat MCP (Model Context Protocol) sehingga AI agent (Claude Desktop, Claude Code, dll) bisa kirim/baca email langsung sebagai tools.

Target deployment: single binary ringan, jalan di Linux, dibungkus Docker.

## 2. Latar Belakang & Masalah

Saat ini kirim/terima email biasanya butuh integrasi langsung ke SMTP/IMAP/POP3 di tiap aplikasi, atau bergantung pada provider API pihak ketiga (SendGrid, Mailgun, dst) yang tidak selalu cocok untuk mailbox pribadi/multi-akun via protokol standar. xmail menyediakan satu service internal yang:

- Menyatukan banyak akun email (personal/kerja/berbagai provider) di satu tempat.
- Expose operasi email sebagai REST API sederhana, sekaligus sebagai MCP tools untuk AI agent.
- Ringan, self-hosted, tidak bergantung pihak ketiga.

## 3. Tujuan (Goals)

- Kirim email via SMTP (TLS & non-TLS) melalui REST API.
- Fetch & cek email via IMAP dan POP3 (TLS & non-TLS).
- Mendukung multi-akun email dalam satu instance service.
- Simpan konfigurasi akun & kredensial sebagai parameter konfigurasi di SQLite.
- Expose fungsi service sebagai MCP Server (tools untuk AI agent), dan juga mampu berperan sebagai MCP Client (konsumsi MCP server lain bila diperlukan).
- Footprint memory kecil, mudah dijalankan di Linux / Docker.
- Tersedia dalam 3 varian rilis dengan behavior yang identik (lihat §6.6): Docker x64, Docker Armbian (arm64), dan Windows x64 (system tray + bisa diinstall sebagai Windows Service).

### Non-Goals (di luar scope MVP)

- Multi-tenant / multi-user dengan sistem login (JWT, RBAC) — MVP pakai API key tunggal.
- OAuth2 login flow ke provider (Gmail/Outlook modern auth) — MVP fokus ke SMTP/IMAP/POP3 dengan username+password/app-password.
- UI dashboard (web frontend).
- Antrian/queue kirim email skala besar (bulk sending, retry policy kompleks) — cukup best-effort synchronous send di MVP.
- High-availability / horizontal scaling (SQLite = single-node).

## 4. Target Pengguna

- Diri sendiri (developer) / tim kecil yang butuh satu service internal untuk mengelola beberapa akun email.
- AI agent (via MCP) yang perlu kirim notifikasi email atau membaca inbox sebagai bagian dari automasi.

## 5. Keputusan Teknis Utama

| Area | Keputusan | Alasan |
|---|---|---|
| Bahasa/runtime | **Go** | Compile ke single static binary, footprint memory rendah (~10-20MB idle), image Docker bisa sekecil <20MB (distroless/scratch), goroutine cocok untuk banyak koneksi IMAP/SMTP concurrent, stdlib `crypto/tls` matang. |
| Alternatif dipertimbangkan | Rust | Memory paling minim, tapi ekosistem library email (SMTP/IMAP/POP3) dan MCP SDK di Rust masih kurang matang dibanding Go — risiko waktu development lebih besar untuk MVP. Bisa dipertimbangkan untuk rewrite nanti kalau butuh optimasi ekstrem. |
| Storage | SQLite (embedded, single file) | Tidak butuh service DB terpisah, cocok untuk single-node & footprint ringan. File DB di-mount sebagai volume Docker untuk persistence. |
| Auth API | API Key statis (header `X-API-Key`) | Simple untuk MVP, service dianggap dipakai internal/trusted network. |
| Packaging | Docker image (multi-stage build, base `scratch`/`distroless`) untuk Linux x64/arm64; native `.exe` + system tray untuk Windows x64 | 3 varian rilis (§6.6), image Docker kecil & portable, `.exe` Windows bisa diinstall sebagai Windows Service. |
| Protokol email | SMTP (kirim), IMAP (fetch/check, primary), POP3 (fetch, sederhana) | Sesuai requirement MVP. |
| MCP | Dual role: MCP Server (expose tools) + kemampuan jadi MCP Client | Sesuai requirement "support untuk mcp". |

## 6. Fitur Utama (MVP)

### 6.1 Manajemen Akun Email (Multi-Account)
- CRUD akun email: nama, alamat email, provider config (host/port SMTP, IMAP, POP3), mode koneksi (TLS/STARTTLS/none), username, credential (password/app-password).
- Setiap akun independen — bisa campur provider berbeda (Gmail, custom mail server, dll) dalam satu instance.
- Test-connection endpoint untuk validasi kredensial & konektivitas. **Catatan (ketegangan desain yang disadari, bukan bug)**: "sebelum disimpan permanen" di sini agak longgar — endpoint-nya (`POST /accounts/{id}/test-connection`) butuh `{id}` akun yang sudah ada, jadi urutannya realistisnya: `POST /accounts` (simpan dulu, termasuk kredensialnya) → `test-connection` (validasi) → kalau gagal, `PUT`/`DELETE` untuk perbaiki/hapus. Bukan "validasi dulu baru simpan" secara literal. Alternatif desain (validasi di endpoint terpisah tanpa perlu account tersimpan dulu) tidak diambil karena akan duplikasi logic config-parsing antara "akun beneran" vs "akun percobaan" — dianggap tidak sepadan untuk MVP.

### 6.2 Kirim Email (SMTP)
- Endpoint kirim email atas nama salah satu akun terdaftar.
- Dukung TLS (implicit TLS/SMTPS port 465), STARTTLS (port 587), dan non-TLS/plain (port 25, untuk internal/dev).
- Dukung attachment, HTML & plain text body, CC/BCC, custom headers dasar.

### 6.3 Fetch & Cek Email (IMAP/POP3)
- Fetch daftar email (dengan pagination, filter folder untuk IMAP) dari akun tertentu.
- Cek email baru (unread count / polling manual via endpoint) — tanpa push/webhook di MVP.
- IMAP: dukung folder selain INBOX, mark as read.
- POP3: fetch sederhana dari server yang tidak mendukung IMAP.
- **Metadata email** (subject, from, to, date, status baca, attachment list) disimpan/cache di SQLite agar fetch berikutnya lebih cepat (bukan selalu round-trip penuh ke mail server). Cache hanya menyajikan hasil kalau window yang diminta sudah tercakup (`messages_cache_state`); kalau tidak, fetch akan dial ke server — supaya `limit` yang lebih besar tidak diam-diam dipotong ke halaman cache lama. Halaman yang lebih pendek dari yang diminta membuktikan ujung mailbox, jadi baris cache di luar ujung itu dibuang — bukan disajikan sebagai pesan yang sudah dihapus di server (`Record` di `internal/account/messages.go`; PLAN.md §10.9). **Isi/body email (text/HTML) sengaja tidak di-cache** — di luar scope MVP (kalau butuh isi lengkap, itu tugas MUA/client di luar xmail; xmail cuma expose metadata biar ringan). *(Klarifikasi: draf awal pakai frasa "Body & metadata email" yang ambigu — dibaca sebagian orang seolah body ikut di-cache, padahal daftar field yang disebutkan cuma metadata. Diperjelas di sini setelah ditandai sebagai potensi spec gap saat review.)*

### 6.4 MCP Support
- **MCP Server**: expose tools berikut ke MCP client (Claude Desktop/Code, dll):
  - `list_accounts` — daftar akun terdaftar (tanpa expose credential).
  - `send_email` — kirim email dari akun tertentu.
  - `fetch_emails` — ambil daftar email terbaru dari akun/folder tertentu.
  - `check_new_emails` — cek jumlah email baru/unread.
- **MCP Client**: kemampuan service untuk memanggil MCP server eksternal lain bila suatu fitur butuh integrasi. **Backlog penuh** (lihat PLAN.md Fase 9) — belum ada kode sama sekali, termasuk interface, sengaja tidak di-stub duluan. *(Revisi: draf awal bilang "disiapkan sebagai extensible interface", sempat diimplementasikan sebagai interface `ExternalToolCaller` tanpa satu pun pemanggil — ditemukan saat code review sebagai dead abstraction, lalu dihapus. Keputusannya: desain interface yang benar cuma bisa ditentukan begitu ada use case konkret yang mau diimplementasikan; interface spekulatif tanpa consumer nyata cuma nebak-nebak bentuk API yang mungkin salah dan harus diubah lagi nanti.)*

### 6.5 Penyimpanan Data (SQLite)
- Semua konfigurasi akun (termasuk credential) & cache metadata email disimpan di SQLite lokal.
- Credential dienkripsi at-rest (lihat §8 Security) sebelum ditulis ke SQLite — bukan disimpan plaintext meskipun "sementara".
- Skema final (lihat migrasi asli di `internal/storage/migrations/` untuk definisi lengkap — `0001_init.sql`, `0002_message_attachments.sql`, `0003_message_sort_rank.sql`, `0004_message_cache_state.sql`, `0005_message_cache_position.sql`):
  - `accounts` (id, name, email, smtp/imap/pop3_host+port+tls_mode, username, created_at, updated_at)
  - `credentials` (account_id, encrypted_secret, nonce)
  - `messages_cache` (id, account_id, protocol, folder, uid, subject, from_addr, to_addr, date, is_read, fetched_at, attachments, sort_rank) — `attachments` = JSON array nama file, `sort_rank` = posisi absolut pesan dari atas mailbox (0 = terbaru), di-set dari offset fetch; `list` mengurutkan hanya dari kolom ini — **bukan** dari `fetched_at`/rowid, yang mencampur batch yang di-fetch di waktu berbeda (bug urutan cache, lihat PLAN.md §10.5/§10.9 dan migrasi `0003`+`0005`)
  - `messages_cache_state` (account_id, protocol, folder, coverage, exhausted, verified_from, updated_at) — `coverage` = berapa banyak window newest-first yang tercakup, `exhausted` = seluruh mailbox sudah tercache (sejak dial terakhir), `verified_from` = rank terendah yang dijamin dial terakhir (0 setelah halaman atas, offset halaman itu sendiri setelah halaman yang lebih dalam; migrasi `0006`). `FetchMessages` hanya menyajikan dari cache kalau window mulai pada atau di atas `verified_from` **dan** berada di dalam `coverage` — atau `exhausted` dan masih mulai di dalamnya — supaya request yang lebih besar tidak diam-diam dipotong ke halaman cache lama (lihat PLAN.md §10.8). Fetch yang mulai di bawah `coverage` diabaikan; halaman penuh yang melewati ujung yang diketahui **melepas** `exhausted`; halaman pendek menurunkan `coverage` ke ujung mailbox yang terbukti sambil membuang baris di luar itu. Baris di bawah `verified_from` berasal dari dial yang lebih lama (halaman dalam tidak menjamin baris di atasnya), jadi Get dial ulang untuk window di bawahnya — kalau tidak, mailbox yang berubah di atas sementara klien hanya mem-fetch halaman dalam bisa disajikan sebagai campuran baris lama dan baru (PLAN.md §10.9).
  - `api_keys` (id, key_hash, label, created_at, last_used_at) — skema disiapkan untuk multi-key, **belum dipakai** (lihat §8)

### 6.6 Distribusi / Release (3 Target)

Satu codebase, tiga artefak rilis, behavior harus identik (logika inti sama, cuma cara start/stop yang beda per platform):

| Target | Platform | Bentuk | Cara jalan |
|---|---|---|---|
| Docker x64 | `linux/amd64` | Docker image | container biasa, headless |
| Docker Armbian | `linux/arm64` | Docker image | container biasa di SBC ARM (Orange Pi/Rock Pi dst yang jalan Armbian) |
| Windows x64 | `windows/amd64` | `.exe` native | icon di system tray; dari tray bisa "Install as Windows Service" supaya jalan di background tanpa user login |

Build ketiga target dilakukan lewat **dua script setara** — `scripts/release.sh` (Git Bash/WSL/Linux/macOS/CI) dan `scripts/release.ps1` (PowerShell native, untuk Windows tanpa Git Bash/WSL) — sama-sama menerima target `docker-amd64`/`docker-arm64`/`windows-amd64`/`all`, sama-sama menghasilkan artefak siap distribusi di `dist/` beserta checksum SHA-256. Detail teknis build & packaging ada di [PLAN.md](./PLAN.md) bagian "Release Build", dan referensi kode/cara extend ada di [ARCHITECTURE.md](./ARCHITECTURE.md).

## 7. API Design (Sketsa)

```
GET    /healthz                       # health check, tanpa auth — buat container orchestrator (Docker/k8s) probe liveness
POST   /accounts                      # tambah akun email baru
GET    /accounts                      # list akun (tanpa credential)
GET    /accounts/{id}                 # detail akun
PUT    /accounts/{id}                 # update config akun
DELETE /accounts/{id}                 # hapus akun
POST   /accounts/{id}/test-connection # validasi SMTP/IMAP/POP3 config

POST   /accounts/{id}/send            # kirim email (dukung attachments[] + headers{} custom)
GET    /accounts/{id}/messages        # fetch email, cache-first (query: folder, limit, offset, protocol, refresh)
POST   /accounts/{id}/check           # trigger cek email baru, return unread count
POST   /accounts/{id}/messages/read   # tandai 1 pesan sudah dibaca (IMAP only)
```

Semua request butuh header `X-API-Key`. Response format JSON konsisten dengan `{data, error}` envelope.

## 8. Security Considerations

- Credential (password/app-password) dienkripsi at-rest di SQLite (mis. AES-GCM dengan key dari environment variable, bukan hardcoded).
- **API key (MVP)**: satu key statis dari env var `XMAIL_API_KEY`, dibandingkan pakai constant-time compare (`crypto/subtle`) supaya tidak bocor lewat timing attack — **bukan** disimpan/di-hash di database, karena tidak pernah disimpan sama sekali (langsung dari env tiap request). Tabel `api_keys` ada di skema DB sebagai persiapan untuk **multi-key/rotatable API key** (key per-hash, bisa banyak, bisa di-revoke tanpa restart) — fitur ini masih backlog (lihat PLAN.md Fase 9), tabelnya belum dipakai. *(Revisi dari draf awal yang bilang "API key disimpan sebagai hash di tabel `api_keys`" — itu deskripsi rencana fitur multi-key yang belum diimplementasikan, ditulis seolah-olah sudah jadi behavior MVP. Ditemukan sebagai kontradiksi PRD↔kode saat code review, diperbaiki di sini alih-alih memaksakan implementasi hashing untuk 1 key statis yang justru tidak butuh di-hash karena tidak pernah disimpan.)*
- TLS certificate validation aktif secara default untuk koneksi TLS; opsi "non-TLS/insecure" harus eksplisit di-set per akun (bukan default), supaya tidak tidak sengaja kirim credential plaintext ke server yang salah.
- Tidak mencatat isi email atau credential ke log level info/debug.

## 9. Non-Functional Requirements

- Memory footprint target: idle < 30MB, di bawah beban moderat (puluhan koneksi IMAP/SMTP concurrent) tetap terkendali (goroutine-based, bukan thread-per-connection berat).
- Docker image target: < 30MB (multi-stage build, static binary di atas `distroless` atau `scratch`).
- Startup time: < 1 detik (cocok untuk restart cepat/orchestration sederhana).
- Deployment: single container, SQLite file di-mount sebagai volume agar data persist antar restart container.

## 10. Out of Scope / Kemungkinan Fase Berikutnya

- OAuth2 (Gmail/Outlook modern auth), push notification (IMAP IDLE / webhook), UI dashboard, multi-user auth (JWT/RBAC), migrasi storage ke Postgres kalau butuh scale multi-node, rate limiting/quota per akun, audit log terperinci.

## 11. Rough Roadmap

1. **Fase 1 — Core**: skema SQLite, CRUD akun, enkripsi credential, kirim email via SMTP (TLS + non-TLS).
2. **Fase 2 — Fetch**: fetch/check email via IMAP (primary) + POP3 (basic), cache metadata di SQLite.
3. **Fase 3 — MCP**: expose MCP server dengan 4 tools inti (list_accounts, send_email, fetch_emails, check_new_emails).
4. **Fase 4 — Packaging & Hardening**: Dockerfile multi-stage, dokumentasi API, test-connection endpoint, review security (enkripsi, TLS default).
5. **Fase 5 (opsional)** — MCP client capability, fitur tambahan sesuai kebutuhan nyata.

## 12. Open Questions

- App-password vs password biasa untuk provider seperti Gmail yang mewajibkan 2FA + app-password — perlu didokumentasikan per-provider saat implementasi test-connection.
- Skema enkripsi credential: key management sederhana (env var tunggal) cukup untuk MVP, atau butuh key rotation dari awal?
