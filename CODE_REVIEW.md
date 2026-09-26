# Code Review — xmail

Review dua-axis (Standards & Spec) atas seluruh isi worktree, dibandingkan dengan titik tetap `66ebca7` ("Init empty repo").

## Konteks & cakupan

- **Titik tetap (fixed point):** `66ebca7` — commit scaffold kosong.
- **Perintah diff:** `git diff 66ebca7...HEAD` → **kosong**. `git log 66ebca7..HEAD --oneline` juga kosong.
- **Realita yang di-review:** seluruh project belum di-commit. Selain `PRD.MD` dan `README.MD` (modified), semua file berstatus **untracked**, jadi tidak muncul di `git diff`. Cakupan review = seluruh working tree (55 file untracked + 2 file modified).
- **Sumber spec:** `PRD.MD` + `PLAN.md` (tidak ada issue tracker / `docs/agents/issue-tracker.md`, dan tidak ada referensi issue di commit mana pun — satu-satunya commit berpesan "Init empty repo").
- **Sumber standards:** `ARCHITECTURE.md` (§1 Core Design Principles, §5 How to Extend, §6 Build & Release, §7 Testing, §8 Known Gaps), `PLAN.md` (§0 Keputusan Teknis locked-in, §6 Testing Strategy), `CLAUDE.md`, plus **smell baseline** Fowler (selalu judgement call, bukan pelanggaran keras). Tidak ada `CONTRIBUTING.md` / `CODING_STANDARDS.md` / `AGENTS.md`.

## Standards

**Pelanggaran standard terdokumentasi (keras): tidak ada.** Yang terverifikasi dipatuhi:

- Hanya `internal/app/app.go` yang meng-import concrete package `mailer/{smtp,imap,pop3}`.
- `cmd/xmail` dan `cmd/xmail-tray` memanggil `app.Run` tanpa business logic.
- Config murni dari env var (`internal/config/config.go`).
- Hanya stdlib `testing` — tidak ada testify di `_test.go` mana pun.
- Build tag `//go:build windows && xmailtray` ada di `cmd/xmail-tray/main.go` + `internal/winservice/service.go`.
- Tidak ada `InsecureSkipVerify`.
- Log tidak membawa body email/kredensial (`internal/api/middleware.go`).
- Logic release ada di `scripts/release.{sh,ps1}`, Makefile hanya wrapper.
- `make test` bebas network.
- Skema MCP dideklarasikan eksplisit (tidak pakai `mcp.WithInputSchema[T]`).

**Divergensi (judgement call):** `PLAN.md` §0 mengunci "env var only … `.env` opsional untuk dev via `godotenv`", tapi tidak ada yang me-load `.env` (tidak ada import godotenv) — `.env.example` ada tetapi `go run ./cmd/xmail` tidak akan membacanya. Ini drift doc↔kode, bukan pelanggaran keras (klausulnya memang ditandai opsional).

**Baseline smell (semua judgement call):**

1. **Duplicated Code** — aritmetika window newest-first identik di dua file. `internal/mailer/imap/client.go` `Fetch`: `end := total - offset; …; start := end - limit + 1; if start < 1 { start = 1 }`; `internal/mailer/pop3/client.go` `Fetch` mengulanginya verbatim. → ekstrak satu helper.
2. **Duplicated Code** — default input (`"imap"`, `"INBOX"`, limit 20) diimplementasikan ulang tiga kali: `internal/api/messages_handler.go`, `internal/mcpserver/server.go`, dan lagi di `account.Service.FetchMessages`/`CheckNew`. Mendekati ARCHITECTURE §1 rule 3 / CLAUDE "no duplicated business logic", tapi masih level adapter → judgement.
3. **Data Clumps** — `(cfg ConnectionConfig, username, secret string)` mengalir lewat `TesterFactory`/`SMTPSenderFactory`/`IMAPFactory`/`POP3Factory` dan tiap `New`; `(accountID, protocol, folder)` mengalir lewat `ExistingUIDs`/`UpsertMessages`/`ListMessages`/`FetchMessages`/`CheckNew`. Masing-masing pantas jadi satu tipe.
4. **Primitive Obsession / Repeated Switches** — protocol bertipe `string` di-switch dua kali di `internal/account/service.go` (`TestConnection`, `resolveFetcher`) untuk memilih wiring per-protokol yang sama; satu tipe `Protocol` atau satu map bersama bisa meruntuhkan keduanya.
5. **Mysterious Name / magic value** — `Service.Update` mengarang `validateSecret = "unchanged"` semata-mata agar lolos cek non-empty di `Validate`; nama/nilainya menyesatkan maksud.
6. **Duplicated Code (minor)** — `connHost`/`connPort`/`connTLSMode` adalah tiga nil-guard berbentuk sama di `internal/account/repository.go`.

## Spec

Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...`, dan `-tags integration` semuanya hijau. Klaim jumlah test di `PLAN.md` terbukti persis (60 unit / 76 case integration `=== RUN`).

**(a) Requirement hilang / parsial**

1. **Custom headers — hilang.** `PRD.MD` §6.2: "Dukung attachment, HTML & plain text body, CC/BCC, **custom headers dasar**." `sendRequest` di `internal/api/send_handler.go` tidak punya field headers dan `internal/mailer/smtp/client.go` juga tidak men-set apa pun. Tidak ada catatan PLAN/ARCHITECTURE yang merekam penghilangan ini.
2. **IMAP "mark as read" — hilang.** `PRD.MD` §6.3: "IMAP: dukung folder selain INBOX, **mark as read**." Tidak ada endpoint, MCP tool, atau method `Service` yang men-set `\Seen`; `internal/mailer/imap/client.go` hanya membaca `FlagSeen`. Tidak ada pemanggilan Store di jalur produksi.
3. **Cache daftar attachment — parsial.** `PRD.MD` §6.3: "Body & metadata email (subject, from, to, date, **attachment list**) disimpan/cache di SQLite." Tabel `messages_cache` di `0001_init.sql` tidak punya kolom attachment; `mailer.Message` juga tidak membawanya.
4. **Peran MCP Client — hilang.** `PRD.MD` §3/§6.4: "MCP Client: kemampuan service untuk memanggil MCP server eksternal … (**disiapkan sebagai extensible interface**)". Tidak ada interface atau scaffold; hanya ditunda ke PLAN Fase 9.
5. **Hashing API key — bertentangan (deviasi tercatat).** `PRD.MD` §8: "API key disimpan sebagai hash (bukan plaintext) di tabel `api_keys`." Tabelnya tidak dipakai; `middleware.go` membandingkan plaintext `XMAIL_API_KEY` dari env dengan constant-time compare. PLAN Fase 1 sudah mencatat ini, tapi tetap bertentangan dengan PRD.

**(b) Scope creep (minor, sebagian besar sudah tercatat)**

`GET /healthz`, transport stdio `XMAIL_MCP_STDIO`, tray "Open dashboard", `scripts/release.ps1` — tidak ada di PRD §6.6/§7; healthz & stdio dicatat di PLAN.

**(c) Terimplementasi tapi salah**

6. **Message cache write-only di jalur baca.** `PRD.MD` §6.3 menginginkan caching "agar fetch berikutnya lebih cepat (bukan selalu round-trip penuh ke mail server)". `GET /accounts/{id}/messages` → `Service.FetchMessages` selalu dial ke server; `Repository.ListMessages` hanya dipanggil dari test — dead production code, jadi pembacaan tidak pernah menyentuh cache.
7. **POP3 `starttls` lolos validasi tapi tidak bisa dipakai.** `Validate` menerima `starttls` untuk pop3, tetapi `connect()` di `pop3/client.go` mengembalikan "not supported". Akibatnya `POST /accounts` mengembalikan 201 untuk konfigurasi yang setiap operasi POP3-nya kemudian gagal. PLAN mencatat error di sisi client, bukan celah validasinya.
8. **DELETE melanggar kontrak envelope.** `PLAN.md` §3: "Semua response: `{data, error}`" — `handleAccountDelete` mengembalikan 204 tanpa body.

**(parsial)** `PRD.MD` §6.1 "test-connection … **sebelum disimpan permanen**": endpoint-nya butuh `{id}` yang sudah ada, jadi validasi terjadi setelah save (sketsa PRD §7 sendiri juga mengandung ketegangan yang sama).

## Ringkasan

- **Standards** — 7 temuan (0 keras, 7 judgement). Terburuk: aritmetika window newest-first yang terduplikasi di `imap/client.go` + `pop3/client.go`, plus `protocol` bertipe `string` yang di-switch dua kali di `service.go`.
- **Spec** — 10 temuan (5 hilang/parsial, 1 bertentangan-tapi-tercatat, 3 terimplementasi-tapi-salah, 1 parsial). Terburuk: message cache tidak pernah dibaca di jalur fetch, sehingga requirement caching `PRD.MD` §6.3 sebenarnya belum terpenuhi dan `ListMessages` menjadi dead production code.

Kedua axis sengaja tidak digabung/di-rerank — satu axis bisa lolos sementara axis lain gagal, dan meranking silang akan menutupi hal itu.

---

# Verifikasi Ulang (putaran 2 — setelah perbaikan)

Perubahan yang direview: 20 file source + 2 file baru (`internal/mcpserver/client.go`, `internal/storage/migrations/0002_message_attachments.sql`), plus `PRD.MD`, `PLAN.md`, `ARCHITECTURE.md`, `README.MD`.

**Baseline teknis:** `go build ./...`, `go vet ./...`, `go build -tags xmailtray ./...`, `go vet -tags xmailtray ./...` semuanya bersih; `go test ./...` hijau; `go test -tags integration ./...` hijau. Klaim jumlah test di `PLAN.md` §10 (68 unit / 87 integration) cocok dengan hasil pengukuran.

**Cara verifikasi:** kedua axis diverifikasi ulang oleh sub-agent terpisah terhadap daftar temuan putaran 1; temuan headline (bug urutan cache) dikonfirmasi sendiri langsung dari kode sumber, bukan hanya dari laporan agent.

## Standards — status per temuan

| # | Temuan | Status |
|---|---|---|
| 1 | Drift `.env`/`godotenv` — `PLAN.md` §0 mengunci "`.env` opsional via `godotenv`", tapi tidak ada yang me-load `.env` | **Masih terbuka** — `go.mod` tanpa `godotenv`, tidak ada loader di kode; `README.MD` masih menyuruh `cp .env.example .env` lalu `make run` (yang = `go run ./cmd/xmail`, tidak membaca file itu). Baik kode maupun dokumen belum direkonsiliasi |
| 2 | Duplikasi aritmetika window newest-first (`imap/client.go` + `pop3/client.go`) | **Fixed** — diekstrak jadi `mailer.WindowRange` (`internal/mailer/types.go`), dipakai kedua client |
| 3 | Duplikasi default input (`"imap"`, `"INBOX"`, 20) di 3 tempat | **Fixed** — `account.DefaultProtocol` / `account.DefaultFetchLimit` jadi sumber tunggal. Sisa minor: literal `"INBOX"` masih diulang di `imap/client.go`, `pop3/client.go`, dan `service.go` |
| 4 | Data Clumps — `(cfg, username, secret)`, `(accountID, protocol, folder)` | **Masih terbuka (sengaja)** — didokumentasikan sebagai keputusan di `PLAN.md` §10.4 #11 |
| 5 | Primitive Obsession / Repeated Switches pada `protocol string` | **Sebagian** — pemilihan config disatukan ke `connConfigForProtocol`; pemilihan *factory* masih dua switch terpisah (`TestConnection`, `resolveFetcher`). Didokumentasikan sebagai sengaja di `PLAN.md` §10.4 #12 |
| 6 | `validateSecret = "unchanged"` (magic value di `Service.Update`) | **Fixed** — `Validate(a, requireSecret bool, secret string)`; `Update` mengirim `secret != nil` |
| 7 | `connHost`/`connPort`/`connTLSMode` — 3 nil-guard seragam di `repository.go` | **Fixed** — jadi satu `connFields(...)` |

## Spec — status per temuan

| # | Temuan | Status |
|---|---|---|
| 1 | Custom headers (PRD §6.2) | **Fixed** — `sendRequest.Headers` → `mailer.OutgoingMessage.Headers` → `SetGenHeaderPreformatted`; schema MCP `send_email` dapat `headers`; ada test. Teks PRD §6.2 tidak diubah |
| 2 | IMAP "mark as read" (PRD §6.3) | **Fixed** — `mailer.Marker`, `imap.Client.MarkRead` (STORE +FLAGS `\Seen` by UID), `Service.MarkRead`, `Repository.MarkMessageRead`, route `POST /accounts/{id}/messages/read`, unit + integration test. POP3 mengembalikan validation error (benar). Belum ada MCP tool — memang tidak diwajibkan PRD §6.4 |
| 3 | Cache daftar attachment (PRD §6.3) | **Fixed** — `Message.Attachments`, IMAP `BODYSTRUCTURE{Extended:true}`, migrasi `0002_message_attachments.sql` (kolom `attachments TEXT`), `encode/decodeAttachments`, integration test. POP3 tetap `nil` (protokolnya tidak melaporkan attachment) |
| 4 | Peran MCP Client (PRD §3/§6.4) | **Sebagian** — `internal/mcpserver/client.go` hanya interface: tanpa implementasi, belum di-wire ke `Service`/`app`, tanpa test. Memenuhi bunyi literal "disiapkan sebagai extensible interface", tapi `ARCHITECTURE.md` §8 menghitungnya sebagai requirement yang "sudah diimplementasikan" → **overstated** untuk sebuah stub |
| 5 | Hashing API key (PRD §8) | **Selesai dengan mengubah spec** — `PRD.MD` §8 ditulis ulang. Perubahan diberi label eksplisit dan beralasan (key statis dari env tidak pernah disimpan, jadi tidak perlu di-hash; tabel `api_keys` dijelaskan sebagai persiapan multi-key yang masih backlog). Satu-satunya instance reword-to-match-code, dan dilakukan secara transparan — bukan penyelundupan |
| 6 | Message cache write-only di jalur baca (PRD §6.3) | **Fixed, tapi memunculkan bug baru** — `FetchMessages` kini cache-first + param `refresh`, dibuktikan lewat penghitung fetch call di level Service dan REST. Lihat "Temuan baru" #1 |
| 7 | POP3 `starttls` lolos validasi tapi unusable | **Fixed** — `Validate` menolak `pop3`+`starttls` saat save, bukan hanya gagal saat dipakai (`TestValidate/pop3_starttls_rejected` mengunci perilaku ini) |
| 8 | DELETE melanggar kontrak envelope (`{data,error}`) | **Fixed** — `handleAccountDelete` mengembalikan `200 {"deleted":true}`; di-assert `TestAccountsCRUD_EndToEnd` |
| 9 | `test-connection` sebelum disimpan permanen (PRD §6.1) | **Masih terbuka** — endpoint tetap butuh `{id}` yang sudah ada, tidak ada jalur validate-before-save; teks PRD §6.1 belum berubah dan belum ada catatan deviasi di `PLAN.md` |
| 10 | Scope creep tidak tercatat | **Fixed (sebagian)** — `healthz`, stdio `XMAIL_MCP_STDIO`, tray "Open dashboard", `release.ps1` kini tercatat di `PLAN.md` (§3, Fase 5, Fase 7, Fase 8) + `ARCHITECTURE.md` §6 + README. `PRD.MD` sendiri masih belum: §7 belum memuat `/healthz`, §6.6 masih menyebut satu script rilis saja |

## Temuan baru

1. **🔴 BUG: urutan hasil cache terbalik (belum ada test).** `ListMessages` memakai `ORDER BY fetched_at DESC, rowid DESC`, tetapi `UpsertMessages` memberi **satu** `now` yang sama untuk seluruh batch — sehingga `fetched_at` identik untuk semua baris dan pengurutan jatuh ke `rowid DESC`. Karena `Fetch` menyisipkan newest-first (pesan terbaru = rowid terkecil), `rowid DESC` justru membalik urutannya. Dikonfirmasi langsung dari `internal/account/messages.go` (`ListMessages` + `UpsertMessages`).
   **Efek:** `GET /accounts/{id}/messages` mengembalikan **newest-first saat cache kosong**, lalu **oldest-first setelah cache terisi** — endpoint yang sama, urutan terbalik, dan paginasi (`offset`) berjalan ke arah yang salah. Berlaku juga untuk tool MCP `fetch_emails`. `TestRepository_UpsertAndListMessages` hanya meng-assert `len`, tidak pernah urutan, jadi bug ini lolos. Catatan tambahan: `fetched_at` beresolusi detik, jadi dua batch berbeda dalam detik yang sama juga bisa saling bertabrakan.
   **Saran:** urutkan lewat kunci yang monotonik (minimal `rowid ASC` untuk batch yang sama, lebih aman lagi kolom sequence/`AUTOINCREMENT` sendiri), lalu tambah test yang meng-assert urutan dengan >1 baris di jalur cache.
2. **Speculative Generality (baru):** `ExternalToolCaller` di `internal/mcpserver/client.go` tanpa implementasi dan tanpa pemanggil — abstraksi mati. Memang scaffolding yang diminta PRD, tapi tetap smell judgement.
3. **Duplicated Code (baru, minor):** guard `if protocol == "" { protocol = DefaultProtocol }` / `if folder == "" { folder = "INBOX" }` kini disalin ke 4 method (`TestConnection`, `FetchMessages`, `CheckNew`, `MarkRead`) — perbaikan finding #3 malah memunculkan ulang dua baris yang sama di banyak tempat.
4. **Doc drift:** `PLAN.md` §10 menyebut "68 unit (naik dari 64)" sementara baseline review terdokumentasi 60; angka per-fase di §5 masih basi (40/48/53/60). `PRD.MD` §7 belum menambah `/messages/read`, `refresh`, `headers`; §6.5 belum menambah kolom `attachments`; §6.6 masih menyebut satu script rilis saja.
5. **Minor:** test storage belum meng-assert kolom `attachments` yang baru.

## Ringkasan putaran 2

- **Standards** — 7 temuan: **4 fixed**, 1 sebagian, 2 masih terbuka (1 di antaranya sengaja + terdokumentasi). 0 pelanggaran keras; tidak ada pelanggaran konvensi terdokumentasi. Ditambah 1 smell spekulatif baru.
- **Spec** — 10 temuan: **7 fixed**, 1 sebagian, 1 masih terbuka, 1 selesai dengan mengubah spec (transparan). Ditambah **1 bug baru yang serius** (urutan cache), 1 duplikasi minor, dan drift dokumen.

Terburuk sekarang: **bug urutan cache.** Perbaikan finding #6 memenuhi *huruf* requirement PRD §6.3 ("fetch berikutnya lebih cepat") tetapi merusak jaminan urutan newest-first yang berlaku di jalur live — dan tidak ada test yang menangkapnya.

---

# Verifikasi Ulang (putaran 3 — setelah perbaikan putaran 2)

Perubahan: `internal/account/messages.go`, `internal/account/messages_test.go`, `internal/account/service.go`, migrasi baru `0003_message_sort_rank.sql`, plus `PRD.MD` / `PLAN.md` / `ARCHITECTURE.md`. `internal/mcpserver/client.go` **dihapus**.

**Baseline teknis:** `go build`/`go vet` (termasuk `-tags xmailtray`) bersih; `go test ./...` = **69 hijau**; `go test -tags integration ./...` = **88 hijau**. Klaim `PLAN.md` §10.5 (69/88) cocok persis dengan hasil pengukuran ulang.

## Status temuan baru putaran 2

| # | Temuan | Status |
|---|---|---|
| 1 | 🔴 Urutan hasil cache terbalik | **Fixed** — kolom `sort_rank` (migrasi `0003`, `INTEGER NOT NULL DEFAULT 0`) diisi eksplisit `len(msgs)-i` di `UpsertMessages` (newest = rank tertinggi), pengurutan jadi `ORDER BY fetched_at DESC, sort_rank DESC`. Diverifikasi langsung dari kode: logikanya benar, dan tidak lagi bergantung pada rowid/urutan insert. Regression test `TestRepository_UpsertMessages_PreservesOrder` meng-assert urutan `[3,2,1]` di cache dingin **dan** setelah re-upsert batch yang sama — kasus kedua penting karena `ON CONFLICT DO UPDATE` mempertahankan rowid lama, jadi fix yang mengandalkan "urutan insert" saja tidak akan cukup. Test lama (`TestRepository_UpsertAndListMessages`) juga diperkuat: dari cuma cek `len` jadi cek urutan |
| 2 | Speculative Generality — `ExternalToolCaller` tanpa implementasi & pemanggil | **Fixed (dihapus)** — `internal/mcpserver/client.go` dibuang seluruhnya, bukan dipertahankan sebagai stub; `PRD.MD` §6.4 direvisi (MCP Client jadi backlog penuh, termasuk desain interface-nya) dengan alasan yang diberi label eksplisit. `ARCHITECTURE.md` §8 juga sudah mencatat penghapusan ini. Pilihan yang benar: interface spekulatif tanpa consumer memang cuma tebakan bentuk API |
| 3 | Guard default (`protocol`/`folder`) terduplikasi di 4 method | **Fixed (sebagian)** — guard `protocol` beres: `resolveFetcher` sekarang mengembalikan `resolvedProtocol` yang sudah di-default, dipakai `FetchMessages`/`CheckNew`/`MarkRead`; sisa 2 guard (`TestConnection` + `resolveFetcher`) memang jalur kode berbeda. Sekalian dibersihkan: `resolveFetcher` tidak lagi mengembalikan `Account`/`secret` yang tidak pernah dipakai caller. **Sisa:** guard `folder == "" → "INBOX"` masih terduplikasi 3× — lihat "Residual" di bawah |
| 4 | Doc drift `PRD.MD` §6.5/§7 | **Fixed** — §6.5 sekarang mengacu ke file migrasi asli (`0001`/`0002`/`0003`) dan menyebut kolom `attachments` + `sort_rank`; §7 sudah memuat `POST /accounts/{id}/messages/read`, query `refresh`, dan `headers{}` |

## Residual (bukan regresi — masih terbuka dari putaran sebelumnya)

- **Drift `.env`/`godotenv`** — `PLAN.md` §0 masih mengunci "`.env` opsional via `godotenv`", `README.MD` masih menyuruh `cp .env.example .env` lalu `make run`, tetapi tidak ada `godotenv` di `go.mod` maupun loader di kode. Belum direkonsiliasi.
- **Data Clumps** `(cfg, username, secret)` / `(accountID, protocol, folder)` — sengaja tidak di-refactor, didokumentasikan (`PLAN.md` §10.4 #11).
- **Duplikasi switch pemilihan factory** di `TestConnection` vs `resolveFetcher` — sengaja dibiarkan sebagian, didokumentasikan (§10.4 #12).
- **Guard `folder == "" → "INBOX"` masih terduplikasi 3×** (`FetchMessages`, `CheckNew`, `MarkRead`). Putaran ini memperbaiki bagian `protocol`-nya saja; `PLAN.md` §10.5 #17 hanya menyebut guard `protocol`. Bagian `folder` belum tersentuh **dan** belum didokumentasikan sebagai sengaja dibiarkan.
- **`test-connection` sebelum disimpan permanen** (PRD §6.1) — masih terbuka: endpoint tetap butuh `{id}` yang sudah ada, teks PRD belum berubah, belum ada catatan deviasi.
- **`PRD.MD` §7 belum memuat `GET /healthz`**, dan §6.6 masih menyebut build "lewat satu script, `scripts/release.sh`" padahal `scripts/release.ps1` sudah ada dan dipakai (README/PLAN/ARCHITECTURE sudah benar).

## Catatan minor atas fix putaran ini

- **`fetched_at` beresolusi detik dipakai sebagai kunci urut primer.** Dua batch berbeda untuk folder yang sama dalam detik yang sama akan tetap tie, lalu jatuh ke `sort_rank` yang cakupannya per-batch — bisa saling menyisipkan. Lebih kuat kalau kunci primer diganti penanda batch yang benar-benar monotonik (bukan timestamp), atau resolusinya dinaikkan.
- **Migrasi `0003` memberi `DEFAULT 0` untuk baris lama**, jadi cache yang sudah terisi sebelum upgrade punya `sort_rank = 0` semua → urutan sembarang sampai ada live refresh berikutnya. Sekali saja dan berdampak rendah, tapi relevan kalau ada DB yang di-upgrade in-place.

## Ringkasan putaran 3

- Temuan baru putaran 2 (4 item): **3 fixed penuh, 1 fixed sebagian**.
- Akumulasi sejak awal: putaran 1 (17 temuan → 11 fixed, 2 sebagian, 2 terbuka, 1 selesai via perubahan spec) + putaran 2 (4 temuan → 3 fixed, 1 sebagian). **Tidak ada lagi bug perilaku yang diketahui terbuka**; semua yang tersisa bertipe keputusan sadar/dokumentasi atau drift dokumen, bukan cacat fungsional.
- Sisa yang paling layak dikerjakan: guard `folder = "INBOX"` yang masih terduplikasi 3× (kecil, tapi persis jenis regresi yang baru saja dibereskan) dan drift `.env`/`godotenv` yang belum direkonsiliasi.

---

# Verifikasi Ulang (putaran 4 — review dua-axis baru)

Review dua-axis penuh atas **seluruh isi worktree** (bukan cuma delta putaran 3), dijalankan lagi dari nol terhadap titik tetap `66ebca7`. Kedua axis dikerjakan sub-agent terpisah.

## Konteks & cakupan

- **Titik tetap:** `66ebca7` ("Init empty repo"). `git diff 66ebca7...HEAD` **kosong**, `git log 66ebca7..HEAD --oneline` **kosong** — 66ebca7 satu-satunya commit.
- **Cakupan nyata:** seluruh working tree (60 file, ~7.320 baris inserasi). File untracked dijadikan terlihat lewat `git add -N .` (index dikembalikan setelah review).
- **Sumber spec:** `PRD.MD` + `PLAN.md` (tidak ada `docs/agents/issue-tracker.md`, tidak ada referensi issue di commit).
- **Sumber standards:** `ARCHITECTURE.md`, `PLAN.md`, `CLAUDE.md`, `.commandcode/taste/*.md`, plus smell baseline Fowler.
- **Baseline teknis:** `go build ./...`, `go vet ./...`, `go vet -tags xmailtray ./...` bersih; `go test ./...` dan `go test -tags integration ./...` hijau.
- **Sikap:** arsip putaran 1-3 **tidak** dipercaya begitu saja; setiap klaim "fixed"/"sengaja" diverifikasi ulang ke kode. Kedua axis menemukan **drift `.env`/`godotenv` secara independen**, jadi temuan itu saling terkonfirmasi.

## Standards

**Pelanggaran standard terdokumentasi (keras): 3.**

1. **Loader `.env` tidak ada.** `PLAN.md` §0 mengunci "`.env` opsional … via `godotenv`". `go.mod` tanpa `godotenv`, dan tidak ada loader di kode — `config.Load` (`internal/config/config.go:52`) cuma `os.Getenv`. Alur dev di `README.MD` (`cp .env.example .env` lalu `make run` = `go run ./cmd/xmail`) **tidak akan membaca file itu** — workflow rusak diam-diam. Masih terbuka sejak putaran 3.
2. **Windows Service hasil install tidak akan pernah bisa start.** `internal/winservice/service.go:60-68` (`New`) menyusun `service.Config` **tanpa `EnvVars`**, sehingga proses yang di-launch SCM tidak melihat `XMAIL_API_KEY`/`XMAIL_ENCRYPTION_KEY`; `cmd/xmail-tray/main.go:45` lalu `log.Fatalf`. Kapabilitas ini didokumentasikan di `README.MD` §3 / `ARCHITECTURE.md` §6. (**Terburuk di axis ini.**)
3. **Dokumen tidak sinkron** (taste: "semua doc relevan di-update saat fitur/target berubah"). `PRD.MD` §7 masih belum memuat `GET /healthz`; `PRD.MD` §6.6 masih menyebut satu script rilis padahal `scripts/release.ps1` sudah ada (README/PLAN/ARCHITECTURE sudah benar).

**Baseline smell (semua judgement call): 4.**

4. **Duplicated Code** — guard + literal `folder == "" → "INBOX"` terulang **7×**: `internal/account/service.go:338,373,414`, `internal/mailer/imap/client.go:89,148,171`, `internal/mailer/pop3/client.go:110`. Putaran 3 mengakui 3× di `service.go` belum dibersihkan, dan **melewatkan** 3 kemunculan di `imap/client.go`. → satu default bersama.
5. **Speculative Generality / dead code** — `queryOr` (`internal/api/messages_handler.go:10`) tidak punya pemanggil; `handleMessagesList` memakai `r.URL.Query().Get` langsung (baris 40-41).
6. **Primitive Obsession / Duplicated** — `const timeFormat = "2006-01-02T15:04:05Z07:00"` (`internal/api/dto.go:80`) menulis ulang `time.RFC3339`, yang di tempat lain (`account/messages.go` dan kedua client) dipakai langsung.
7. **Duplicated source versi** — `serverVersion = "0.1.0"` di-hardcode (`internal/mcpserver/server.go:21`), tidak terhubung ke stamping `-X main.version` (`ARCHITECTURE.md` §6) → MCP akan melaporkan `0.1.0` selamanya.

**Disuppress** (keputusan repo terdokumentasi mengalahkan baseline): Repeated Switches pada `protocol string` (`connConfigForProtocol`/`TestConnection`/`resolveFetcher`) — `PLAN.md` §10.4 #12; Data Clumps `(cfg,username,secret)` / `(accountID,protocol,folder)` — `PLAN.md` §10.4 #11. Klaim `PLAN.md` §10.5 #17 bahwa guard `protocol` sudah dideduplikasi **benar** (`resolveFetcher` mengembalikan `resolvedProtocol`, dipakai `FetchMessages`/`CheckNew`/`MarkRead`).

## Spec

**(a) Requirement hilang / parsial: 4.**

1. **Peran MCP Client — hilang.** `PRD.MD:77`/`:23` "mampu berperan sebagai MCP Client". Tidak ada kode: `internal/mcpserver/` cuma `server.go` (tanpa `client.go`). Teks PRD §6.4 sudah diubah jadi menyatakan ini backlog, jadi *teks requirement*-nya kini cocok — tapi kapabilitasnya absen.
2. **`test-connection` "sebelum disimpan permanen" — parsial.** `PRD.MD:57`. Route `POST /accounts/{id}/test-connection` (`internal/api/server.go:66`) butuh id yang sudah ada; `Service.TestConnection` memuat akun via `repo.Get`. Tidak bisa validasi sebelum save — harus create dulu. Tidak ada catatan PLAN.
3. **Caching body email — parsial.** `PRD.MD:69` "Body & metadata email (subject, from, to, date, attachment list) disimpan/cache". Yang di-cache cuma metadata + **nama** attachment; tidak ada kolom body di `migrations/0001`/`0002`.
4. **MCP `send_email` membuang cc/bcc/attachments.** `PRD.MD:62` mewajibkan ini; `sendEmailArgs` (`internal/mcpserver/server.go:145-152`) tidak punya field-nya, padahal REST (`internal/api/send_handler.go`) punya. Bertentangan dengan premis repo "REST dan MCP identik" (`CLAUDE.md`).

**(b) Scope creep (tidak diminta; baru sebagian direkonsiliasi): 2.**

5. **Script rilis kedua.** `PRD.MD:98` "lewat **satu** script, `scripts/release.sh`" — repo menambah `scripts/release.ps1` (ARCHITECTURE/README diperbarui, PRD tidak).
6. **`/healthz` tanpa auth.** `PRD.MD:116` "**Semua** request butuh header `X-API-Key`"; `internal/api/middleware.go:19` mengecualikan `/healthz` (route `server.go:44`). Tercatat di PLAN §5 tapi bertentangan dengan PRD §7.

**(c) Terimplementasi tapi salah / drift doc↔kode: 2.**

7. **`.env`/`godotenv` mati.** `PLAN.md:20` mengunci "`.env` opsional … via `godotenv`"; tidak ada `godotenv` di `go.mod` dan `internal/config/config.go:38` cuma `os.Getenv`. `README.MD:56` menyuruh `cp .env.example .env && make run` — langkah setup yang tidak berfungsi. (**Terburuk di axis ini.**)
8. **Klaim PLAN basi.** `PLAN.md:427` (§10.2 #7) masih mencantumkan `ExternalToolCaller` sebagai requirement yang terimplementasi; §10.5 #16 mencatat penghapusannya — `internal/mcpserver/client.go` memang tidak ada. Tabel arsipnya masih terbaca seolah "sudah jadi".

## Ringkasan

- **Standards** — **7 temuan: 3 keras, 4 judgement.** Terburuk: Windows Service hasil install tidak diberi env var, jadi `XMAIL_*` absen dan proses keluar saat startup (`internal/winservice/service.go:60`, `cmd/xmail-tray/main.go:45`).
- **Spec** — **8 temuan: 4 hilang/parsial, 2 scope-creep, 2 drift doc/kode.** Terburuk: `.env`/`godotenv` didokumentasikan sebagai jalur config yang berfungsi tetapi tidak ada kodenya (`internal/config/config.go:38`, `README.MD:56`).

Kedua axis sengaja tidak digabung/di-rerank. Drift `.env`/`godotenv` ditemukan **independen di kedua axis**, jadi ini yang paling layak digarap berikutnya.

---

# Verifikasi Ulang (setelah perbaikan temuan putaran 4)

Verifikasi langsung ke kode (bukan percaya klaim dokumen) atas perbaikan yang tercatat di `PLAN.md` §10.6. Titik tetap `66ebca7` tidak berubah.

**Baseline teknis (diukur ulang, semua hijau):** `go build`/`go vet` (default dan `-tags xmailtray`) bersih; `go test ./...` = **71 `=== RUN`**; `go test -tags integration ./...` = **90**; `go test -tags xmailtray ./internal/winservice/` = **1**. Ketiganya cocok persis dengan klaim `PLAN.md` §10.6 — tidak ada angka yang di-overstate.

## Standards — status per temuan

| # | Temuan | Status |
|---|---|---|
| 1 | Loader `.env` tidak ada | **Fixed** — `github.com/joho/godotenv` di `go.mod`, `config.Load` memanggil `godotenv.Load()` (best-effort, tidak override env asli). Dikunci `TestLoad_ReadsDotEnv` + `TestLoad_RealEnvOverridesDotEnv`; `README.MD`/`ARCHITECTURE.md` ikut diperbarui |
| 2 | Windows Service tanpa `EnvVars` | **Fixed** — `buildServiceConfig` mengisi `EnvVars` (re-encode `EncryptionKey` ke base64); regression test `TestBuildServiceConfig_EnvVarsRoundTrip` mensimulasikan proses SCM ber-environment kosong. **Tapi lihat temuan baru A** |
| 3 | `PRD.MD` §7/§6.6 tidak sinkron | **Fixed** — §7 sudah memuat `GET /healthz`; §6.6 sudah "dua script setara" |
| 4 | Guard `folder == ""` terduplikasi | **Fixed** — `mailer.DefaultFolder` di `internal/mailer/types.go`, dipakai 6 tempat. Sisa literal `"INBOX"` di `pop3/client.go` sah (POP3 memang tanpa folder) |
| 5 | `queryOr` dead code | **Fixed** — dihapus; `queryIntOr`/`queryBool` dipakai |
| 6 | `timeFormat` menulis ulang `time.RFC3339` | **Fixed** — `dto.go` langsung `time.RFC3339` |
| 7 | `serverVersion` hardcoded | **Fixed** — `version` di-thread `cmd/*` → `app.Run(ctx,cfg,version)` → `mcpserver.New(svc,version)` (dan `winservice.New`) |

## Spec — status per temuan

| # | Temuan | Status |
|---|---|---|
| 1 | MCP Client role | **Backlog (didokumentasikan)** — PRD §6.4 + §3 menyatakan backlog penuh, tanpa stub. Klaim §10.2 #7 sudah dicoret & diarahkan ke §10.5 #16 |
| 2 | `test-connection` sebelum save | **Didokumentasikan** — catatan ketegangan desain di PRD §6.1 + alasan tidak diubah. Klaim "sebelum disimpan permanen" dikoreksi |
| 3 | Caching body email | **Spec diklarifikasi** — PRD §6.3 jadi "**Metadata email**", eksplisit body tidak di-cache. Spec kini match kode |
| 4 | MCP `send_email` buang cc/bcc | **Fixed** — `sendEmailArgs.CC/BCC` + schema + diteruskan ke `OutgoingMessage`, di-assert `TestHandleSendEmail` (`server_test.go:109-129`). Attachments tetap REST-only, sengaja & didokumentasikan (PLAN §10.6 #22, kode, skill) |
| 5 | Scope creep `release.ps1` | **Fixed** — PRD §6.6 diperbarui |
| 6 | `/healthz` tanpa auth | **Fixed** — PRD §7 kini mencantumkan `/healthz` + "tanpa auth" |
| 7 | `.env`/`godotenv` mati | **Fixed** — sama dengan Standards #1 |
| 8 | `PLAN.md` §10.2 #7 klaim basi | **Fixed** — baris dicoret, diarahkan ke §10.5 #16 |

## Temuan baru / sisa

1. **(Latent, sedang) Fix `EnvVars` itu perlu tapi belum cukup untuk service yang benar-benar jalan.** `buildServiceConfig` meneruskan `XMAIL_DB_PATH` apa adanya, dan default-nya relatif (`xmail.db`, lihat `.env.example:2`). Windows Service Control Manager meluncurkan service dengan working directory `%SystemRoot%\System32`, jadi service terinstall dengan path default akan mencoba membuat/Membuka `xmail.db` di `System32` — kemungkinan gagal (`Access denied`) atau memakai DB yang berbeda dari tray. `service.Config.WorkingDirectory` **tidak bisa** jadi penambal: `go doc github.com/kardianos/service.Config` menyatakan field itu *"not supported on Windows"*. Remedinya: absolutkan `DBPath` saat menulis `EnvVars` (atau dokumentasikan bahwa `XMAIL_DB_PATH` wajib absolut untuk mode service). Test baru belum menangkap ini (hanya round-trip 4 var, tidak memeriksa lokasi file). Tidak bisa diverifikasi runtime di sesi ini (install service butuh Administrator).
2. **(Minor) `XMAIL_MCP_STDIO` tidak dipropagasi.** `EnvVars` hanya memuat 4 var; service yang di-install dari sesi tray dengan `XMAIL_MCP_STDIO=true` akan diam-diam kehilangan setelan itu.
3. **(Minor, doc) Penomoran putaran review tidak konsisten.** `README.MD` menulis "**3 putaran** code review ... total 25 temuan" dan `PLAN.md` §10.6 berjudul "Putaran **ketiga**" — padahal `CODE_REVIEW.md` sekarang punya **4** seksi review (initial, putaran 2, putaran 3, putaran 4), dan temuan putaran-4 itulah yang di `PLAN.md` dilabeli "putaran ketiga". Seksi "putaran 3" yang lama dan "putaran 4" menyebut putaran yang berbeda dari hitungan PLAN. Perlu satu konvensi (dan `README.MD` sekarang basi karena review ke-4 nyata).
4. **(Minor, doc) `ARCHITECTURE.md` §8 paragraf pembuka** masih meringkas "dua putaran review" saja (baris 222) walau §10.6/#19 sudah merinci putaran ketiga di baris 225.

## Ringkasan

- **Standards:** 7/7 temuan putaran 4 **fixed** (terverifikasi di kode + test). **Spec:** 8/8 **ditutup** (5 fixed di kode/dokumen, 2 didokumentasikan sebagai backlog/ketegangan desain, 1 diklarifikasi di spec agar match kode). Tidak ada regresi fungsional.
- Perbaikan-perbaikan ini memperkenalkan **1 temuan nyata baru** (path DB relatif pada Windows Service, #A) + 3 minor (1 fungsional kecil, 2 drift dokumen).
- Terburuk: **#A** — perbaikan `EnvVars` sudah benar arahnya, tetapi service terinstall dengan konfigurasi default masih bisa gagal start karena `XMAIL_DB_PATH` relatif terhadap `System32`.
