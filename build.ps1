$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$cacheRoot = Join-Path $projectRoot ".build"
$appDir = Join-Path $projectRoot "cmd\vidowallpapernumbers"
$iconPath = Join-Path $appDir "app.ico"

$env:GOCACHE = Join-Path $cacheRoot "gocache"
$env:GOMODCACHE = Join-Path $cacheRoot "gomodcache"
$env:GOTMPDIR = Join-Path $cacheRoot "gotmp"
$env:TMP = Join-Path $cacheRoot "tmp"
$env:TEMP = Join-Path $cacheRoot "tmp"

New-Item -ItemType Directory -Force -Path $env:GOCACHE | Out-Null
New-Item -ItemType Directory -Force -Path $env:GOMODCACHE | Out-Null
New-Item -ItemType Directory -Force -Path $env:GOTMPDIR | Out-Null
New-Item -ItemType Directory -Force -Path $env:TMP | Out-Null

$sourceImage = Get-ChildItem -Path $projectRoot -File | Where-Object {
    $_.Extension -in @(".png", ".jpg", ".jpeg", ".bmp")
} | Sort-Object Name | Select-Object -First 1

if (-not $sourceImage) {
    throw "No image file found in project root for icon generation."
}

& (Join-Path $projectRoot "tools\generate_icon.ps1") -SourceImage $sourceImage.FullName -OutputIco $iconPath

Push-Location $appDir
try {
    windres --target=pe-x86-64 -i "app.rc" -o "resource_windows_amd64.syso"
}
finally {
    Pop-Location
}

$output = Join-Path $projectRoot "VidoWallpaperNumbers.exe"
go build -trimpath -ldflags "-H=windowsgui -s -w" -o $output ./cmd/vidowallpapernumbers
