# Deploy Spider Web Online

`spider-web` adalah server yang harus **jalan terus** selama crawl
(bisa belasan menit untuk ribuan artikel) — karena itu tidak cocok di
Netlify (hosting situs statis + function pendek, maks ~60 detik per request).
Tiga cara di bawah ini yang cocok, tanpa ubah kode.

## Opsi A — Cloudflare Tunnel (paling cepat, gratis)

Tetap jalankan `spider-web.exe` di PC sendiri, tapi dapat URL publik
`https://...` yang bisa dibuka dari mana saja. Anda sudah pakai Cloudflare
untuk `tikrenew.pages.dev`, jadi ini paling nyambung.

1. Install `cloudflared` di PC: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/
2. Login sekali: `cloudflared tunnel login`
3. Jalankan spider: `spider-web.exe` (jalan di `localhost:8080`)
4. Di terminal lain: `cloudflared tunnel --url http://localhost:8080`
5. Dapat URL publik acak `https://xxx.trycloudflare.com` — buka di browser/HP.

Untuk URL tetap (bukan acak), buat Named Tunnel sekali:
`cloudflared tunnel create spider` lalu route ke hostname di domain
Cloudflare Anda. Panduan: https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/

Catatan: PC harus tetap menyala selama dipakai.

## Opsi B — Render / Railway / Fly.io (gratis, tanpa PC menyala)

Layanan ini menjalankan aplikasi sebagai proses yang hidup terus —
persis yang dibutuhkan Spider. Dockerfile sudah disiapkan di repo ini.

**Render (termudah):**
1. Push folder `spider-web/` ke repo GitHub.
2. Di dashboard Render: New → Web Service → pilih repo.
3. Render otomatis membaca `Dockerfile` (atau `render.yaml` di repo ini).
4. Selesai — dapat URL `https://spider-web.onrender.com`.

Catatan paket gratis: service "tidur" setelah 15 menit tidak dipakai,
request pertama membangunkan (±30 detik), lalu normal.

**Railway / Fly.io:** cara mirip — hubungkan repo GitHub, pilih Dockerfile,
deploy. Keduanya punya free tier.

## Opsi B2 — Koyeb (dilaporkan gratis, status kartu simpang siur)

Beberapa sumber menyebut free tier Koyeb (Docker, 1 service) tanpa kartu
kredit; satu perbandingan terbaru menyebut ada hold $29 saat signup.
Layak dicoba — jika diminta kartu, lewati saja.

1. Daftar di koyeb.com (bisa pakai GitHub) → **Create App** → **GitHub** →
   pilih repo `spider-web`.
2. Builder: **Dockerfile**, Instance: **Free**, Port: biarkan default
   (Koyeb menyuntik `PORT` otomatis seperti Render).
3. Deploy → dapat URL `https://<nama>-<org>.koyeb.app`.

## Opsi B3 — Hugging Face Spaces

CATATAN (2026-10-16): halaman HF saat ini menyatakan Gradio dan Docker
Spaces **membutuhkan paket PRO (berbayar)** — opsi gratisnya tinggal Static
Spaces yang tidak bisa menjalankan backend Spider. Opsi ini tidak lagi
disarankan kecuali Anda sudah berlangganan PRO.

## Opsi C — VPS sendiri

1. Copy binary Linux ke VPS: `go build -o spider-web .` di Linux,
   atau pakai Docker: `docker build -t spider-web .`
2. Jalankan: `PORT=8080 ./spider-web` (atau via Docker: `docker run -p 8080:8080 spider-web`)
3. Supaya jalan terus + auto-start: pasang sebagai systemd service,
   dan pasang Nginx/Caddy di depannya untuk HTTPS bila diakses publik.
