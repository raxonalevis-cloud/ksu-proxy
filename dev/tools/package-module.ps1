param(
  [string]$Output = "dist/ksu-proxy-v0.1.1-dev.zip",
  [string]$Go = "go",
  [string]$Python = "python",
  [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"
$devRoot = Split-Path -Parent $PSScriptRoot
$root = Split-Path -Parent $devRoot
$module = $root
$dist = Join-Path $root (Split-Path -Parent $Output)
New-Item -ItemType Directory -Force -Path $dist | Out-Null

$stageScript = Join-Path $PSScriptRoot "stage-cores.ps1"
& $stageScript | Out-Host
if (-not $SkipBuild) {
  $buildScript = Join-Path $PSScriptRoot "build-android.ps1"
  & $buildScript -Go $Go | Out-Host
}
$verifyScript = Join-Path $PSScriptRoot "verify-module.ps1"
& $verifyScript -RequireBuiltController | Out-Host

$fullOutput = Join-Path $root $Output
$zipTool = Join-Path $PSScriptRoot "build-module-zip.py"
$pythonCmd = Get-Command $Python -ErrorAction SilentlyContinue
if (-not $pythonCmd) {
  $pythonCmd = Get-Command "python3" -ErrorAction SilentlyContinue
}
if (-not $pythonCmd) {
  throw "Python toolchain not found. Install Python or pass -Python with the full path to python.exe."
}

$tmpOutput = "$fullOutput.tmp"
if (Test-Path $tmpOutput) {
  Remove-Item -LiteralPath $tmpOutput
}

& $pythonCmd.Source $zipTool $module $tmpOutput | Out-Host
if ($LASTEXITCODE -ne 0) {
  if (Test-Path $tmpOutput) {
    Remove-Item -LiteralPath $tmpOutput
  }
  throw "Zip packaging failed with exit code $LASTEXITCODE"
}

Move-Item -LiteralPath $tmpOutput -Destination $fullOutput -Force
Write-Host "Wrote $fullOutput"
