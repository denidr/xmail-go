# Implementation Plan: Folder Listing & Folder Error Semantics

Dokumen ini **terpisah dari [PLAN.md](./PLAN.md)** (fase rilis inti) dan [PLAN-DASHBOARD.md](./PLAN-DASHBOARD.md) (dashboard manajemen akun). Fokusnya dua hal yang muncul dari pengujian langsung terhadap akun Gmail asli:

1. **Tidak ada cara mengetahui daftar folder** — nama folder IMAP harus ditebak, dan untuk Gmail nama itu bergantung bahasa akun.
2. **Folder yang tidak ada dijawab `500 internal_error`**, bukan `400 validation_failed`.

- Requirement terkait: [PRD.MD](./PRD.MD) §6.3 (fetch IMAP + filter folder), §6.4 (MCP tools), §7 (API).
- Referensi arsitektur & resep extend: [ARCHITECTURE.md](./ARCHITECTURE.md) §1, §3, §5.1-§5.3, §7.
- Referensi kontrak REST: [docs/API.md](./docs/API.md).

---

## 0. Konteks & bukti (kenapa plan ini ada)

Semua di bawah ini direproduksi pada instance nyata (`xmail-tray-windows-amd64-0.1.0`, akun Gmail `stem.developer.apps@gmail.com`) pada 2026-09-26, bukan dugaan dari membaca kode:

| # | Kejadian | Bukti (persis dari server) |
|---|---|---|
| 1 | Fetch folder Sent dengan nama English gagal | `GET /messages?folder=[Gmail]/Sent Mail` → `500 {"code":"internal_error","message":"account: fetch: imap: select \"[Gmail]/Sent Mail\": imap: NO [NONEXISTENT] Unknown Mailbox: [Gmail]/Sent Mail (Failure)"}` |
| 2 | Nama folder sebenarnya bergantung bahasa akun | Yang benar ternyata `[Gmail]/Surat Terkirim` (akun berbahasa Indonesia) — ketemu hanya dengan **menebak 4 kandidat nama** |
| 3 | Tidak ada endpoint untuk menemukan nama itu | Tidak ada `GET /accounts/{id}/folders`, tidak ada tool MCP `list_folders`; IMAP `LIST` tidak pernah dipakai di codebase (cek: tidak ada pemanggilan `List(` di `internal/mailer/imap`) |
| 4 | Folder tidak ada = 500, bukan 400 | Poin #1: errornya masuk kategori `internal_error`, padahal itu kesalahan input pemanggil — bandingkan `uid` kosong → `400 validation_failed` |

Konsekuensi praktis: konsumen REST/MCP tidak bisa membangun pemilih folder, dan kesalahan nama folder terlihat seperti gangguan server, bukan kesalahan input. Ini juga menghalangi follow-up apa pun yang butuh tahu folder (mis. "tandai semua di folder X sebagai dibaca").

---

## 1. Scope

**In scope**

- Kapabilitas **daftar folder** untuk IMAP, diekspos di REST **dan** MCP (satu implementasi, dua kulit — ARCHITECTURE §1 rule 3).
- **Klasifikasi error**: folder tidak ada → `400 validation_failed` (REST) / pesan error yang jelas (MCP), di ketiga jalur yang menyentuh folder: `Fetch`, `Check`, `MarkRead`.
- Tes (unit + integration + contract) dan propagasi dokumentasi.

**Non-scope (eksplisit)**

- **Dashboard tidak ikut berubah.** Dashboard sengaja dibatasi ke manajemen akun (PRD §6.7); tidak ada UI baca pesan, jadi tidak ada tempat untuk pemilih folder. Kalau nanti UI baca pesan dibuat, endpoint ini yang akan dipakainya.
- Membuat/menghapus/rename folder (IMAP `CREATE`/`DELETE`/`RENAME`) — tidak diminta, dan menambah permukaan destruktif.
- Subscribe/unsubscribe (`SUBSCRIBE`) dan caching daftar folder (lihat §8).
- Resolusi otomatis peran folder (mis. menerima `"sent"` lalu memetakan ke `[Gmail]/Surat Terkirim`) — **ditunda**, lihat §8. Yang dikerjakan sekarang: mengembalikan atribut peran (`\Sent`, `\Drafts`, …) sehingga klien bisa menentukannya sendiri.
- Perubahan pada POP3 selain pemetaan error (POP3 memang tidak punya folder).

---

## 2. Keputusan teknis (locked-in)

| Item | Pilihan | Alasan |
|---|---|---|
| Kapabilitas baru | Interface `mailer.FolderLister` (mirip `mailer.Marker`) | Konvensi yang sudah ada: kapabilitas opsional = interface terpisah, dan hanya protokol yang mendukung yang mengimplementasikannya (ARCHITECTURE §1 rule 2) |
| Registrasi | Field `FolderLister` baru di `account.Protocol` + satu `RegisterProtocol` di `internal/app.wireMailer` | Pola ADR 0001 — menambah kapabilitas bukan menambah field/setter ke-`Service` |
| Bentuk data | `mailer.Folder{Name, Delimiter, Attributes}` dengan JSON tag, diserialisasi langsung oleh REST & MCP | Mengikuti preseden `mailer.Message` (satu bentuk, dua kulit, tanpa DTO) — aman karena tidak ada field rahasia (ARCHITECTURE §1 rule 4 soal rahasia tetap berlaku: `Folder` tidak punya kredensial) |
| Sumber data | IMAP `LIST "" "*"` (rekursif), selalu **live** | Satu panggilan murah; cache justru bikin daftar folder basi. Tidak ada tabel/migrasi baru |
| POP3 | `400 validation_failed` ("protocol pop3 has no folders") | Konsisten dengan `MarkRead` pada POP3 yang juga 400. Alternatif "kembalikan satu entri sintetis INBOX" ditolak: itu menambah cabang khusus di `Service` untuk keuntungan kecil |
| Filter `\Noselect` | **Tidak difilter** — dikembalikan apa adanya beserta atributnya | Tidak menyembunyikan data; klien yang memutuskan. Didokumentasikan bahwa entri `\Noselect` tidak bisa di-`SELECT` (fetch ke situ akan 400) |
| Klasifikasi error | Di dalam `internal/mailer/imap` (pengetahuan protokol tetap di sana), lewat sentinel `mailer.ErrFolderNotFound` | `internal/api`/`internal/mcpserver` tetap tidak tahu apa-apa soal IMAP; `account.Service` yang menerjemahkan ke `ErrValidation` |
| Dependency baru | **nol** | `List`, `ListData`, `ListOptions`, `Error.Code`, `ResponseCodeNonExistent` semuanya sudah ada di `go-imap/v2` yang terpasang |

**API `go-imap/v2` yang akan dipakai** (diverifikasi lewat `go doc` pada versi terpasang `v2.0.0-beta.8`, bukan dari ingatan):

```
func (c *Client) List(ref, pattern string, options *imap.ListOptions) *ListCommand
func (cmd *ListCommand) Collect() ([]*imap.ListData, error)   // caller must Close()
type ListData struct { Attrs []MailboxAttr; Delim rune; Mailbox string; ... }
type Error StatusResponse   // *Error punya field Type, Code, Text
const ResponseCodeNonExistent ResponseCode = "NONEXISTENT"
```

Catatan: `ListOptions` non-zero butuh IMAP4rev2/LIST-EXTENDED, jadi implementasinya memakai `nil` options (aman di semua server). `ListCommand` **wajib** di-`Close()` — sama seperti `closeClient` yang sudah ada.

---

## 3. Desain

### 3.1 `internal/mailer/types.go` — tipe & interface + sentinel

```go
// Folder is one mailbox on the server, as reported by IMAP LIST.
type Folder struct {
    Name       string   `json:"name"`
    Delimiter  string   `json:"delimiter,omitempty"`  // "" when the server reports NIL
    Attributes []string `json:"attributes,omitempty"` // e.g. `\Sent`, `\HasNoChildren`, `\Noselect`
}

// FolderLister lists the mailboxes available on a server (implemented by
// mailer/imap; POP3 has no folders, so it does not implement this).
type FolderLister interface {
    ListFolders(ctx context.Context) ([]Folder, error)
}

// ErrFolderNotFound marks a mailbox that does not exist on the server,
// so account.Service can map it to a caller error (400) instead of a
// server error (500). Protocol packages wrap it; nothing above mailer
// needs to know how a given protocol detects it.
var ErrFolderNotFound = errors.New("mailer: folder not found")
```

- `Delimiter` is a `string` because a rune that isn't set (`Delim == 0`) must serialize as absent, not as `\u0000`.
- `Attributes` are passed through as `MailboxAttr`'s underlying strings.

### 3.2 `internal/account/protocol.go` — satu field baru

```go
type Protocol struct {
    Sender       func(cfg ConnectionConfig, fromAddress, username, secret string) mailer.Sender
    Fetcher      func(cfg ConnectionConfig, username, secret string) mailer.Fetcher
    FolderLister func(cfg ConnectionConfig, username, secret string) mailer.FolderLister  // new; nil = protocol has no folders
}
```

`internal/app.wireMailer` menambah satu baris di registrasi IMAP yang sudah ada (`FolderLister: func(cfg, username, secret) mailer.FolderLister { return imap.New(...) }`) — bukan blok registrasi baru.

### 3.3 `internal/mailer/imap/client.go` — implementasi

```go
func (c *Client) ListFolders(ctx context.Context) ([]mailer.Folder, error)
```

- `dial` → `cl.List("", "*", nil)` → `defer cmd.Close()` → `cmd.Collect()` → `closeClient(cl)`.
- `Delim == 0` → `Delimiter: ""`; selain itu `string(d.Delim)`.
- `Attrs` → `[]string` (konversi langsung).
- Urutkan hasil secara deterministik (`sort.Slice` by `Name`) supaya keluaran REST dan MCP selalu sama urutannya dan tes tidak bergantung urutan respons server — pola yang sama dengan `Fetch` yang sudah di-sort by UID (PLAN.md §10.8 #36).
- Tidak ada `DefaultFolder`/canonicalisasi di sini: tidak ada parameter folder.
- Assert di atas deklarasi `var _ ...` yang sudah ada: tambahkan `_ mailer.FolderLister = (*Client)(nil)`.

**Klasifikasi error** (dipakai `Fetch`, `Check`, `MarkRead`):

```go
// classifyFolderErr converts an IMAP "no such mailbox" status response
// into mailer.ErrFolderNotFound (wrapped, so the server text survives).
// Anything else is returned unchanged.
func classifyFolderErr(err error) error
```

- Implementasi: `var ie *imapv2.Error; if errors.As(err, &ie) && (ie.Code == imapv2.ResponseCodeNonExistent || ie.Code == imapv2.ResponseCodeTryCreate) { return fmt.Errorf("%w: %s", mailer.ErrFolderNotFound, ie.Text) }`.
- Dipasang di tiga tempat: `Select` pada `Fetch`, `Status` pada `Check`, `Select` pada `MarkRead`.
- Kenapa di sini, bukan di `Service`: itu pengetahuan IMAP (`ResponseCode`), dan ARCHITECTURE §1 rule 2 melarang pengetahuan protokol naik ke atas `mailer/*`.

### 3.4 `internal/account/service.go` — satu method baru + satu helper

```go
// ListFolders returns the mailboxes available for an account's protocol.
func (s *Service) ListFolders(ctx context.Context, accountID, protocol string) ([]mailer.Folder, error)
```

- Default `protocol` lewat jalur yang sudah ada (`DefaultProtocol`), seperti `FetchMessages`/`CheckNew` (PLAN.md §10.5 #17 — jangan bikin guard baru).
- `repo.Get` lalu `protocolFor(protocol)`; kalau `p.FolderLister == nil` → `fmt.Errorf("%w: protocol %q has no folders", ErrValidation, protocol)`.
- Helper baru `domainError(err error) error` yang memetakan `mailer.ErrFolderNotFound` → `fmt.Errorf("%w: %s", ErrValidation, ...)`, dipakai di `FetchMessages`, `CheckNew`, dan `MarkRead` supaya pemetaannya ada di satu tempat.

### 3.5 REST — `GET /accounts/{id}/folders`

- Route di `internal/api/server.go` (`routes()`), handler baru di `internal/api/messages_handler.go` (paling relevan: folder = konsep pesan/fetch) atau `accounts_handler.go` — pilih `messages_handler.go`.
- Query: `?protocol=imap` (opsional, default `imap`). Tidak ada `folder`/`limit`/`offset`/`refresh`.
- Bobot tipis: decode query → `s.service.ListFolders` → `writeData` / `writeErrFor`. **Tidak ada DTO baru**: `[]mailer.Folder` di-serialisasi langsung, dan `nil` dinormalkan ke `[]` (preseden `handleMessagesList` — PLAN.md §10.9 ronde 9).
- `writeErrFor` sudah memetakan `ErrValidation` → 400 dan `ErrNotFound` → 404, jadi tidak perlu perubahan middleware/envelope.

### 3.6 MCP — tool `list_folders`

- Konstanta nama tool baru `toolListFolders = "list_folders"` (dipakai `registerTools` **dan** prefix error handler, pola yang sudah ada supaya tidak drift).
- Skema: `account_id` (required), `protocol` (opsional). **Tidak ada** argumen lain.
- Handler `handleListFolders`: validasi `AccountID` manual → `s.service.ListFolders` → `mcp.NewToolResultStructuredOnly(folders)`, dengan `nil` → `[]`.
- Didaftarkan dengan `mcp.NewTypedToolHandler` dan skema eksplisit via `mcp.WithString(...)` (bukan `WithInputSchema[T]`, lihat PLAN.md Fase 5).

### 3.7 Bentuk respons (kontrak)

```json
{
  "data": [
    { "name": "INBOX", "delimiter": "/" },
    { "name": "[Gmail]/Drafts", "delimiter": "/", "attributes": ["\\Drafts", "\\HasNoChildren"] },
    { "name": "[Gmail]/Surat Terkirim", "delimiter": "/", "attributes": ["\\Sent", "\\HasNoChildren"] },
    { "name": "[Gmail]", "delimiter": "/", "attributes": ["\\Noselect", "\\HasChildren"] }
  ],
  "error": null
}
```

- `attributes` di-`omitempty`; `delimiter` di-`omitempty` (server yang melaporkan NIL tidak memaksa klien memikirkan `\u0000`).
- REST: `GET /accounts/{id}/folders?protocol=imap` → `200`; `400 validation_failed` bila `protocol` tidak mendukung folder (`pop3`, `smtp`, atau tak terkonfigurasi); `404` bila `account_id` tidak ada.
- MCP: `list_folders {account_id, protocol?}` → array yang sama.

### 3.8 Perubahan perilaku: folder tidak ada → 400

| Sebelum | Sesudah |
|---|---|
| `GET /messages?folder=[Gmail]/Sent Mail` → `500 internal_error` | `400 validation_failed`, pesan menyebut nama folder dan teks server |
| `POST /check` dengan folder salah → `500` | `400` |
| `POST /messages/read` dengan folder salah → `500` | `400` |
| MCP tool dengan folder salah → `isError: true` + teks bergaya 500 | `isError: true` + pesan "folder not found" |

Ini **perubahan perilaku yang disengaja** dan satu-satunya di plan ini; dicatat di PLAN.md seperti keputusan tercatat lain (preseden: `PUT` = full replace). Error yang **tidak** berubah: gagal dial, auth salah, timeout, dan error IMAP lain (tetap `500`) — supaya tidak menyamarkan gangguan server sebagai kesalahan input.

---

## 4. Yang tidak berubah (backward compatibility)

- Tidak ada perubahan skema DB, migrasi, env var, atau dependency.
- `GET /messages`, `POST /check`, `POST /messages/read`, `POST /send`, dan MCP 4 tool lama: kontrak tidak berubah **kecuali** status code folder-salah (500 → 400, §3.8) dan pesan error yang kini lebih spesifik.
- `messages_cache` tetap di-key `(account, protocol, folder)` — daftar folder **tidak** ikut di-cache dan tidak menambah baris.
- Dashboard, Docker, dan rilis Windows tidak tersentuh.

---

## 5. Urutan kerja (fase)

Setiap fase berakhir dengan `bash scripts/test.sh` hijau (gate dev: gofmt, `go mod tidy -diff`, vet, build, unit, integration, tray+service, `node --check`).

### Fase F1 — Kapabilitas folder di lapisan mailer ✅ SELESAI
- [x] `mailer.Folder`, `mailer.FolderLister`, `mailer.ErrFolderNotFound` di `internal/mailer/types.go`.
- [x] `imap.Client.ListFolders` + assert interface + sort deterministik.
- [x] `internal/mailer/imap/client_test.go` (baru): unit test murni tanpa jaringan.
- [x] Integration test: fake `imapserver` memberi respons LIST; assert nama, delimiter, attributes.
- [x] Verifikasi: `go test ./internal/mailer/... -tags integration`.
- Catatan: belum ada permukaan REST/MCP, jadi belum ada yang bisa memakainya — mirip cara `Marker` dulu ditambahkan.

### Fase F2 — Service + wiring ✅ SELESAI
- [x] `Protocol.FolderLister` + registrasi di `internal/app.wireMailer`.
- [x] `Service.ListFolders` + guard `protocol` tidak mendukung folder → `ErrValidation`.
- [x] Test `internal/account/service_test.go`: mock `FolderLister` (pola mock inline yang sudah dipakai); POP3/SMTP → `ErrValidation`; akun tidak ada → `ErrNotFound`.
- [x] Verifikasi: `go test ./internal/account/`.

### Fase F3 — REST + MCP ✅ SELESAI
- [x] Route `GET /accounts/{id}/folders` + handler.
- [x] Tool MCP `list_folders` + handler + skema.
- [x] Test REST (`internal/api/folders_test.go`): 200 + bentuk `{data:[...]}`, `[]` untuk protokol tanpa folder… **bukan** — protokol tanpa folder = 400; jadi: 200 dengan mock, 400 untuk `protocol=pop3`, 404 untuk id salah.
- [x] Test MCP: handler langsung + satu test lewat transport JSON-RPC (menutup celah `BindArguments`, seperti `TestTransport_SendEmailBindsArguments`).
- [x] Verifikasi: `go test ./internal/api/ ./internal/mcpserver/`.

### Fase F4 — Klasifikasi error folder (500 → 400) ✅ SELESAI
- [x] `classifyFolderErr` di `internal/mailer/imap` + dipasang di `Fetch`/`Check`/`MarkRead`.
- [x] `domainError` helper di `internal/account/service.go` + dipakai di `FetchMessages`/`CheckNew`/`MarkRead`.
- [x] Test unit: `&imapv2.Error{Code: ResponseCodeNonExistent}` → `errors.Is(err, mailer.ErrFolderNotFound)`; kode lain (mis. `INUSE`) → diteruskan apa adanya.
- [x] Test service: fetcher mock yang mengembalikan `ErrFolderNotFound` → `ErrValidation`.
- [x] Test REST: folder salah → 400 (bukan 500) — inilah pembukti perubahan perilaku.
- [x] Verifikasi: `go test ./internal/mailer/imap/ ./internal/account/ ./internal/api/`.

### Fase F5 — Dokumentasi + verifikasi terhadap Gmail asli ✅ SELESAI
- [x] Propagasi dokumen (daftar lengkap di §6) + skill agent `.claude/skills/xmail/SKILL.md`.
- [x] Verifikasi live pada akun nyata (`stem.developer.apps@gmail.com`, binary headless baru dijalankan dengan `dist/xmail.db` & kredensial asli): `GET /accounts/{id}/folders` **menemukan** `[Gmail]/Surat Terkirim` (attributes `["\\HasNoChildren","\\Sent"]`) di antara 9 folder, terurut nama — persis akar masalah §0 #2. `GET /messages?folder=[Gmail]/Surat Terkirim&limit=2` → `200` dengan pesan asli.
- [x] Verifikasi live perubahan perilaku (§3.8): `GET /messages?folder=[Gmail]/Sent Mail` → **`400 validation_failed`** (`"account: fetch: imap: select \"[Gmail]/Sent Mail\": mailer: folder not found: Unknown Mailbox: [Gmail]/Sent Mail (Failure)"`) — bukan `500` lagi (§0 #1).
- [x] Verifikasi live MCP: `list_folders {account_id}` lewat JSON-RPC `/mcp` mengembalikan array yang sama (di `structuredContent` **dan** `content`), dan `fetch_emails` dengan folder salah → `isError: true` + pesan "folder not found". Dashboard tidak disentuh (§1) — sesuai non-scope.

### 5.1 Catatan implementasi (penyimpangan dari rencana)

- **`internal/mailer/imap/client_test.go` tidak berisi test `ListFolders`** — pemetaan `ListData` → `Folder` (delimiter NIL, attributes, urutan deterministik) diuji lewat integration test `TestIntegration_ListFolders` (fake `imapserver` handler LIST) di `integration_test.go`, sesuai tabel §7. `client_test.go` berisi unit test `classifyFolderErr` (F4) — itu memang penempatan yang direncanakan §7, jadi file dibuat di F4, bukan F1.
- **`classifyFolderErr` dipasang juga di `MarkRead`** (rencana §3.3 menyebut tiga tempat: `Fetch`/`Check`/`MarkRead` — persis seperti itu, tanpa tambahan).
- **`Service.ListFolders` juga memetakan error `ListFolders` protokol**: rencana §3.4 tidak menyebut pemetaan error untuk listing (LIST gagal = gangguan server), jadi `ListFolders` **tidak** melewati `domainError` — error bawaan diteruskan apa adanya (→ `500`), sesuai §8 "server tanpa LIST/jawaban aneh → 500". Hanya `Fetch`/`Check`/`MarkRead` yang memakai `domainError`.
- **Verifikasi live dijalankan terhadap akun Gmail asli** (bukan hanya unit test): binary headless hasil build kode baru dijalankan menunjuk `dist/xmail.db` dengan kredensial asli, lalu `GET /folders` (menemukan `[Gmail]/Surat Terkirim` + `\Sent`), `GET /messages` ke folder benar (`200`), `GET /messages` ke folder salah (`400`, bukan `500`), dan `list_folders`/`fetch_emails` lewat `/mcp`. Sisi otomatis juga membuktikan regression: dengan `domainError` di-revert-sementara, tes REST menampilkan `500 internal_error` persis perilaku lama. (Efek samping yang wajar: fetch mengisi `messages_cache` di DB itu — perilaku normal sebuah fetch, bukan perubahan destruktif.)
- **Non-ASCII (modified UTF-7/UTF-8) tetap belum diuji** — akun uji hanya punya nama folder ASCII (Indonesia, tapi ASCII). Risiko tetap seperti §8; bukan klaim selesai.
- **Tambahan di luar daftar dokumen §6**: `.claude/skills/xmail/SKILL.md` (skill agent yang mengajari cara memakai service lewat REST/MCP) juga diperbarui — daftar tool ke-5, cara menemukan nama folder, dan catatan folder-tidak-ada → 400. Dokumen ini tidak ada di §6 karena §6 fokus dokumen produk, tapi meninggalkannya basi akan menyesatkan agent yang justru jadi konsumen utama endpoint ini. Turut diperbarui: komentar paket `internal/mcpserver/server.go`, `PRD.MD` §10 (roadmap), dan komentar pohon direktori `PLAN.md` yang menyebut "4 tools".

---

## 6. Dokumentasi yang harus diperbarui

| Dokumen | Bagian | Perubahan |
|---|---|---|
| `docs/API.md` | §7 tabel ringkasan + seksi endpoint baru + §4 tabel error + §8 tipe data | `GET /accounts/{id}/folders`; `Folder`; baris "folder not found → 400"; hapus asumsi bahwa semua error fetch = 500 |
| `docs/MCP_CLIENT_GUIDE.md` | tabel tool | `list_folders` (5 tool, bukan 4) |
| `docs/postman/` | koleksi | request `Folders → List Folders` yang memakai `accountId` dari environment |
| `PRD.MD` | §6.3, §6.4, §7 | folder listing sebagai kemampuan IMAP; tool MCP ke-5; baris route |
| `ARCHITECTURE.md` | §1 (rule 2 contoh interface), §2 (file test baru), §3 (lifecycle baru), §5 (catatan resep), §7 (baris tabel test) | `FolderLister` di daftar interface; lifecycle `GET /folders`; test baru |
| `CONTEXT.md` | glosarium | entri **Folder list** (dan tegaskan `Folder` untuk POP3 tetap INBOX) |
| `README.MD` | daftar Fitur | "daftar folder IMAP" |
| `PLAN.md` | fase + log keputusan | catatan fase fitur ini + entri keputusan untuk perubahan 500 → 400 |
| `PLAN-FOLDERS.md` (dokumen ini) | §5 | tandai fase selesai + catatan penyimpangan, seperti PLAN-DASHBOARD.md §5.1 |

Kalimat yang harus muncul di `docs/API.md`: **"`GET /messages` dengan `folder` yang tidak ada sekarang `400 validation_failed`, bukan `500`."** — supaya konsumen yang dulu menangani 500 tahu.

---

## 7. Testing strategy

Mengikuti piramida PLAN.md §6 (stdlib `testing`, tanpa framework tambahan, DB temp-file nyata, mock mailer inline).

| Yang diuji | Di mana | Teknik |
|---|---|---|
| `ListFolders` memetakan `ListData` → `Folder` (delimiter NIL, attributes, urutan deterministik) | `internal/mailer/imap/integration_test.go` | fake `imapserver` yang sudah ada, tambah handler LIST |
| Klasifikasi `ResponseCodeNonExistent` → `ErrFolderNotFound`; kode lain tidak diubah | `internal/mailer/imap/client_test.go` (baru) | unit murni: konstruksi `&imapv2.Error{...}` — **tanpa jaringan** |
| `Service.ListFolders` dispatch + guard protokol | `internal/account/service_test.go` | mock `mailer.FolderLister` inline |
| `ErrFolderNotFound` → `ErrValidation` di `FetchMessages`/`CheckNew`/`MarkRead` | `internal/account/service_test.go` | fetcher mock yang mengembalikan error tersentinel |
| Endpoint REST: 200/400/404 + `[]` bukan `null` | `internal/api/folders_test.go` (baru) atau `server_test.go` | `httptest` + `newTestServer`, pola `registerIMAP` yang sudah ada |
| Tool MCP: handler + binding argumen lewat transport nyata | `internal/mcpserver/server_test.go` | `mcp.NewTypedToolHandler` + client MCP in-process |
| Perubahan perilaku 500 → 400 tidak bocor ke error lain | `internal/api/` | kasus "fetcher mengembalikan error jaringan" → tetap 500 |

Prinsip yang dipegang (dari kebiasaan repo ini): setiap fix harus **dibuktikan gagal tanpa fix-nya**. Khusus Fase F4, tes REST 400 itu harus dijalankan dulu dengan `domainError` dimatikan untuk memastikan ia benar-benar menangkap bug, baru di-restore.

---

## 8. Risiko & keputusan yang ditunda

| Hal | Status / alasan |
|---|---|
| **Resolusi peran folder** (klien kirim `"sent"` alih-alih `[Gmail]/Surat Terkirim`) | **Ditunda.** Yang dikerjakan sekarang adalah mengembalikan `attributes` (`\Sent`, `\Drafts`, …) supaya klien bisa memetakannya sendiri tanpa xmail menebak. Kalau nanti perlu, tambah helper `special_use` eksplisit — bukan heuristik nama |
| **Pemilih folder di dashboard** | Di luar scope dashboard (PRD §6.7). Butuh UI baca pesan dulu; endpoint ini sudah siap dipakai saat itu |
| **Caching daftar folder** | Sengaja tidak. LIST murah dan hasilnya berubah jarang tapi tidak terduga; cache bikin daftar basi. Kalau ternyata terlalu banyak dial, opsi follow-up: TTL pendek di memori (bukan tabel) |
| **`\Noselect`** | Dikembalikan apa adanya. Risiko: klien memakainya lalu dapat 400. Mitigasi: didokumentasikan + atributnya ikut dikirim |
| **Nama folder non-ASCII (modified UTF-7 / UTF-8)** | Belum diuji. Akun uji berbahasa Indonesia (ASCII). Perlu diverifikasi dengan folder bernama non-ASCII sebelum dianggap beres; catat hasilnya di PLAN.md |
| **Server tanpa `LIST` / jawaban aneh** | `Collect()` error → diteruskan sebagai 500 (bukan 400): itu memang gangguan server, bukan kesalahan input |
| **`-race`** | Tetap tidak tersedia di mesin ini (tanpa cgo/gcc) — tidak ada goroutine baru, tapi jangan dianggap sudah diuji race |

---

## 9. Dampak ke artefak rilis

Tidak ada. Tidak ada env var, migrasi, dependency, atau file rilis baru; ketiga target (Docker x64/arm64, Windows tray) memakai `internal/app.Run` yang sama dan otomatis mendapat endpoint + tool baru. Tidak perlu build ulang script rilis.
