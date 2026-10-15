# Build Spider Web (1 file .exe, tanpa jendela console)
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

go build -trimpath -ldflags '-w -s -H windowsgui' -o spider-web.exe .

Write-Host ""
Write-Host "OK: spider-web.exe" -ForegroundColor Green
Write-Host "Jalankan: .\spider-web.exe  (browser terbuka otomatis di http://localhost:8080)"
Write-Host "Port lain : `$env:PORT=9090; .\spider-web.exe"
