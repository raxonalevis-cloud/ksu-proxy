# KSU Proxy 开发指南

## 目录结构

项目根目录现在按刷入模块来组织，风格接近 TTLink：

```text
ksu-proxy/
  module.prop
  customize.sh
  service.sh
  action.sh
  uninstall.sh
  skip_mount
  webroot/
  KsuProxy/
    bin/arm64-v8a/
    config/
    scripts/
  dev/
    src/
      go.mod
      cmd/
      internal/
    tools/
    docs/
    examples/
  sing-box
```

打包 zip 只包含根目录刷入文件、`webroot/` 和 `KsuProxy/`，不会把 `dev/` 打进模块包。

`dev/` 内部结构：

```text
dev/
  src/
    go.mod
    cmd/
    internal/
  tools/
  docs/
  examples/
```

## 运行路径

模块安装后：

```text
/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/proxyd
/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/sing-box
/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/x-tunnel
/data/adb/modules/ksu-proxy/KsuProxy/scripts/ksu-proxy.sh
```

用户配置和运行态数据：

```text
/data/adb/ksu-proxy/config
/data/adb/ksu-proxy/runtime
/data/adb/ksu-proxy/logs
/data/adb/ksu-proxy/run
```

首次启动时，如果 `/data/adb/ksu-proxy/config/config.json` 不存在，脚本会从模块内的 `KsuProxy/config/` 复制默认配置。

## 主配置

默认主配置已经从 `TTLink-v2.32/TTLink/confs/box.json` 迁移到：

```text
KsuProxy/config/sing-box/config.json
```

关联资源也一并迁移：

```text
KsuProxy/config/sing-box/rules      <- TTLink-v2.32/TTLink/confs/rules
KsuProxy/config/sing-box/providers  <- TTLink-v2.32/TTLink/confs/providers
KsuProxy/config/sing-box/board      <- TTLink-v2.32/TTLink/board
```

已做路径适配：

```text
../confs/rules/...      -> rules/...
../confs/providers/...  -> providers/...
external_ui             -> /data/adb/ksu-proxy/config/sing-box/board
```

TTLink 原配置里的旧 `ech-tunnel` 分组已经改写为 `x-tunnel`。`KsuProxy/config/config.json` 里默认：

```json
"selector_tag": "x-tunnel"
```

运行时 `proxyd render` 会用 `KsuProxy/config/x-tunnel/nodes.list` 生成新的 `x-tunnel` selector。

## x-tunnel 节点

默认节点来自：

```text
box_tunnel-v2.1.1/box_tunnel/confs/tunnels/x_tunnel_nodes.list
```

迁移到：

```text
KsuProxy/config/x-tunnel/nodes.list
```

支持 `box_tunnel-v2.1.1` 风格：

```text
@default_dns=https://1.1.1.1/dns-query
@default_ech=cloudflare-ech.com
@default_front_ip=173.245.59.112,104.17.127.226
@default_token=just_a_tony
@default_parallel=3

tag|listen_port|front_url|token|dns|ech|front_ip|parallel
x-tunnel-az|1088|wss://az100.rety.de5.net/about
```

也兼容 TTLink 旧风格：

```text
dns=223.5.5.5/dns-query
ech=cloudflare-ech.com
n=3
ip=162.159.44.20
token=your_default_token

tag|listen_port|host_or_forward|ip|token|dns|ech|n
x-tunnel-main|2080|example.com|162.159.44.20|node_token
```

## 白名单

用户只维护包名意图：

```json
{
  "packages": [
    { "package_name": "com.android.chrome", "scope": "all_instances" }
  ]
}
```

`proxyd reconcile` 会自动解析所有 Android user/profile 下的 UID。执行层只用 UID 做防火墙匹配，包名只用于配置和 UI 展示。

## 代理模式

`capture.mode`：

- `tproxy`：默认模式，当前已实现。
- `tun`：字段和配置预留，防火墙后端待实现。

`routing.mode`：

- `rule`：保留 `box.json` 里的分流规则。
- `global`：只对已进入代理环境的流量全局走代理。
- `direct`：只对已进入代理环境的流量全部直连。

注意：这里的 `global` 不代表整机所有 App 都代理。非白名单 App 不会进入捕获链。

`hotspot.enabled=true` 时，proxyd 会把热点入口接口（例如 `ap0`、`swlan0`、`rndis0`）
上的 TCP/UDP 流量在 `PREROUTING` 做 TPROXY，并额外创建 `iif <iface> fwmark ... table ...`
策略路由。热点客户端没有 Android UID，因此不能复用白名单 UID 策略路由；必须按入口接口
把被标记流量送回本机 tproxy。

## Yacd

节点分组和手动切换复用 sing-box Clash API + Yacd：

```text
http://127.0.0.1:9090/ui
```

默认面板目录：

```text
/data/adb/ksu-proxy/config/sing-box/board
```

如果要让局域网访问面板，必须把 `clash_api_listen` 改成 `0.0.0.0:9090` 并设置 `clash_api_secret`。

## 构建

本机安装 Go 后：

```powershell
cd D:\linux\ksu-proxy
.\dev\tools\build-android.ps1
```

手动构建：

```powershell
cd D:\linux\ksu-proxy\dev\src
$env:GOOS="android"
$env:GOARCH="arm64"
$env:CGO_ENABLED="0"
go build -trimpath -ldflags "-s -w" -o ..\..\KsuProxy\bin\arm64-v8a\proxyd .\cmd\proxyd
go build -trimpath -ldflags "-s -w" -o ..\..\KsuProxy\bin\arm64-v8a\proxyctl .\cmd\proxyctl
```

## 核心二进制

当前指定来源：

```text
sing-box: D:\linux\ksu-proxy\sing-box
x-tunnel: D:\linux\box_tunnel-v2.1.1\box_tunnel\binary\x-tunnel
```

同步到模块目录：

```powershell
cd D:\linux\ksu-proxy
.\dev\tools\stage-cores.ps1
```

## 打包

```powershell
cd D:\linux\ksu-proxy
.\dev\tools\package-module.ps1
```

打包前会执行：

```powershell
.\dev\tools\stage-cores.ps1
.\dev\tools\verify-module.ps1 -RequireBuiltController
```

如果还没有编译 `proxyd` 和 `proxyctl`，打包脚本会拒绝生成 zip，避免刷入一个能安装但无法启动服务的包。

## 测试命令

设备端：

```sh
/data/adb/modules/ksu-proxy/KsuProxy/scripts/ksu-proxy.sh status
/data/adb/modules/ksu-proxy/KsuProxy/scripts/ksu-proxy.sh restart
```

直接调用控制器：

```sh
/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/proxyd -config /data/adb/ksu-proxy/config/config.json status
```
