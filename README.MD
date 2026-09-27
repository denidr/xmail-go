# xmail

Email-as-a-service backend: kirim & terima email lewat REST API dan MCP, multi-akun, TLS & non-TLS, storage SQLite.

Dokumen lengkap:
- [PRD.MD](./PRD.MD) — requirement produk, scope, non-goals.
- [PLAN.md](./PLAN.md) — rencana implementasi teknis: struktur project, skema DB, kontrak API/MCP, urutan kerja per fase, histori keputusan.
- [PLAN-DASHBOARD.md](./PLAN-DASHBOARD.md) — rencana terpisah untuk fitur dashboard web manajemen akun (CRUD akun + uji konektivitas; PRD.MD §6.7).
- [PLAN-FOLDERS.md](./PLAN-FOLDERS.md) — rencana terpisah untuk daftar folder IMAP (`GET /accounts/{id}/folders`, tool MCP `list_folders`) dan klasifikasi error folder (500 → 400).
- [ARCHITECTURE.md](./ARCHITECTURE.md) — **referensi teknis codebase saat ini**: apa isinya, bagaimana kerjanya, dan cara menambah fitur. Ditulis supaya cepat dipahami manusia maupun LLM agent yang akan mengerjakan repo ini — mulai dari sini kalau mau langsung ubah/tambah kode.
- [CODE_REVIEW.md](./CODE_REVIEW.md) — arsip hasil code review independen; resolusinya (fix apa untuk temuan apa) dicatat di PLAN.md §10.
- [CONTEXT.md](./CONTEXT.md) — glosarium domain: istilah inti (Account, Message, Message cache, Coverage, Exhausted, Fetch window, dst) beserta istilah yang dihindari.
- [docs/adr/](./docs/adr/) — Architecture Decision Records: keputusan arsitektur beserta alasannya (mulai dari ADR-0001 soal penyatuan dispatch protokol).
- [docs/API.md](./docs/API.md) — **referensi REST API**: base URL & auth, envelope response, semua endpoint (request/response), kode error, dan contoh `curl`.
- [docs/MCP_CLIENT_GUIDE.md](./docs/MCP_CLIENT_GUIDE.md) — **panduan MCP client**: cara menyambungkan Claude Code/Claude Desktop/custom client via Streamable HTTP atau stdio, referensi 5 tool, dan troubleshooting.
- [docs/postman/](./docs/postman/) — koleksi Postman siap import (`xmail.postman_collection.json` + `xmail.postman_environment.json`).

Status saat ini: **semua fase (1-8) selesai & diverifikasi nyata** — core, SMTP, IMAP, POP3, MCP, hardening review, Windows tray+service, dan ketiga target rilis (Docker x64, Docker Armbian/arm64, Windows x64) sudah dibuild, dijalankan, dan di-smoke-test langsung (bukan cuma lolos compile). Repo juga sudah melalui beberapa putaran review independen (`CODE_REVIEW.md` — arsip lengkap tiap putaran; ringkasan resolusi per temuan di PLAN.md §10). Salah satu putaran menemukan 3 pelanggaran keras (terburuk: Windows Service yang di-install tidak akan pernah bisa start) — semuanya diperbaiki dengan regression test, bukan sekadar dicatat. Lihat PLAN.md §5 untuk detail tiap fase. Di luar fase inti itu, **Fase 10** (daftar folder IMAP: `GET /accounts/{id}/folders` + tool MCP `list_folders`, plus klasifikasi error folder 500→400) juga sudah selesai — lihat [PLAN-FOLDERS.md](./PLAN-FOLDERS.md).

## Fitur

- Multi-akun email dalam satu instance.
- Kirim email via SMTP (TLS implicit, STARTTLS, atau non-TLS), dukung attachment & custom header (`X-Priority`, dll).
- Fetch & cek email via IMAP (folder, unread count, attachment list, mark-as-read) dan POP3 (fetch sederhana).
- Daftar folder IMAP (`GET /accounts/{id}/folders` / tool MCP `list_folders`) — tahu nama folder yang benar beserta atribut perannya (`\Sent`, `\Drafts`, …), tanpa menebak.
- Fetch email **cache-first** — request pertama dial ke server & simpan ke SQLite, request berikutnya langsung dari cache (parameter `refresh=true` buat paksa ambil ulang dari server).
- Kredensial akun dienkripsi (AES-256-GCM) sebelum disimpan di SQLite.
- Expose operasi email sebagai MCP tools (`list_accounts`, `send_email`, `fetch_emails`, `check_new_emails`, `list_folders`) untuk AI agent seperti Claude Desktop/Claude Code.
- **Dashboard web ringan untuk manajemen akun** — CRUD akun + uji konektivitas per protokol, disajikan dari binary yang sama (tanpa service/artefak tambahan). Lihat [Dashboard](#dashboard).
- Single static binary (Go, tanpa CGO), image Docker kecil.
- **3 varian rilis** dengan behavior identik: Docker x64, Docker Armbian (arm64), Windows x64 (system tray + bisa diinstall sebagai Windows Service). Lihat [Release / Instalasi](#release--instalasi).

## Struktur Project

```
cmd/xmail/              entrypoint headless — dipakai Docker x64 & Docker Armbian (arm64)
cmd/xmail-tray/          entrypoint Windows x64 — system tray + Windows Service (build tag: xmailtray)
internal/app/            Run(ctx, cfg): wiring bersama, dipakai cmd/xmail DAN cmd/xmail-tray
internal/winservice/     install/start/stop/uninstall sebagai Windows Service (build tag: xmailtray)
internal/config/         load config dari environment variable
internal/storage/        koneksi SQLite + migration
internal/cryptox/        enkripsi/dekripsi kredensial (AES-GCM)
internal/account/        model, repository, service akun email
internal/mailer/         interface Sender/Fetcher/Checker/Marker/FolderLister
internal/mailer/smtp/    implementasi kirim (SMTP)
internal/mailer/imap/    implementasi fetch/check/folder (IMAP)
internal/mailer/pop3/    implementasi fetch (POP3)
internal/api/            REST API (router, middleware, handler)
internal/dashboard/      dashboard web (aset HTML/CSS/JS di-embed + handler statis)
internal/mcpserver/      MCP server (expose tools)
scripts/release.sh       build release 3 target → dist/ + checksums
assets/icon.ico          icon tray Windows (placeholder)
```

Detail tiap package, alasan desain, dan cara menambah kode baru (endpoint/tool/protokol) ada di **[ARCHITECTURE.md](./ARCHITECTURE.md)** — dokumen ini lebih lengkap & lebih up-to-date dibanding sketsa struktur di [PLAN.md §1](./PLAN.md#1-struktur-direktori).

## Requirement

- Go 1.26+
- Docker (opsional, untuk build/run image)

## Menjalankan (development)

```bash
# copy contoh env lalu isi
cp .env.example .env

go mod tidy
make run
# atau: go run ./cmd/xmail
```

`.env` di-load otomatis (via `github.com/joho/godotenv`, dev-only) — env var asli yang sudah di-set (mis. dari Docker `-e`/`--env-file`) selalu menang, `.env` cuma fallback kalau belum ada.

### Environment Variables

| Variable | Wajib | Deskripsi |
|---|---|---|
| `XMAIL_LISTEN_ADDR` | tidak (default `:5569`) | Alamat listen HTTP API |
| `XMAIL_DB_PATH` | tidak (default `xmail.db`) | Path file SQLite |
| `XMAIL_ENCRYPTION_KEY` | **ya** | Base64 dari 32 byte random key, dipakai enkripsi kredensial akun. Generate: `openssl rand -base64 32` |
| `XMAIL_API_KEY` | **ya** (MVP) | API key statis untuk auth header `X-API-Key` |
| `XMAIL_MCP_STDIO` | tidak | Set `true` untuk expose MCP server lewat stdio transport |

## Dashboard

Binary yang sama menyajikan dashboard web untuk **manajemen akun** (CRUD + uji konektivitas). Tidak ada proses atau artefak tambahan — asetnya di-embed ke binary.

- **Buka**: `http://localhost:5569` (atau `http://<XMAIL_LISTEN_ADDR>`) di browser. Dari tray Windows, menu **Open dashboard** membuka URL ini.
- **Masuk**: masukkan nilai `XMAIL_API_KEY`. Key disimpan di `sessionStorage` browser (hilang saat tab ditutup) dan dikirim sebagai header `X-API-Key` pada tiap request — **key yang sama** dengan REST API/MCP.
- **Yang bisa dilakukan**: list/detail/create/edit/hapus akun, dan uji koneksi SMTP/IMAP/POP3 per akun.
- **Yang tidak ada di dashboard**: kirim email, fetch/baca pesan, dan daftar folder — jalur email tetap lewat REST API/MCP.

Dashboard tidak menambah hak akses: aset statis (`GET /`) memang tanpa auth karena tidak memuat rahasia, tapi semua endpoint API tetap wajib `X-API-Key` (lihat [docs/API.md](./docs/API.md) §3). Karena disajikan same-origin, tidak ada CORS. Respons dashboard diberi CSP `default-src 'self'`.

**Perhatian**: bila `XMAIL_LISTEN_ADDR` tidak di-bind ke loopback (mis. `0.0.0.0:5569`), dashboard & API bisa dijangkau dari jaringan — siapa pun yang memegang API key bisa mengelola akun. Batasi akses jaringan/firewall sesuai kebutuhan.

## Menjalankan via Docker (development)

```bash
make docker-build
make docker-run
```

Data SQLite disimpan di volume `xmail-data` (mount ke `/app/data` di container) supaya persist antar restart.

## Release / Instalasi

Ada 3 target rilis, semuanya lewat `internal/app.Run` yang sama (lihat [PLAN.md](./PLAN.md#01-tiga-target-rilis)) supaya behavior konsisten. Semua artefak dibangun oleh script, keluar ke `dist/` lengkap dengan `SHA256SUMS.txt`. Ada **2 versi script yang fungsinya sama persis** — pakai salah satu sesuai shell yang tersedia:

| Script | Jalankan dari | Kapan pakai |
|---|---|---|
| [`scripts/release.sh`](./scripts/release.sh) | Git Bash / WSL / Linux / macOS | Sudah ada Git for Windows (paling umum di mesin dev), atau WSL/Linux/macOS/CI |
| [`scripts/release.ps1`](./scripts/release.ps1) | PowerShell (native Windows) | **Windows tanpa Git Bash/WSL** — tidak butuh `bash`, `make`, atau tool Unix apapun |

```bash
# Git Bash / WSL / Linux / macOS
scripts/release.sh <target> [version]   # target: docker-amd64 | docker-arm64 | windows-amd64 | all
scripts/release.sh all v0.1.0           # atau: make release-all VERSION=v0.1.0
```

```powershell
# PowerShell native (Windows tanpa Git Bash/WSL)
scripts\release.ps1 -Target <target> [-Version <versi>]
scripts\release.ps1 -Target all -Version v0.1.0
```

Detail cara kerja script & cara menambah target baru ada di [ARCHITECTURE.md § Build & Release](./ARCHITECTURE.md#6-build--release). `make release-*` cuma alias singkat untuk `release.sh` — kalau tidak punya `make` (default di Windows polos), langsung pakai `release.ps1` atau `release.sh` saja.

### 1. Docker x64 (`linux/amd64`)

```bash
scripts/release.sh docker-amd64
docker load < dist/xmail-<version>-linux-amd64-docker.tar.gz
docker run -d -p 5569:5569 -v xmail-data:/app/data --env-file .env xmail:<version>-amd64
```

### 2. Docker Armbian (`linux/arm64`)

Untuk SBC ARM yang jalan Armbian (Orange Pi, Rock Pi, dst). Bisa di-build cross-platform dari mesin x64 lewat `docker buildx`, atau langsung di board-nya.

```bash
scripts/release.sh docker-arm64
docker load < dist/xmail-<version>-linux-arm64-docker.tar.gz
docker run -d -p 5569:5569 -v xmail-data:/app/data --env-file .env xmail:<version>-arm64
```

### 3. Windows x64 (system tray + Windows Service)

```bash
scripts/release.sh windows-amd64          # via Git Bash/WSL
```
```powershell
scripts\release.ps1 -Target windows-amd64  # via PowerShell native, tanpa Git Bash/WSL
```
Hasil kedua-duanya sama: `dist/xmail-tray-windows-amd64-<version>.exe` (sudah dites jalan lewat kedua cara).

Jalankan `.exe`-nya — muncul icon di system tray. Dari menu tray bisa:
- **Start / Stop** — jalankan/hentikan service di foreground (selama tray app dibuka).
- **Install as Windows Service** — daftarkan diri sebagai Windows Service (`XmailService`) supaya tetap jalan di background tanpa perlu login/tray terbuka; muncul di `services.msc`.
- **Uninstall** — hapus Windows Service.
- **Open dashboard** — buka `http://<listen_addr>` di browser default.

#### Kalau `.exe` di-double-click tapi "tidak terjadi apa-apa"

Gejala: tidak ada icon tray, tidak ada entri di Task Manager, tidak ada di `services.msc`, tidak ada log. Penyebab paling umum: **konfigurasi belum ada**, jadi proses berhenti di detik pertama dengan `XMAIL_API_KEY is required` — dan karena build rilis ini `windowsgui` (tanpa console), error itu sebelumnya tidak terlihat di mana pun.

Sekarang kegagalan start selalu meninggalkan jejak:

- **Dialog error** muncul saat ada user yang bisa melihatnya, berisi apa yang harus dibuat (di-skip kalau `XMAIL_NO_DIALOG=1`).
- **Log file**: `%LOCALAPPDATA%\xmail\xmail-tray.log` (mis. `C:\Users\<kamu>\AppData\Local\xmail\xmail-tray.log`) — cek ini dulu.

Yang dibutuhkan supaya bisa jalan: `XMAIL_API_KEY` dan `XMAIL_ENCRYPTION_KEY`. Cara termudah: buat file `.env` **di folder yang sama dengan `xmail.exe`** (bukan di root repo) — `.env` dibaca dari working directory, dan untuk exe yang di-double-click artinya folder exe itu sendiri:

```ini
XMAIL_API_KEY=kunci-pilihanmu
XMAIL_ENCRYPTION_KEY=<hasil dari: openssl rand -base64 32>
```

Dua catatan supaya tidak salah duga:
- Saat berjalan normal pun, tray app tidak muncul di daftar **Apps** Task Manager (tidak punya window biasa) — lihat tab **Details**.
- Setelah icon tray muncul, server **belum** jalan sampai kamu klik **Start**, atau install sebagai Windows Service. Sebelum itu, "Open dashboard" akan membuka port yang masih tertutup.

> Status: target Windows tray **sudah diimplementasikan dan diverifikasi jalan** (lihat PLAN.md Fase 7) — `make release-windows-amd64` menghasilkan `.exe` yang sudah dites benar-benar berjalan (tray icon + menu render tanpa error). Satu hal yang **belum** divalidasi otomatis: klik "Install as Windows Service" beneran (sengaja tidak dieksekusi otomatis karena itu perubahan level-sistem yang butuh Administrator — jalankan manual dan cek `services.msc`/`sc query XmailService` sebelum rilis). Icon di `cmd/xmail-tray/icon.ico` masih placeholder (digenerate programatik) — ganti dengan aset final kapan pun sebelum rilis publik.

## Testing

Strategi lengkap (piramida unit → integration → contract → race/static → manual per-target-rilis) ada di [PLAN.md §6](./PLAN.md#6-testing-strategy). Ringkasnya:

```bash
make test               # unit test saja — cepat, tanpa network/docker, wajib hijau tiap fase
make test-integration   # + integration test (tag "integration"): in-process fake SMTP/IMAP/POP3 server
make test-race          # + race detector (-race), wajib sebelum build image/release
make coverage           # laporan coverage per fungsi (go tool cover)

make test-all           # SEMUA cek sekaligus (lihat di bawah)
```

### Cek lengkap sebelum commit

Ada **dua script setara** (seperti `release.sh`/`release.ps1`) yang menjalankan seluruh verifikasi dev secara berurutan dan berhenti di kegagalan pertama:

| Script | Jalankan dari | Kapan pakai |
|---|---|---|
| [`scripts/test.sh`](./scripts/test.sh) | Git Bash / WSL / Linux / macOS / CI | ada Git Bash/WSL/macOS/Linux |
| [`scripts/test.ps1`](./scripts/test.ps1) | PowerShell (native Windows) | **Windows tanpa Git Bash/WSL** — tidak butuh `bash`/`make` |

```bash
bash scripts/test.sh                  # semua cek
bash scripts/test.sh --race           # + race detector (butuh cgo/gcc)
bash scripts/test.sh --coverage       # + laporan coverage
bash scripts/test.sh --help           # daftar flag
```

```powershell
scripts\test.ps1                 # semua cek
scripts\test.ps1 -Race -Coverage
```

Catatan: di git kedua script tersimpan sebagai mode `100644` (sama seperti `scripts/release.sh`), jadi panggil lewat `bash <script>` — atau `chmod +x scripts/*.sh` sekali kalau mau memanggilnya langsung.

Yang dijalankan: `gofmt` (deteksi format), `go mod tidy -diff` (drift go.mod/go.sum), `go vet`, `go build`, cek kompilasi tray+service (`GOOS=windows -tags xmailtray`, jadi ikut jalan di Linux/macOS), unit test, integration test, test `internal/winservice` + `cmd/xmail-tray` (hanya di host Windows), dan `node --check` untuk `app.js` dashboard (kalau `node` tersedia). Race detector bersifat opt-in karena butuh cgo/gcc (PLAN.md §6.4). `make test-all` cuma alias tipis untuk `scripts/test.sh`.

Tidak pakai `testify`/framework tambahan — stdlib `testing` + table-driven test saja, sejalan dengan prinsip project ini yang minim dependency. Untuk checklist manual/E2E tiap target rilis (Docker x64, Docker Armbian, Windows tray+service), lihat PLAN.md §6.5.

## Kontribusi / Urutan Kerja

Ikuti fase di [PLAN.md §5](./PLAN.md#5-urutan-kerja-fase) secara berurutan: Fase 1 (Core) → Fase 2 (SMTP) → Fase 3 (IMAP) → Fase 4 (POP3) → Fase 5 (MCP) → Fase 6 (Docker & Hardening) → Fase 7 (Windows Tray & Service) → Fase 8 (Release Packaging 3 target), lalu Fase 10 (Daftar folder IMAP — [PLAN-FOLDERS.md](./PLAN-FOLDERS.md)). Setiap fase punya langkah verifikasi sendiri di PLAN.md.
