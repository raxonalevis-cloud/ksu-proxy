# KSU Proxy

KernelSU transparent proxy module. sing-box core + x-tunnel sidecar, TProxy-first, package-name whitelist expanded to Android UIDs.

## Repository layout

- **Root** = KernelSU flashable module (`module.prop`, `service.sh`, `customize.sh`, etc.)
- `KsuProxy/` — runtime assets shipped in the zip: binaries, config templates, scripts
- `dev/` — source code and build tools (NOT included in module zip)
- `webroot/` — WebUI (`index.html`)
- `dist/` — built zip artifacts

## Build

All build scripts are **PowerShell**. Go cross-compiles to `android/arm64` with `CGO_ENABLED=0`.

```powershell
# Build proxyd + proxyctl
.\dev\tools\build-android.ps1

# Copy sing-box + x-tunnel binaries into module dir
.\dev\tools\stage-cores.ps1

# Full package: stage cores → build → verify → zip
.\dev\tools\package-module.ps1

# Manual Go build (from dev/src):
$env:GOOS="android"; $env:GOARCH="arm64"; $env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o ..\..\KsuProxy\bin\arm64-v8a\proxyd .\cmd\proxyd
```

Python 3 is required for `build-module-zip.py` (called by `package-module.ps1`).

## Verify before packaging

`verify-module.ps1` checks: required files exist, JSON configs parse, no CRLF in `.sh` files, non-empty rule/provider/board dirs. Always run with `-RequireBuiltController` for release zips.

## Config files

- `default-config.json` and `sing-box/config.json` are **pure JSON** — no comments allowed
- `.example.jsonc` variants exist for human reference with comments
- Runtime config lives at `/data/adb/ksu-proxy/config/` on device
- First boot copies module defaults → data dir; upgrades never overwrite user-modified files

## On-device commands

```sh
/data/adb/modules/ksu-proxy/KsuProxy/scripts/ksu-proxy.sh status
/data/adb/modules/ksu-proxy/KsuProxy/scripts/ksu-proxy.sh restart
/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/proxyd -config /data/adb/ksu-proxy/config/config.json status
```

## Go source structure (`dev/src/`)

- `cmd/proxyd/` — main daemon: run, stop, reconcile, render, status, list-apps, admin HTTP API
- `cmd/proxyctl/` — thin wrapper that exec's proxyd
- `internal/` — appresolver, config, core, firewall, hotspot, supervisor

## Conventions

- Shell scripts must use **Unix line endings** (LF). CRLF causes install failures.
- Module version tracked in `module.prop` (`version`, `versionCode`) and `KsuProxy/config/.module_config_version`
- Firewall mark: `0x12000000/0xff000000`, routing table: `2025`
- x-tunnel nodes use `|`-delimited format with `@default_*` headers
- TPROXY port: `2025`, Clash API: `127.0.0.1:9090`, Admin API: `127.0.0.1:9099`
