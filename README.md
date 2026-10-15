# 🕷️ Spider Web

Versi web dari aplikasi desktop **Spider** (crawler sitemap/artikel).
Masukkan URL website → link artikel + judul + tanggal terkumpul otomatis →
unduh sebagai **Excel** — semuanya lewat browser.

## Cara pakai (paling gampang)

1. Jalankan `spider-web.exe` (Windows) — browser terbuka otomatis di
   http://localhost:8080
2. Tempel URL website (satu per baris), atur opsi bila perlu, klik **Mulai**
3. Pantau progress di tab **Log**, lihat tabel di tab **Hasil**
4. Klik **Simpan ke Excel** untuk mengunduh:
   - 1 website → 1 file `{domain}_{yyyymmdd-hhmmss}.xlsx`
   - banyak website → 1 file `spider_{yyyymmdd-hhmmss}.zip` berisi satu
     `.xlsx` per website

## Opsi

| Opsi | Arti |
|---|---|
| Maks halaman | Jumlah maksimal halaman listing yang diperiksa per website (default 200) |
| Maks artikel | Jumlah maksimal artikel yang dikumpulkan per website (default 3000) |
| Paralel | Berapa banyak link diambil bersamaan (default 8, aman 5–10) |
| Jeda (ms) | Jeda sebelum mengambil tiap halaman, agar website tidak terbebani (default 150) |

Tombol **Berhenti** menghentikan crawl kapan saja; hasil yang sudah terkumpul
tetap bisa diunduh.

## Isi file Excel

Sheet **Results**: `No | Date | URL | Category | Keyword | Title`
- Date = tanggal publish artikel (YYYY-MM-DD, dari meta tag / JSON-LD / `<time>` / teks halaman)
- Category = sub-path pertama URL (`food-stamps` → `food stamps`)
- Keyword = segmen terakhir path URL
- Judul diambil dari `og:title` / `<title>` / `<h1>` (akhiran nama situs dibersihkan otomatis)

Sheet **Errors** (hanya bila ada): daftar URL artikel yang gagal + penyebabnya.

## Menjalankan di port lain

```powershell
$env:PORT=9090; .\spider-web.exe
```

Di Linux/Mac:

```bash
PORT=9090 ./spider-web
```

## Dipakai di jaringan / VPS

Server listen di semua interface (`:8080`). Untuk diakses dari komputer lain
di jaringan yang sama, buka `http://<ip-server>:8080`. Untuk diakses dari
internet, pasang di belakang reverse proxy (Nginx/Caddy) dengan HTTPS —
jangan expose langsung tanpa proteksi.

## Build dari source

Butuh Go 1.25+.

```powershell
# Windows -> 1 file spider-web.exe
.\build.ps1
```

```bash
# Linux
go build -trimpath -ldflags '-w -s' -o spider-web .
```

UI (`ui/`) di-embed ke dalam binary, jadi hasil build tetap 1 file.

## Struktur

```
├── server.go   # HTTP server + API + SSE + export (xlsx/zip)
├── crawler.go  # Logika crawl: fetch, klasifikasi link, pagination, judul, tanggal
├── excel.go    # Penulis .xlsx (kolom & sheet)
├── types.go    # Tipe data bersama
├── ui/         # index.html + main.js + style.css (vanilla, tanpa build step)
├── build.ps1   # Build Windows 1 file
└── README.md
```

## API (untuk integrasi)

- `POST /api/jobs` `{mainUrls, maxPages, maxArticles, concurrency, delayMs}` → `{jobId}`
- `GET /api/jobs/{id}/events` → Server-Sent Events (`progress`, `done`)
- `GET /api/jobs/{id}` → status
- `GET /api/jobs/{id}/results` → `{articles, errors, stopped, done}`
- `POST /api/jobs/{id}/stop`
- `GET /api/jobs/{id}/export` → file `.xlsx` / `.zip`
- `DELETE /api/jobs/{id}`

## Catatan

- Crawl berjalan di server (bukan di browser), jadi tidak kena batasan CORS.
- Beberapa website memblokir bot agresif — turunkan *Paralel* dan naikkan
  *Jeda* bila banyak error HTTP 429/403.
- Gunakan secara bertanggung jawab: hormati `robots.txt` dan Terms of Service
  website target.
