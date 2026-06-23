param(
  [string]$ModuleDir = ".",
  [switch]$RequireBuiltController
)

$ErrorActionPreference = "Stop"
$devRoot = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent $devRoot
$module = Join-Path $root $ModuleDir

$requiredRoot = @(
  "module.prop",
  "service.sh",
  "customize.sh",
  "action.sh",
  "uninstall.sh",
  "skip_mount",
  "webroot/index.html",
  "KsuProxy/scripts/ksu-proxy.sh",
  "KsuProxy/config/whitelist/packages.json",
  "KsuProxy/bin/arm64-v8a/sing-box",
  "KsuProxy/bin/arm64-v8a/x-tunnel"
)

if ($RequireBuiltController) {
  $requiredRoot += @(
    "KsuProxy/bin/arm64-v8a/proxyd",
    "KsuProxy/bin/arm64-v8a/proxyctl"
  )
}

foreach ($rel in $requiredRoot) {
  $path = Join-Path $module $rel
  if (-not (Test-Path -LiteralPath $path)) {
    throw "Missing module entry: $rel"
  }
}

$nonEmptyDirs = @(
  "KsuProxy/config/sing-box/rules",
  "KsuProxy/config/sing-box/providers",
  "KsuProxy/config/sing-box/board"
)

foreach ($rel in $nonEmptyDirs) {
  $path = Join-Path $module $rel
  if (-not (Test-Path -LiteralPath $path)) {
    throw "Missing module directory: $rel"
  }
  $count = (Get-ChildItem -LiteralPath $path -Recurse -File | Measure-Object).Count
  if ($count -le 0) {
    throw "Module directory is empty: $rel"
  }
}

function Test-JsonFile($path) {
  $raw = [System.IO.File]::ReadAllText($path, [System.Text.Encoding]::UTF8)
  $raw | ConvertFrom-Json | Out-Null
}

Test-JsonFile (Join-Path $module "KsuProxy/config/default-config.json")
Test-JsonFile (Join-Path $module "KsuProxy/config/whitelist/packages.json")

$shellRoots = @(
  (Join-Path $module "service.sh"),
  (Join-Path $module "customize.sh"),
  (Join-Path $module "action.sh"),
  (Join-Path $module "uninstall.sh")
)
$shellFiles = @()
foreach ($path in $shellRoots) {
  if (Test-Path -LiteralPath $path) {
    $shellFiles += Get-Item -LiteralPath $path
  }
}
$scriptDir = Join-Path $module "KsuProxy/scripts"
if (Test-Path -LiteralPath $scriptDir) {
  $shellFiles += Get-ChildItem -LiteralPath $scriptDir -Recurse -File | Where-Object { $_.Extension -eq ".sh" }
}
foreach ($file in $shellFiles) {
  $bytes = [System.IO.File]::ReadAllBytes($file.FullName)
  for ($i = 0; $i -lt $bytes.Length - 1; $i++) {
    if ($bytes[$i] -eq 13 -and $bytes[$i + 1] -eq 10) {
      throw "CRLF line endings found in shell script: $($file.FullName)"
    }
  }
}

Write-Host "Module structure looks installable for KernelSU manager: $module"
