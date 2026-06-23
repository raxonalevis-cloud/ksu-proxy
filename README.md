# KSU Proxy

KernelSU transparent proxy module with a TTLink-style package layout:

- sing-box as the main transparent proxy and routing core.
- x-tunnel as optional sidecar nodes exposed to sing-box through local SOCKS outbounds.
- Package-name whitelist intent expanded to Android UID instances automatically.
- TProxy-first architecture so non-whitelisted apps bypass the module.
- Hot whitelist reconciliation without restarting sing-box.
- Module WebUI for package whitelist selection.
- Default configuration references for sing-box and x-tunnel.

Package-facing files live at the project root, runtime assets live in `KsuProxy/`, and development files live in `dev/`.

Configuration reference files:

- `KsuProxy/config/README.md`
- `KsuProxy/config/config.example.jsonc`
- `KsuProxy/config/sing-box/README.md`
- `KsuProxy/config/x-tunnel/README.md`

Runtime configuration is initialized under `/data/adb/ksu-proxy/config` on first start.
