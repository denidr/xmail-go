# Implementation Plan: xmail Web Dashboard (Manajemen Akun)

Dokumen ini **terpisah dari [PLAN.md](./PLAN.md)**. PLAN.md mencatat fase 1-9 rilis inti (core, SMTP, IMAP, POP3, MCP, Docker, Windows tray/service, packaging). Dokumen ini fokus ke **satu fitur baru**: dashboard web yang **hanya** menangani akun (CRUD + uji konektivitas), tanpa menyentuh jalur email (send/fetch/check).

- Requirement: [PRD.MD](./PRD.MD) §6.7 (baru), §7.
- Referensi arsitektur & cara extend: [ARCHITECTURE.md](./ARCHITECTURE.md).
- Referensi kontrak REST: [docs/API.md](./docs/API.md).

---

## 0. Scope

**In scope:**

- Menyajikan UI web (HTML/CSS/JS) dari binary yang sama dengan API — tanpa artefak rilis baru.
- Manajemen akun: list, detail, create, update (full replace), delete.
- Uji konektivitas per protokol (`smtp`/`imap`/`pop3`) dari UI.
- Login memakai API key yang sudah ada (`XMAIL_API_KEY`), tanpa mekanisme auth baru di sisi server.

**Out of scope (eksplisit):**

- Kirim email (`/send`), fetch/check pesan (`/messages`, `/check`), mark-read di UI.
- Multi-user / RBAC / JWT / OAuth2.
- Perubahan pada endpoint REST yang sudah ada (hanya penambahan routing aset statis).
- Framework frontend (React/Vite) atau langkah build Node/npm.
- Artefak rilis baru: dashboard ikut ke ketiga target yang sudah ada (Docker x64, Docker arm64, Windows x64), tidak ada target ke-4.

---

## 0.1 Keputusan Teknis (locked-in)

| Item | Pilihan | Alasan |
|---|---|---|
| Penyajian aset | `go:embed` (`embed.FS`) + `http.FileServerFS`, paket baru `internal/dashboard` | tidak ada dependency baru, aset ikut jadi satu binary — konsisten prinsip "single static binary, minim dependency" (PLAN.md §0) |
| Frontend | HTML + CSS + JS vanilla (ES module), **tanpa build step** | tidak butuh Node/npm, tidak menambah rantai build ke Dockerfile/CI/Makefile |
| Routing UI | hash routing (`#/login`, `#/accounts`, `#/accounts/new`, `#/accounts/{id}`) | tidak butuh fallback routing di server; `GET /` selalu `index.html`, hash tidak pernah sampai ke server |
| Auth | API key yang sudah ada, disimpan di `sessionStorage`, dikirim JS via header `X-API-Key` | tidak ada state/sesi baru di server; key yang sama dengan REST & MCP |
| Aset statis | **tanpa auth** | tidak ada rahasia di dalam aset; browser tidak bisa menyisipkan header saat memuat HTML — rahasia hanya ada di response API |
| CORS | tidak perlu | dashboard disajikan dari origin yang sama dengan API (`same-origin`) |
| CSP | `default-src 'self'` pada respons dashboard | mitigasi XSS karena API key berada di browser |
| Dependency baru | **nol** | sejalan dengan PLAN.md §6 ("tidak pakai framework tambahan") |

---

## 1. Perubahan Struktur Direktori

```
internal/
  dashboard/
    dashboard.go               # package baru: //go:embed assets + Handler() http.Handler
    assets/
      index.html               # shell + 4 view (login/list/new/edit)
      app.js                   # router hash, API client (envelope), view render
      style.css
  api/
    server.go                  # Server.dashboard + komposisi Handler() + predicate isAPIPath
    dashboard_test.go          # test: aset publik, batas auth, integritas aset
docs/
  adr/
    0002-dashboard-serving-and-auth.md   # ADR: keputusan penyajian + model auth
```

`go:embed` hanya bisa meng-embed file di dalam/di bawah direktori package, jadi aset harus berada di `internal/dashboard/assets/` (bukan `web/` atau root repo).

---

## 2. Integrasi Server

### 2.1 Penyajian aset

- Paket `internal/dashboard` mengekspos `Handler() http.Handler` yang membungkus `http.FileServerFS(fs.Sub(embedded, "assets"))` (perlu `fs.Sub` karena `//go:embed assets` menyimpan prefix `assets/`).
- Handler menambahkan header pada setiap respons dashboard:
  - `Content-Security-Policy: default-src 'self'` — melarang CDN/inline script & style. **Konsekuensi**: `index.html` tidak boleh memakai `<script>...</script>` inline atau atribut `onclick=`; semua handler JS di-`addEventListener` dari `app.js`.
  - `X-Content-Type-Options: nosniff`.
  - `Cache-Control: no-cache` untuk `index.html` (supaya update binary langsung terbaca setelah restart), cache normal untuk `app.js`/`style.css`.
- Handler **tidak menyentuh** response API (tidak ada header ini di jalur `/accounts`, `/mcp`, `/healthz`).

### 2.2 Routing & batas auth

`api.Server` mendapat field `dashboard http.Handler` (boleh `nil`; semua test lama mengirim `nil` sehingga perilaku lama tidak berubah). `api.NewServer` mendapat parameter ke-4. Komposisinya:

```go
func (s *Server) Handler() http.Handler {
    api := apiKeyAuth(s.apiKey, s.mux)
    if s.dashboard == nil {
        return requestLogger(api)
    }
    return requestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if isAPIPath(r.URL.Path) {
            api.ServeHTTP(w, r)
            return
        }
        s.dashboard.ServeHTTP(w, r)
    }))
}

// isAPIPath adalah satu-satunya tempat yang tahu path mana milik API.
// Sisanya = aset dashboard (tanpa auth, tidak ada rahasia di dalamnya).
func isAPIPath(p string) bool {
    return p == "/healthz" || strings.HasPrefix(p, "/accounts") || strings.HasPrefix(p, "/mcp")
}
```

- `/healthz` tetap tanpa auth lewat `apiKeyAuth` yang sudah ada — tidak berubah.
- `/accounts` dan `/mcp` **tetap** wajib `X-API-Key` (401 tanpa key) — tidak ada pelonggaran auth di jalur API.
- `requestLogger` tetap membungkus semuanya; request aset ikut ter-log (method/path/status saja, tidak pernah body).
- Path tak dikenal (mis. `/foo`) → dashboard handler → `FileServerFS` → 404. Bisa diterima karena hash routing tidak pernah mengirim path aplikasi ke server.

### 2.3 Wiring

- `internal/app.Run`: `api.NewServer(cfg.APIKey, svc, mcpSrv.HTTPHandler(), dashboard.Handler())`.
- Tidak ada env var baru. Tidak ada perubahan di `config`, `storage`, `account`, `mailer`, `mcpserver`.

---

## 3. Kontrak API yang Dipakai (referensi — tidak berubah)

Dashboard hanya memanggil endpoint yang sudah ada:

| Aksi UI | Endpoint | Catatan untuk UI |
|---|---|---|
| Validasi key + list akun | `GET /accounts` | dipakai juga sebagai validasi login (401 → kembali ke `#/login`) |
| Detail akun | `GET /accounts/{id}` | untuk mengisi form edit |
| Create | `POST /accounts` | `password` **wajib** |
| Update | `PUT /accounts/{id}` | **full replace** — semua field dikirim ulang; protokol yang tidak diinginkan → `null` |
| Delete | `DELETE /accounts/{id}` | konfirmasi dulu di UI |
| Uji koneksi | `POST /accounts/{id}/test-connection` | body `{protocol}`; per protokol |

Semantik yang wajib dihormati UI (sumber: docs/API.md §5, §7):

1. **Password tidak pernah dikembalikan** — field password di form edit selalu kosong; label eksplisit "kosongkan = tidak mengubah kredensial". Saat update, kirim `password: null` kalau tidak diisi.
2. **`PUT` = full replace** — form edit harus mengirim seluruh field (name/email/username + semua blok protokol yang aktif). Lupa mengirim `imap` = menghapus konfigurasi IMAP. Ini sumber bug UX paling mungkin; ditutup dengan test manual di checklist.
3. **Create menyimpan dulu, baru bisa test** (ketegangan desain yang disadari, PRD §6.1) — alur UI: create → arahkan ke tombol "Test koneksi" → kalau gagal, perbaiki lewat form edit atau hapus.
4. **Envelope `{data,error}`** — API client JS membaca `error.code`/`error.message`; `401 unauthorized` khusus → reset key & kembali ke login.

---

## 4. Spesifikasi UI

### 4.1 View (hash routing)

| Hash | Isi |
|---|---|
| `#/login` | input API key + tombol "Masuk"; validasi dengan `GET /accounts`; sukses → simpan ke `sessionStorage`, redirect ke `#/accounts` |
| `#/accounts` | tabel akun + tombol "Tambah akun"; banner sukses/error |
| `#/accounts/new` | form kosong |
| `#/accounts/{id}` | form terisi (password kosong) + tombol "Test koneksi" per protokol + "Hapus akun" |

Header selalu tampil saat sudah login: indikator sesi + tombol "Keluar" (hapus `sessionStorage`).

### 4.2 Tabel akun

Kolom: Nama, Email, Username, Protokol aktif (badge SMTP/IMAP/POP3), Hasil test terakhir (per-sesi, in-memory), Aksi (`Test`, `Edit`, `Hapus`).

- `Test` menjalankan `test-connection` untuk tiap protokol yang terkonfigurasi secara berurutan dan menampilkan ok/error inline per protokol.
- `Hapus` memakai dialog konfirmasi yang menyebut nama akun.

### 4.3 Form create/edit (komponen bersama)

- Field umum: `name`, `email`, `username`, `password`.
- Per protokol (SMTP/IMAP/POP3): checkbox "aktifkan" + `host`, `port`, `tls_mode` (`select`: `tls` / `starttls` / `none`). Saat checkbox mati → blok dikirim sebagai `null`.
- Validasi klien mencerminkan aturan server (field wajib, port 1-65535, minimal satu protokol, POP3 + `starttls` ditolak) — **tetapi server tetap otoritas**: pesan `error.message` dari server ditampilkan apa adanya, tidak ditelan validasi klien.
- Tombol aksi disabled selama request berjalan; ada indikator loading.

### 4.4 State & penanganan error

- Satu API client JS: `api(method, path, body?)` → parse envelope; lempar `{code, message}` bila `error != null`; `401` → logout paksa.
- **Tidak pernah merender data dari server/user lewat `innerHTML`** — pakai `textContent`/`createElement`. Ini melengkapi CSP sebagai dua lapis pertahanan XSS.
- Setelah operasi tulis berhasil: tampilkan banner, kembali ke `#/accounts`, list di-refresh.

---

## 5. Urutan Kerja (Fase)

### Fase D1 — Static serving + routing/auth ✅ SELESAI
- [x] Paket `internal/dashboard`: `//go:embed assets`, `Handler()` (CSP, nosniff, no-cache).
- [x] `api.Server`: field `dashboard`, komposisi `Handler()`, predicate `isAPIPath`; `api.NewServer` + parameter ke-4.
- [x] Semua pemanggil `api.NewServer` lama diperbarui (`nil`).
- [x] Test `internal/api/dashboard_test.go`: aset publik `200` tanpa key; `GET /accounts` & `POST /mcp` tetap `401` tanpa key; `GET /` → `index.html`; header CSP/nosniff ada di respons dashboard dan **tidak** ada di respons API.
- [x] Verifikasi: `go build ./...`, `go vet ./...`, `go test ./...` hijau.

### Fase D2 — Shell dashboard + login ✅ SELESAI
- [x] `index.html`, `style.css`, `app.js`: router hash, API client, view login, header/sesi, tombol keluar.
- [x] Test Go untuk integritas aset: `index.html` mereferensikan aset yang benar-benar ada di `embed.FS`; `index.html` tidak memuat script/style inline (karena CSP `default-src 'self'`).
- [x] Verifikasi: aset & header diuji dengan `httptest` (`internal/dashboard/dashboard_test.go`) + smoke test server asli (`GET /` → 200 `text/html` + CSP). Perilaku login itu sendiri = checklist manual di bawah.

### Fase D3 — List + delete + test koneksi ✅ SELESAI
- [x] Tabel akun, badge protokol, tombol test (per protokol), tombol hapus dengan konfirmasi.
- [x] Verifikasi: alur API yang dipakai diverifikasi nyata lewat server asli (create → list → test-connection sukses-gagal → delete); rendering tabel = checklist manual.

### Fase D4 — Form create/edit ✅ SELESAI
- [x] Form bersama new/edit; checkbox protokol; map ke `accountRequest`; penanganan `password: null`; pengiriman full-replace.
- [x] Verifikasi: semantik full-replace & `password: null` dicocokkan ke `docs/API.md` §7.5 dan diverifikasi lewat API asli; interaksi form = checklist manual.

### Fase D5 — Hardening + dokumentasi ✅ SELESAI
- [x] `docs/adr/0002-dashboard-serving-and-auth.md`.
- [x] Update: `ARCHITECTURE.md` (§1 prinsip #5, §2 layout, §3.5 lifecycle dashboard, §5.7 resep "tambah aset/halaman baru", §7 tabel testing), `README.MD` (seksi Dashboard + fitur + struktur), `docs/API.md` (§12 + baris `GET /`).
- [x] Cek keamanan: `grep` di `internal/dashboard/assets` → yang muncul hanya field input, nama storage key, dan nama header — tidak ada kredensial tertanam, tidak ada `innerHTML`, tidak ada inline handler/style.
- [~] Checklist manual lintas target: **headless binary (= entrypoint Docker) sudah dijalankan nyata** dengan server asli; `docker run` container dan GUI tray Windows **belum** dieksekusi di sesi ini (lihat catatan).

### 5.1 Catatan implementasi (penyesuaian dari rencana)

- **Deviasi kecil di header cache**: rencana §2.1 bilang `no-cache` hanya untuk `index.html`. Implementasinya `no-cache` untuk **semua** aset dashboard — pasangan `index.html` baru + `app.js` lama yang ke-cache adalah bug split-version yang tidak akan tertangkap test Go mana pun. Efisien karena `http.FileServerFS` tetap menjawab `304` lewat `Last-Modified` bila tidak berubah.
- **Tambahan hardening yang tidak eksplisit di rencana**: `X-Content-Type-Options: nosniff`, dan satu test otomatis (`TestIndexHasNoInlineScriptOrStyle`) yang mengunci `index.html` tetap patuh CSP (tanpa `<script>` inline, `<style>`, `style=""`, atau `on*=`). Tanpa itu, satu atribut inline saja bikin halaman blank tanpa ada test yang gagal.
- **`api.NewServer` sekarang `NewServer(apiKey, service, mcpHandler, dashboardHandler)`** — hanya 2 pemanggil (`internal/app.Run`, `internal/api/server_test.go`), plus helper test baru `newTestServerWith`.
- **Path non-API tak dikenal** (mis. `/nope`) dijawab `404` oleh file server, bukan envelope JSON — didokumentasikan di `docs/API.md` §12 supaya tidak terbaca sebagai bug.
- **`app.js` tidak punya test otomatis** (sesuai rencana §6): repo sengaja tanpa test runner JS. Perilaku UI ditutup checklist manual; kalau JS tumbuh lebih besar dari layar akun, evaluasi ulang.
- **Cakupan rilis**: dashboard ikut ke-3 target tanpa perubahan Dockerfile/Makefile/script rilis (aset di-`go:embed`). Tidak ada env var atau migrasi baru.

**Hasil verifikasi (sesi ini)**: `go build`/`go vet` bersih (default + `-tags xmailtray`); `go test ./...` = **130** baris `=== RUN` (naik dari 120; +10 test dashboard), `go test -tags integration ./...` = **149** (naik dari 139), `go test -tags xmailtray ./internal/winservice/` = 3.

**Smoke test server asli** (binary headless, `127.0.0.1:5599`): `GET /` → `200` + `Content-Security-Policy: default-src 'self'` + `nosniff` + `no-cache` + `text/html`; `GET /app.js` & `/style.css` → `200` dengan `text/javascript`/`text/css`; `GET /accounts` tanpa key → `401` envelope; `GET /healthz` → `200` envelope; `POST /accounts` → `201`; `GET /accounts` (dengan key) → `200`; `POST .../test-connection {smtp}` → `500 internal_error` dengan pesan dial asli; `POST .../test-connection {nope}` → `400 validation_failed`; `DELETE` → `200 {"deleted":true}`; list kembali `[]`.


---

## 6. Testing Strategy

Mengikuti piramida PLAN.md §6, dengan satu keterbatasan jujur: **repo ini tidak punya test runner JS** (stdlib Go saja), jadi logika `app.js` tidak diuji otomatis.

| Yang diuji | Di mana | Teknik |
|---|---|---|
| Batas auth (API tetap 401, aset publik 200) | `internal/api/dashboard_test.go` | `httptest` + `newTestServer` varian dashboard |
| Header CSP/nosniff pada aset, tidak ada di API | idem | assert header per-path |
| Integritas aset (referensi `index.html` ↔ `embed.FS`, tidak ada inline script) | `internal/dashboard/dashboard_test.go` | baca `embed.FS` + parse string sederhana |
| Perilaku UI (login, list, test, create/edit/delete) | checklist manual §5 (Docker + tray) | dijalankan manual, dicatat hasilnya |
| Regresi kontrak API | `internal/api/server_test.go` (sudah ada) | tidak berubah — bukti endpoint lama tidak tersentuh |

Verifikasi akhir tiap fase: `bash scripts/test.sh` (atau `scripts\test.ps1` di Windows tanpa Git Bash) — gate dev yang menjalankan `gofmt`, `go mod tidy -diff`, vet, build, cek kompilasi tray (`GOOS=windows -tags xmailtray`), unit + integration test, dan `node --check` atas `app.js` bila `node` tersedia; semuanya harus hijau (lihat README "Cek lengkap sebelum commit").

---

## 7. Keamanan

- **Model auth**: API key di `sessionStorage` (hilang saat tab ditutup), dikirim sebagai header `X-API-Key`. Trade-off vs alternatif cookie-sesi HttpOnly dicatat di ADR 0002; cookie butuh state sesi + endpoint login baru, sehingga ditolak untuk MVP (prinsip "minim dependency/state").
- **XSS**: CSP `default-src 'self'` (tanpa inline, tanpa CDN) + tidak ada `innerHTML` dengan data dinamis.
- **Tidak ada eskalasi hak**: dashboard tidak punya kemampuan melebihi klien REST biasa — semua aksi tetap lewat API + key yang sama. Dashboard bukan jalur otorisasi baru.
- **Kredensial**: password write-only, tidak pernah di-prefill, tidak pernah di-log; tidak ada secret yang di-embed saat build.
- **Eksposur**: dashboard tidak membuka port baru. Bila `XMAIL_LISTEN_ADDR` bukan loopback, dashboard (dan API) terjangkau dari jaringan — catatan keamanan yang sudah berlaku untuk API sekarang juga berlaku untuk dashboard; ditambahkan ke README.
- **Tidak ada log body**: `requestLogger` tetap hanya mencatat method/path/status/duration.

---

## 8. Risiko & Keputusan yang Ditunda

| Hal | Status |
|---|---|
| Alternatif auth cookie-sesi (HttpOnly) | ditunda — dicatat di ADR 0002 sebagai alternatif bila key-di-browser dianggap kurang aman |
| UI kirim/baca email | di luar scope; desain ulang bila diminta (butuh compose/reader + penanganan attachment) |
| Multi-key / rotatable API key (PLAN.md Fase 9 backlog) | tidak dikerjakan di sini; kalau nanti ada, dashboard otomatis untung (key per-user) |
| Test otomatis untuk `app.js` | tidak ada test runner JS di repo; ditutup dengan checklist manual. Bila JS tumbuh besar, evaluasi runner ringan (mis. `node --test`) — **bukan** sekarang |
| `go test -race` | tetap tidak tersedia di mesin dev (tanpa cgo), sama seperti PLAN.md §6.4 — dashboard tidak menambah goroutine baru |
