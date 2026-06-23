Android arm64 binaries live here before packaging:

- proxyd
- proxyctl
- sing-box
- x-tunnel

Core sources used by this project:

- sing-box: ksu-proxy/sing-box
- x-tunnel: ../box_tunnel-v2.1.1/box_tunnel/binary/x-tunnel

Sync them with:
powershell -ExecutionPolicy Bypass -File dev/tools/stage-cores.ps1

Build proxyd/proxyctl with:
cd dev/src
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../../KsuProxy/bin/arm64-v8a/proxyd ./cmd/proxyd
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../../KsuProxy/bin/arm64-v8a/proxyctl ./cmd/proxyctl
