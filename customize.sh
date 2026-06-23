#!/system/bin/sh
SKIPUNZIP=0
ASH_STANDALONE=1

ui_print "- KSU Proxy"
ui_print "- User config will live in /data/adb/ksu-proxy/config"

DATA="/data/adb/ksu-proxy"
DATA_CONFIG="${DATA}/config"
MODULE_CONFIG="${MODPATH}/KsuProxy/config"

stop_active_module_before_update() {
  active="/data/adb/modules/ksu-proxy"
  script="${active}/KsuProxy/scripts/ksu-proxy.sh"

  if [ -x "${script}" ] && [ "${active}" != "${MODPATH}" ]; then
    ui_print "- Stopping active ksu-proxy before update"
    KSP_SILENT=1 KSP_STOP_REASON="module update pending reboot" "${script}" stop >/dev/null 2>&1 || true
  fi

  for pid_file in "${DATA}/run/module-watch.pid" "${DATA}/run/proxyd-run.pid"; do
    [ -f "${pid_file}" ] || continue
    pid="$(cat "${pid_file}" 2>/dev/null)"
    case "${pid}" in
      ""|*[!0-9]*) rm -f "${pid_file}"; continue ;;
    esac
    if [ -d "/proc/${pid}" ] && tr '\000' ' ' <"/proc/${pid}/cmdline" 2>/dev/null | grep -qF "ksu-proxy.sh"; then
      kill "${pid}" >/dev/null 2>&1 || true
    fi
    rm -f "${pid_file}"
  done
}

generate_default_config() {
  cat > "${DATA_CONFIG}/default-config.json" << 'DEFAULTCONF_EOF'
{
  "version": 1,
  "data_dir": "/data/adb/ksu-proxy",
  "capture": { "mode": "tproxy" },
  "routing": { "mode": "rule" },
  "sing_box": {
    "enabled": true,
    "binary": "/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/sing-box",
    "base_config": "/data/adb/ksu-proxy/config/sing-box/config.json",
    "runtime_config": "/data/adb/ksu-proxy/runtime/sing-box/config.json",
    "config_dir": "/data/adb/ksu-proxy/config/sing-box",
    "tproxy_listen": "::",
    "tproxy_port": 2025,
    "tun_interface": "ksu0",
    "tun_address4": "172.18.0.1/30",
    "tun_address6": "fdfe:dcba:9876::1/126",
    "clash_api_listen": "127.0.0.1:9090",
    "clash_api_secret": "",
    "external_ui": "/data/adb/ksu-proxy/config/sing-box/board",
    "external_ui_download_url": "https://srs.acstudycn.eu.org/gh-pages.zip",
    "external_ui_download_detour": "out-direct",
    "validate_before_run": true,
    "restart_on_config_mod": true
  },
  "x_tunnel": {
    "enabled": true,
    "binary": "/data/adb/modules/ksu-proxy/KsuProxy/bin/arm64-v8a/x-tunnel",
    "nodes_file": "/data/adb/ksu-proxy/config/x-tunnel/nodes.list",
    "selector_tag": "x-tunnel",
    "default_dns": "https://223.5.5.5/dns-query",
    "default_ech": "cloudflare-ech.com",
    "default_n": 3,
    "default_front_ips": "173.245.59.112,104.17.127.226",
    "default_ip": "",
    "default_token": ""
  },
  "whitelist": {
    "file": "/data/adb/ksu-proxy/config/whitelist/packages.json",
    "mode": "package_all_instances",
    "clone_user_ids": [999]
  },
  "hotspot": {
    "enabled": false,
    "auto_detect": true,
    "interfaces": ["ap0", "wlan1", "swlan0", "rndis0", "bt-pan"],
    "client_mode": "all",
    "client_allow_mac": []
  },
  "firewall": {
    "backend": "auto",
    "chain_prefix": "KSP",
    "mark": "0x12000000/0xff000000",
    "table": 2025,
    "rule_priority": 1000,
    "core_uids": [0, 1000, 2000],
    "bypass_ipv4": ["0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/3"],
    "bypass_ipv6": ["::/127", "fc00::/7", "fe80::/10", "ff00::/8"],
    "disable_quic": false,
    "dry_run": false
  },
  "runtime": {
    "poll_interval_seconds": 2,
    "start_grace_millis": 500,
    "admin_api_listen": "127.0.0.1:9099",
    "module_dir": "/data/adb/modules/ksu-proxy"
  },
  "update": {
    "enabled": true,
    "repo": "raxonalevis-cloud/ksu-proxy",
    "check_interval_hours": 24
  },
  "binaries": {
    "sing_box_url": "https://dsm.210313.xyz:4443/sharing/wGUXx2l1e",
    "x_tunnel_url": "https://dsm.210313.xyz:4443/sharing/fawIHVa0W"
  }
}
    "start_grace_millis": 500,
    "admin_api_listen": "127.0.0.1:9099",
    "module_dir": "/data/adb/modules/ksu-proxy"
  }
}
DEFAULTCONF_EOF
}

generate_nodes_list() {
  cat > "${DATA_CONFIG}/x-tunnel/nodes.list" << 'NODESLIST_EOF'
# Shared tunnel defaults.
@default_dns=https://223.5.5.5/dns-query
@default_ech=cloudflare-ech.com
@default_front_ips=173.245.59.112,104.17.127.226
@default_token=your_token
@default_parallel=3

# tag|listen_port|front_url
x-tunnel-placeholder|1088|wss://example.com/about

# tag|listen_port|front_url|token|dns|ech|front_ips|parallel
x-tunnel-full|2088|wss://example.net/about|node_token|https://223.5.5.5/dns-query|cloudflare-ech.com|173.245.59.112,104.17.127.226|3
NODESLIST_EOF
}

install_config_templates() {
  mkdir -p "${DATA_CONFIG}/sing-box/providers" "${DATA_CONFIG}/sing-box/rules" "${DATA_CONFIG}/sing-box/board" "${DATA_CONFIG}/x-tunnel" "${DATA_CONFIG}/whitelist"

  # Providers (placeholders)
  for f in ruite.json v2ray.txt clash.yaml; do
    [ -f "${MODULE_CONFIG}/sing-box/providers/${f}" ] && cp -af "${MODULE_CONFIG}/sing-box/providers/${f}" "${DATA_CONFIG}/sing-box/providers/${f}"
  done

  # Rules (.srs binary files)
  if [ -d "${MODULE_CONFIG}/sing-box/rules" ]; then
    find "${MODULE_CONFIG}/sing-box/rules" -type f -name "*.srs" | while read -r src; do
      rel="${src#${MODULE_CONFIG}/sing-box/rules/}"
      [ -f "${DATA_CONFIG}/sing-box/rules/${rel}" ] || cp -af "${src}" "${DATA_CONFIG}/sing-box/rules/${rel}"
    done
  fi

  # Board (yacd webui)
  if [ -d "${MODULE_CONFIG}/sing-box/board" ]; then
    cp -af "${MODULE_CONFIG}/sing-box/board/." "${DATA_CONFIG}/sing-box/board/" 2>/dev/null || true
  fi

  # Whitelist (default packages)
  [ -f "${MODULE_CONFIG}/whitelist/packages.json" ] && [ ! -f "${DATA_CONFIG}/whitelist/packages.json" ] && cp -af "${MODULE_CONFIG}/whitelist/packages.json" "${DATA_CONFIG}/whitelist/packages.json"

  # Generate proxyd config from heredoc
  generate_default_config
  # Create config.json from default-config.json
  cp -af "${DATA_CONFIG}/default-config.json" "${DATA_CONFIG}/config.json"

  # Generate x-tunnel nodes from heredoc
  generate_nodes_list

  # Set permissions
  chown -R 0:0 "${DATA_CONFIG}" 2>/dev/null
  find "${DATA_CONFIG}" -type d -exec chmod 0750 {} \; 2>/dev/null
  find "${DATA_CONFIG}" -type f -exec chmod 0640 {} \; 2>/dev/null
}

stop_active_module_before_update

set_perm_recursive "$MODPATH" 0 0 0755 0644
set_perm "$MODPATH/service.sh" 0 0 0755
set_perm "$MODPATH/action.sh" 0 0 0755
set_perm "$MODPATH/uninstall.sh" 0 0 0755
set_perm "$MODPATH/KsuProxy/scripts/ksu-proxy.sh" 0 0 0755
set_perm_recursive "$MODPATH/KsuProxy/bin" 0 0 0755 0755
set_perm_recursive "$MODPATH/KsuProxy/config" 0 0 0755 0640

# First install: generate config templates to data dir
# Upgrade: skip (user data dir preserved)
if [ ! -f "${DATA_CONFIG}/config.json" ] && [ ! -f "${DATA_CONFIG}/default-config.json" ]; then
  ui_print "- First install: initializing config templates"
  install_config_templates
else
  ui_print "- Upgrade detected: preserving user config"
fi

# Clean config files from module directory (no longer needed after install/upgrade)
rm -rf "${MODPATH}/KsuProxy/config/sing-box/providers" \
       "${MODPATH}/KsuProxy/config/sing-box/rules" \
       "${MODPATH}/KsuProxy/config/sing-box/board" \
       "${MODPATH}/KsuProxy/config/sing-box/config.example.jsonc" \
       "${MODPATH}/KsuProxy/config/whitelist"
ui_print "- Module directory cleaned"
