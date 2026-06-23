param(
  [string]$SingBoxSource = "sing-box",
  [string]$XTunnelSource = "../box_tunnel-v2.1.1/box_tunnel/binary/x-tunnel",
  [string]$OutDir = "KsuProxy/bin/arm64-v8a"
)

$ErrorActionPreference = "Stop"
$devRoot = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent $devRoot
Push-Location $root
try {
  New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

  $singBoxPath = Resolve-Path -LiteralPath $SingBoxSource
  $xTunnelPath = Resolve-Path -LiteralPath $XTunnelSource

  Copy-Item -LiteralPath $singBoxPath -Destination (Join-Path $OutDir "sing-box") -Force
  Copy-Item -LiteralPath $xTunnelPath -Destination (Join-Path $OutDir "x-tunnel") -Force

  Get-Item (Join-Path $OutDir "sing-box"), (Join-Path $OutDir "x-tunnel") |
    Select-Object FullName, Length, LastWriteTime
} finally {
  Pop-Location
}
