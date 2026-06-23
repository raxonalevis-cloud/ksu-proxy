param(
  [string]$Go = "go",
  [string]$OutDir = ""
)

$ErrorActionPreference = "Stop"
$devRoot = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent $devRoot
$src = Join-Path $devRoot "src"
if ($OutDir -eq "") {
  $OutDir = Join-Path $root "KsuProxy/bin/arm64-v8a"
}

if (-not (Get-Command $Go -ErrorAction SilentlyContinue)) {
  throw "Go toolchain not found: $Go. Install Go or pass -Go with the full path to go.exe."
}

Push-Location $src
try {
  New-Item -ItemType Directory -Force -Path $OutDir | Out-Null
  $env:GOOS = "android"
  $env:GOARCH = "arm64"
  $env:CGO_ENABLED = "0"
  & $Go build -trimpath -ldflags "-s -w" -o (Join-Path $OutDir "proxyd") ./cmd/proxyd
  & $Go build -trimpath -ldflags "-s -w" -o (Join-Path $OutDir "proxyctl") ./cmd/proxyctl
} finally {
  Pop-Location
}
