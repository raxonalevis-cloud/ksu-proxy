#!/system/bin/sh
MODDIR=$(dirname "$(dirname "$0")")
MODULE_ROOT=$(dirname "${MODDIR}")
DATA_DIR="/data/adb/ksu-proxy"
CONFIG_DIR="${DATA_DIR}/config"
RUN_DIR="${DATA_DIR}/run"
LOG_DIR="${DATA_DIR}/logs"
BACKUP_DIR="${DATA_DIR}/backups"
PROXYD="${MODDIR}/bin/arm64-v8a/proxyd"
CONFIG_FILE="${CONFIG_DIR}/config.json"
MODULE_CONFIG_VERSION_FILE="${MODDIR}/config/.module_config_version"
DATA_CONFIG_VERSION_FILE="${CONFIG_DIR}/.module_config_version"
RUN_PID="${RUN_DIR}/proxyd-run.pid"
WATCH_PID="${RUN_DIR}/module-watch.pid"
BASE_DESCRIPTION="Whitelist transparent proxy for KernelSU with sing-box and x-tunnel"

export PATH="/data/adb/ap/bin:/data/adb/ksu/bin:/data/adb/magisk:/system/bin:/system/xbin:$PATH"

# Create directories early so logging works even if ensure_data() fails
mkdir -p "${CONFIG_DIR}" "${RUN_DIR}" "${LOG_DIR}" "${DATA_DIR}/runtime" "${BACKUP_DIR}"

module_config_version() {
  if [ -f "${MODULE_CONFIG_VERSION_FILE}" ]; then
    cat "${MODULE_CONFIG_VERSION_FILE}" 2>/dev/null
  else
    echo "1"
  fi
}

data_config_version() {
  if [ -f "${DATA_CONFIG_VERSION_FILE}" ]; then
    cat "${DATA_CONFIG_VERSION_FILE}" 2>/dev/null
  fi
}

chmod_config() {
  chown -R 0:0 "${CONFIG_DIR}" 2>/dev/null
  find "${CONFIG_DIR}" -type d -exec chmod 0750 {} \; 2>/dev/null
  find "${CONFIG_DIR}" -type f -exec chmod 0640 {} \; 2>/dev/null
}

notify_user() {
  [ "${KSP_SILENT:-0}" = "1" ] && return 0
  msg="$*"
  [ -n "${msg}" ] || return 0
  am start -n re.tools/.main --es toast "${msg}" >/dev/null 2>&1 && return 0
  cmd notification post -S bigtext -t "KSU Proxy" ksu-proxy "${msg}" >/dev/null 2>&1 && return 0
  cmd notification post ksu-proxy "${msg}" >/dev/null 2>&1 || true
}

set_module_description() {
  state="$1"
  detail="$2"
  mkdir -p "${RUN_DIR}" "${LOG_DIR}"
  ts="$(date '+[%m-%d %H:%M]' 2>/dev/null)"
  [ -n "${ts}" ] || ts="[status]"
  # Map state to emoji
  emoji=""
  case "${state}" in
    STARTING) emoji="🟢" ;;
    RUNNING)  emoji="✅" ;;
    STOPPING) emoji="🟡" ;;
    STOPPED)  emoji="⏹️" ;;
    ERROR)    emoji="❌" ;;
  esac
  display="${emoji} ${state}"
  desc="description=${ts} ${display} - ${detail}; ${BASE_DESCRIPTION}"
  prop="${MODULE_ROOT}/module.prop"
  tmp="${RUN_DIR}/module.prop.$$"
  if [ -f "${prop}" ]; then
    if command -v awk >/dev/null 2>&1; then
      awk -v desc="${desc}" '
        BEGIN { done = 0 }
        /^description=/ { print desc; done = 1; next }
        { print }
        END { if (done == 0) print desc }
      ' "${prop}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${prop}"
    elif command -v sed >/dev/null 2>&1; then
      if grep -q '^description=' "${prop}" 2>/dev/null; then
        sed "s|^description=.*|${desc}|" "${prop}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${prop}"
      else
        cat "${prop}" >"${tmp}" 2>/dev/null && echo "${desc}" >>"${tmp}" && cat "${tmp}" >"${prop}"
      fi
    fi
    rm -f "${tmp}"
  fi
  echo "${ts} ${display} - ${detail}" >"${RUN_DIR}/status"
}

module_disabled() {
  [ -f "${MODULE_ROOT}/disable" ] || [ -f "${MODULE_ROOT}/remove" ]
}

module_disabled_reason() {
  if [ -f "${MODULE_ROOT}/remove" ]; then
    echo "module remove marker present"
  elif [ -f "${MODULE_ROOT}/disable" ]; then
    echo "module disable marker present"
  else
    echo "module disabled"
  fi
}

start_watcher() {
  mkdir -p "${RUN_DIR}" "${LOG_DIR}"
  if [ -f "${WATCH_PID}" ] && kill -0 "$(cat "${WATCH_PID}" 2>/dev/null)" >/dev/null 2>&1; then
    return 0
  fi
  rm -f "${WATCH_PID}"
  (
    last=""
    while true; do
      if module_disabled; then
        if [ "${last}" != "disabled" ]; then
          reason="$(module_disabled_reason)"
          # 先立即写入 STOPPING，让 KernelSU Manager 能及时看到过渡状态
          set_module_description "STOPPING" "${reason}"
          KSP_SILENT=1 KSP_STOP_REASON="${reason}" "$0" stop >/dev/null 2>&1
          set_module_description "STOPPED" "${reason}"
          notify_user "KSU Proxy stopped"
          last="disabled"
        fi
      else
        if [ "${last}" = "disabled" ]; then
          set_module_description "STARTING" "module enabled"
          notify_user "KSU Proxy starting"
          "$0" start >/dev/null 2>&1
        fi
        last="enabled"
      fi
      sleep 1
    done
  ) >/dev/null 2>&1 &
  echo "$!" >"${WATCH_PID}"
}

backup_config() {
  ts="$(date +%Y%m%d-%H%M%S 2>/dev/null)"
  [ -n "${ts}" ] || ts="current"
  backup="${BACKUP_DIR}/config-${ts}"
  mkdir -p "${backup}"
  cp -af "${CONFIG_DIR}/." "${backup}/" 2>/dev/null
}

migrate_admin_api_port() {
  if [ -f "${CONFIG_FILE}" ] && grep -q '"admin_api_listen"[[:space:]]*:[[:space:]]*"127.0.0.1:2080"' "${CONFIG_FILE}" 2>/dev/null; then
    tmp="${RUN_DIR}/config.json.$$"
    sed 's|"admin_api_listen"[[:space:]]*:[[:space:]]*"127.0.0.1:2080"|"admin_api_listen": "127.0.0.1:9099"|' "${CONFIG_FILE}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${CONFIG_FILE}"
    rm -f "${tmp}"
  fi
}

migrate_firewall_backend_auto() {
  if [ -f "${CONFIG_FILE}" ] && grep -q '"backend"[[:space:]]*:[[:space:]]*"iptables"' "${CONFIG_FILE}" 2>/dev/null; then
    tmp="${RUN_DIR}/firewall-backend.$$"
    sed 's|"backend"[[:space:]]*:[[:space:]]*"iptables"|"backend": "auto"|' "${CONFIG_FILE}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${CONFIG_FILE}"
    rm -f "${tmp}"
  fi
}

migrate_firewall_mark() {
  if [ -f "${CONFIG_FILE}" ] && {
    grep -q '"mark"[[:space:]]*:[[:space:]]*"0x10000000/0xffffffff"' "${CONFIG_FILE}" 2>/dev/null ||
    grep -q '"mark"[[:space:]]*:[[:space:]]*"0x4b535550/0xffffffff"' "${CONFIG_FILE}" 2>/dev/null ||
    grep -q '"mark"[[:space:]]*:[[:space:]]*"0x12000000/0xffffffff"' "${CONFIG_FILE}" 2>/dev/null
  }; then
    tmp="${RUN_DIR}/firewall-mark.$$"
    sed \
      -e 's|"mark"[[:space:]]*:[[:space:]]*"0x10000000/0xffffffff"|"mark": "0x12000000/0xff000000"|' \
      -e 's|"mark"[[:space:]]*:[[:space:]]*"0x4b535550/0xffffffff"|"mark": "0x12000000/0xff000000"|' \
      -e 's|"mark"[[:space:]]*:[[:space:]]*"0x12000000/0xffffffff"|"mark": "0x12000000/0xff000000"|' \
      "${CONFIG_FILE}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${CONFIG_FILE}"
    rm -f "${tmp}"
  fi
}

migrate_xtunnel_dns_url() {
  if [ -f "${CONFIG_FILE}" ] && grep -q '"default_dns"[[:space:]]*:[[:space:]]*"223\.5\.5\.5/dns-query"' "${CONFIG_FILE}" 2>/dev/null; then
    tmp="${RUN_DIR}/dns-url.$$"
    sed 's|"default_dns"[[:space:]]*:[[:space:]]*"223\.5\.5\.5/dns-query"|"default_dns": "https://223.5.5.5/dns-query"|' "${CONFIG_FILE}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${CONFIG_FILE}"
    rm -f "${tmp}"
  fi
  nodes="${CONFIG_DIR}/x-tunnel/nodes.list"
  if [ -f "${nodes}" ] && grep -q '223\.5\.5\.5/dns-query' "${nodes}" 2>/dev/null; then
    tmp="${RUN_DIR}/dns-url.$$"
    sed \
      -e 's#^@default_dns=223\.5\.5\.5/dns-query#@default_dns=https://223.5.5.5/dns-query#' \
      -e 's#|223\.5\.5\.5/dns-query#|https://223.5.5.5/dns-query#g' \
      "${nodes}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${nodes}"
    rm -f "${tmp}"
  fi
}

migrate_singbox_independent_cache() {
  sb_config="${CONFIG_DIR}/sing-box/config.json"
  [ -f "${sb_config}" ] || return 0
  grep -q '"independent_cache"' "${sb_config}" 2>/dev/null || return 0
  if command -v python3 >/dev/null 2>&1; then
    python3 -c "
import json, sys
p = json.load(open(sys.argv[1]))
dns = p.get('dns', {})
dns.pop('independent_cache', None)
p['dns'] = dns
json.dump(p, open(sys.argv[1], 'w'), indent=2, ensure_ascii=False)
print('\n', end='')
" "${sb_config}" 2>/dev/null && return 0
  fi
  tmp="${RUN_DIR}/singbox-migrate.$$"
  sed '/"independent_cache"/d' "${sb_config}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${sb_config}"
  rm -f "${tmp}"
}

migrate_singbox_domain_resolver() {
  sb_config="${CONFIG_DIR}/sing-box/config.json"
  [ -f "${sb_config}" ] || return 0
  grep -q '"default_domain_resolver"' "${sb_config}" 2>/dev/null && return 0
  if command -v python3 >/dev/null 2>&1; then
    python3 -c "
import json, sys
p = json.load(open(sys.argv[1]))
r = p.get('route', {})
if 'default_domain_resolver' not in r:
    r['default_domain_resolver'] = 'dns-direct'
p['route'] = r
json.dump(p, open(sys.argv[1], 'w'), indent=2, ensure_ascii=False)
print('\n', end='')
" "${sb_config}" 2>/dev/null && return 0
  fi
  tmp="${RUN_DIR}/singbox-migrate.$$"
  sed 's|"find_process": true,|"find_process": true,\n    "default_domain_resolver": "dns-direct",|' "${sb_config}" >"${tmp}" 2>/dev/null && cat "${tmp}" >"${sb_config}"
  rm -f "${tmp}"
}

migrate_singbox_providers() {
  sb_config="${CONFIG_DIR}/sing-box/config.json"
  [ -f "${sb_config}" ] || return 0
  command -v python3 >/dev/null 2>&1 || return 0
  python3 -c "
import json, sys
changed = False
p = json.load(open(sys.argv[1]))
for prov in p.get('providers', []):
    if prov.get('type') == 'remote' and prov.get('url', '').startswith('https://example.com'):
        prov['type'] = 'local'
        prov.pop('url', None)
        prov.pop('user_agent', None)
        prov.pop('download_detour', None)
        changed = True
    prov.pop('download_detour', None)
if changed:
    json.dump(p, open(sys.argv[1], 'w'), indent=2, ensure_ascii=False)
    print('\n', end='')
" "${sb_config}" 2>/dev/null
}

sync_reference_docs() {
  mkdir -p "${CONFIG_DIR}/sing-box" "${CONFIG_DIR}/x-tunnel" "${CONFIG_DIR}/whitelist"
  for rel in \
    "README.md" \
    "config.example.jsonc" \
    "default-config.json" \
    "sing-box/README.md" \
    "sing-box/config.example.jsonc" \
    "x-tunnel/README.md" \
    "x-tunnel/nodes.example.list" \
    "whitelist/README.md"; do
    if [ -f "${MODDIR}/config/${rel}" ]; then
      cp -af "${MODDIR}/config/${rel}" "${CONFIG_DIR}/${rel}" 2>/dev/null
    fi
  done
}

ensure_singbox_config() {
  singbox_config="${CONFIG_DIR}/sing-box/config.json"
  [ -f "${singbox_config}" ] && return 0
  mkdir -p "${CONFIG_DIR}/sing-box"
  # Generate default sing-box config (strip comments from example)
  example="${MODDIR}/config/sing-box/config.example.jsonc"
  if [ -f "${example}" ] && command -v python3 >/dev/null 2>&1; then
    python3 -c "
import json, sys
txt = open(sys.argv[1]).read()
out = []
i, n = 0, len(txt)
while i < n:
  c = txt[i]
  if c == '\"':
    j = i + 1
    while j < n and txt[j] != '\"':
      if txt[j] == '\\\\': j += 1
      j += 1
    out.append(txt[i:j+1]); i = j + 1
  elif c == '/' and i+1 < n and txt[i+1] == '/':
    while i < n and txt[i] != '\n': i += 1
  elif c == '/' and i+1 < n and txt[i+1] == '*':
    i += 2
    while i+1 < n and not (txt[i] == '*' and txt[i+1] == '/'): i += 1
    i += 2
  else:
    out.append(c); i += 1
result = ''.join(out)
result = __import__('re').sub(r',\s*([}\]])', r'\1', result)
parsed = json.loads(result)
open(sys.argv[2], 'w').write(json.dumps(parsed, indent=2, ensure_ascii=False) + '\n')
" "${example}" "${singbox_config}" 2>/dev/null && return 0
    rm -f "${singbox_config}"
  fi
  # Fallback: write minimal config directly
  cat > "${singbox_config}" << 'SINGBOX_EOF'
{
  "log": {"disabled": false, "level": "info", "output": "/data/adb/ksu-proxy/logs/sing-box-core.log", "timestamp": true},
  "dns": {
    "servers": [
      {"type": "https", "tag": "cfworkers", "server": "1.1.1.1", "path": "/dns-query", "detour": "🚀代理"},
      {"type": "https", "tag": "dns-direct", "server": "223.5.5.5", "path": "/dns-query"},
      {"type": "fakeip", "tag": "fakeip", "inet4_range": "198.18.0.0/15", "inet6_range": "2001:db8::/32"}
    ],
    "rules": [
      {"rule_set": "anti-ad", "action": "reject", "method": "drop"},
      {"query_type": ["A", "AAAA"], "rule_set": ["geosite-geolocation-!cn"], "server": "fakeip"},
      {"rule_set": ["geosite-private", "geosite-tld-cn", "geosite-cn", "geosite-geolocation-cn"], "server": "dns-direct"}
    ],
    "strategy": "prefer_ipv4",
    "final": "cfworkers"
  },
  "inbounds": [],
  "outbounds": [
    {"type": "selector", "tag": "🚀代理", "use_all_providers": true, "outbounds": ["out-direct"], "default": "out-direct", "interrupt_exist_connections": true},
    {"type": "selector", "tag": "📲telegram", "use_all_providers": true, "include": " telegram |tg|电报", "outbounds": ["🚀代理", "out-direct"], "default": "🚀代理", "interrupt_exist_connections": true},
    {"type": "selector", "tag": "🐠漏网之鱼", "use_all_providers": true, "outbounds": ["🚀代理", "out-direct"], "default": "🚀代理", "interrupt_exist_connections": true},
    {"type": "selector", "tag": "🎁收集", "providers": ["搜集1", "搜集2"], "outbounds": ["out-direct"], "default": "out-direct", "interrupt_exist_connections": true},
    {"type": "direct", "tag": "out-direct"}
  ],
  "route": {
    "rules": [
      {"action": "sniff"},
      {"protocol": "dns", "action": "hijack-dns"},
      {"rule_set": "anti-ad", "action": "reject", "method": "drop"},
      {"rule_set": ["geosite-telegram", "geoip-telegram"], "outbound": "📲telegram"},
      {"rule_set": ["geosite-geolocation-!cn"], "outbound": "🚀代理"},
      {"rule_set": ["geosite-private"], "outbound": "out-direct"},
      {"rule_set": ["geosite-tld-cn"], "outbound": "out-direct"},
      {"rule_set": ["geosite-cn"], "outbound": "out-direct"},
      {"rule_set": ["geosite-geolocation-cn"], "outbound": "out-direct"},
      {"rule_set": ["geoip-cn"], "outbound": "out-direct"},
      {"ip_is_private": true, "outbound": "out-direct"},
      {"outbound": "🐠漏网之鱼"}
    ],
    "rule_set": [
      {"type": "local", "tag": "anti-ad", "format": "binary", "path": "rules/anti-ad.srs"},
      {"type": "local", "tag": "geoip-cn", "format": "binary", "path": "rules/geoip-cn.srs"},
      {"type": "local", "tag": "geoip-telegram", "format": "binary", "path": "rules/geoip-telegram.srs"},
      {"type": "local", "tag": "geosite-cn", "format": "binary", "path": "rules/geosite-cn.srs"},
      {"type": "local", "tag": "geosite-geolocation-cn", "format": "binary", "path": "rules/geosite-geolocation-cn.srs"},
      {"type": "local", "tag": "geosite-geolocation-!cn", "format": "binary", "path": "rules/geosite-geolocation-!cn.srs"},
      {"type": "local", "tag": "geosite-private", "format": "binary", "path": "rules/geosite-private.srs"},
      {"type": "local", "tag": "geosite-tld-cn", "format": "binary", "path": "rules/geosite-tld-cn.srs"},
      {"type": "local", "tag": "geosite-telegram", "format": "binary", "path": "rules/geosite-telegram.srs"}
    ],
    "find_process": true,
    "auto_detect_interface": true,
    "default_domain_resolver": "dns-direct",
    "final": "🚀代理"
  },
  "providers": [
    {"type": "remote", "tag": "xb", "url": "https://example.com/your-subscription-url", "path": "./providers/ruite.json", "http_client": {"detour": "🎁收集"}, "health_check": {"enabled": true, "url": "https://www.google.com/generate_204", "interval": "10m", "timeout": "3s"}},
    {"tag": "搜集1", "type": "local", "path": "./providers/v2ray.txt", "health_check": {"enabled": true, "url": "https://www.google.com/generate_204", "interval": "10m0s", "timeout": "3s"}},
    {"tag": "搜集2", "type": "local", "path": "./providers/clash.yaml", "health_check": {"enabled": true, "url": "https://www.google.com/generate_204", "interval": "10m0s", "timeout": "3s"}}
  ],
  "experimental": {
    "cache_file": {"enabled": true},
    "clash_api": {"external_controller": "127.0.0.1:9090", "external_ui": "/data/adb/ksu-proxy/config/sing-box/board", "external_ui_download_url": "https://srs.acstudycn.eu.org/gh-pages.zip", "external_ui_download_detour": "out-direct"}
  }
}
SINGBOX_EOF
}

ensure_data() {
  mkdir -p "${CONFIG_DIR}" "${RUN_DIR}" "${LOG_DIR}" "${DATA_DIR}/runtime" "${BACKUP_DIR}"
  module_version="$(module_config_version)"
  data_version="$(data_config_version)"

  # On version upgrade: backup, sync reference docs, update version marker
  if [ -n "${data_version}" ] && [ "${module_version}" != "${data_version}" ]; then
    backup_config
    sync_reference_docs
    echo "${module_version}" >"${DATA_CONFIG_VERSION_FILE}"
    chmod_config
  fi

  # Run all migrations on data dir config
  migrate_admin_api_port
  migrate_firewall_backend_auto
  migrate_firewall_mark
  migrate_xtunnel_dns_url
  migrate_singbox_independent_cache
  migrate_singbox_domain_resolver
  migrate_singbox_providers

  # Ensure directories exist
  mkdir -p "${CONFIG_DIR}/sing-box/providers" "${CONFIG_DIR}/sing-box/rules" "${CONFIG_DIR}/x-tunnel" "${CONFIG_DIR}/whitelist"

  # Fallback: if config.json missing, generate from default template
  if [ ! -f "${CONFIG_FILE}" ]; then
    if [ -f "${CONFIG_DIR}/default-config.json" ]; then
      cp -af "${CONFIG_DIR}/default-config.json" "${CONFIG_FILE}"
    else
      # Generate default-config.json from heredoc if missing
      mkdir -p "${CONFIG_DIR}"
      cat > "${CONFIG_DIR}/default-config.json" << 'DEFAULTCONF_EOF'
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
    "dry_run": false,
    "block_loopback": true,
    "ipv6_mode": "auto",
    "dns_route": false
  },
  "runtime": {
    "poll_interval_seconds": 2,
    "start_grace_millis": 500,
    "admin_api_listen": "127.0.0.1:9099",
    "module_dir": "/data/adb/modules/ksu-proxy",
    "scheduled_restart": true
  },
  "update": {
    "enabled": true,
    "repo": "raxonalevis-cloud/ksu-proxy",
    "check_interval_hours": 24
  }
}
DEFAULTCONF_EOF
      cp -af "${CONFIG_DIR}/default-config.json" "${CONFIG_FILE}"
    fi
  fi

  # Auto-generate sing-box config if missing (v0.2.6+)
  ensure_singbox_config
  chmod_config
}

require_proxyd() {
  if [ ! -x "${PROXYD}" ]; then
    echo "proxyd is missing or not executable: ${PROXYD}" >>"${LOG_DIR}/service.log"
    set_module_description "ERROR" "proxyd missing"
    notify_user "KSU Proxy proxyd missing"
    exit 1
  fi
}

is_running_proxyd() {
  pid="$1"
  [ -n "${pid}" ] || return 1
  [ -d "/proc/${pid}" ] || return 1
  tr '\000' ' ' <"/proc/${pid}/cmdline" 2>/dev/null | grep -q "proxyd"
}

# 清理所有残留的防火墙规则和策略路由。
# 表名/链名必须与 Go 代码 firewall/manager.go 中的 nftTableName() 和
# KSP_ 前缀保持一致：nft 表 ksp_proxy，iptables 链 KSP_LOCAL / KSP_TPROXY /
# KSP_HOTSPOT / KSP_QUIC / KSP_BYPASS。
cleanup_firewall_and_routes() {
  if command -v nft >/dev/null 2>&1; then
    nft flush table inet ksp_proxy 2>/dev/null || true
    nft delete table inet ksp_proxy 2>/dev/null || true
  fi
  if command -v iptables >/dev/null 2>&1; then
    for t in mangle nat filter; do
      for ch in KSP_LOCAL KSP_TPROXY KSP_HOTSPOT KSP_QUIC KSP_LOOPBACK KSP_BYPASS; do
        iptables -t "${t}" -F "${ch}" 2>/dev/null || true
        iptables -t "${t}" -X "${ch}" 2>/dev/null || true
      done
    done
  fi
  if command -v ip6tables >/dev/null 2>&1; then
    for t in mangle nat filter; do
      for ch in KSP_LOCAL KSP_TPROXY KSP_HOTSPOT KSP_QUIC KSP_LOOPBACK KSP_BYPASS; do
        ip6tables -t "${t}" -F "${ch}" 2>/dev/null || true
        ip6tables -t "${t}" -X "${ch}" 2>/dev/null || true
      done
    done
  fi
  ip route flush table 2025 2>/dev/null || true
  ip -6 route flush table 2025 2>/dev/null || true
}

# IPv6 控制函数
enable_ipv6() {
  sysctl -w net.ipv4.ip_forward=1 2>/dev/null || true
  sysctl -w net.ipv6.conf.all.forwarding=1 2>/dev/null || true
  sysctl -w net.ipv6.conf.all.accept_ra=2 2>/dev/null || true
  sysctl -w net.ipv6.conf.all.disable_ipv6=0 2>/dev/null || true
  sysctl -w net.ipv6.conf.default.disable_ipv6=0 2>/dev/null || true
}

disable_ipv6() {
  sysctl -w net.ipv4.ip_forward=1 2>/dev/null || true
  sysctl -w net.ipv6.conf.all.forwarding=0 2>/dev/null || true
  sysctl -w net.ipv6.conf.all.accept_ra=0 2>/dev/null || true
  sysctl -w net.ipv6.conf.all.disable_ipv6=1 2>/dev/null || true
  sysctl -w net.ipv6.conf.default.disable_ipv6=1 2>/dev/null || true
}

# 根据配置设置 IPv6 模式
apply_ipv6_mode() {
  mode="${1:-auto}"
  case "${mode}" in
    enable)
      enable_ipv6
      ;;
    disable)
      disable_ipv6
      ;;
    auto|*)
      # auto 模式：保持当前状态，不做任何操作
      ;;
  esac
}

# 定时重启 sing-box 服务
scheduled_restart_singbox() {
  # 获取当前小时和分钟
  hour=$(date +%H)
  min=$(date +%M)
  current_time="${hour}:${min}"

  # 检查是否在计划重启时间
  if [ "${current_time}" = "01:11" ] || [ "${current_time}" = "13:23" ]; then
    # 避免重复重启（检查上次重启时间）
    last_restart_file="${RUN_DIR}/last_scheduled_restart"
    if [ -f "${last_restart_file}" ]; then
      last_restart=$(cat "${last_restart_file}" 2>/dev/null)
      if [ "${last_restart}" = "${current_time}" ]; then
        return 0
      fi
    fi

    echo "$(date '+%Y-%m-%d %H:%M:%S') Scheduled restart at ${current_time}" >>"${LOG_DIR}/service.log"

    # 重启 sing-box 服务
    if [ -x "${PROXYD}" ]; then
      "${PROXYD}" -config "${CONFIG_FILE}" stop >>"${LOG_DIR}/service.log" 2>&1 || true
      sleep 2
      nohup "${PROXYD}" -config "${CONFIG_FILE}" run >>"${LOG_DIR}/service.log" 2>&1 &
      echo "$!" >"${RUN_PID}"
      echo "${current_time}" >"${last_restart_file}"
      echo "$(date '+%Y-%m-%d %H:%M:%S') sing-box restarted successfully" >>"${LOG_DIR}/service.log"
    fi
  fi
}

# 启动定时重启守护进程
start_scheduled_restart() {
  # 检查是否启用定时重启
  scheduled_enabled="false"
  if [ -f "${CONFIG_FILE}" ] && command -v python3 >/dev/null 2>&1; then
    scheduled_enabled=$(python3 -c "import json; c=json.load(open('${CONFIG_FILE}')); print(c.get('runtime',{}).get('scheduled_restart','false'))" 2>/dev/null || echo "false")
  fi

  if [ "${scheduled_enabled}" = "true" ] || [ "${scheduled_enabled}" = "True" ]; then
    # 启动后台守护进程，每分钟检查一次
    (
      while true; do
        scheduled_restart_singbox
        sleep 60
      done
    ) >/dev/null 2>&1 &
    echo $! >"${RUN_DIR}/scheduled-restart.pid"
  fi
}

# 停止定时重启守护进程
stop_scheduled_restart() {
  if [ -f "${RUN_DIR}/scheduled-restart.pid" ]; then
    pid=$(cat "${RUN_DIR}/scheduled-restart.pid" 2>/dev/null)
    if [ -n "${pid}" ] && [ -d "/proc/${pid}" ]; then
      kill "${pid}" >/dev/null 2>&1 || true
    fi
    rm -f "${RUN_DIR}/scheduled-restart.pid"
  fi
}

write_start_failure_report() {
  mkdir -p "${LOG_DIR}"
  report="${LOG_DIR}/start-failure.log"
  {
    echo "KSU Proxy start failure report"
    echo "time=$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)"
    echo "module_root=${MODULE_ROOT}"
    echo "config_file=${CONFIG_FILE}"
    for marker in disable remove; do
      if [ -f "${MODULE_ROOT}/${marker}" ]; then
        echo "marker=${MODULE_ROOT}/${marker}"
      fi
    done
    for path in \
      "${LOG_DIR}/service.log" \
      "${LOG_DIR}/proxyd.log" \
      "${LOG_DIR}/sing-box.log" \
      "${LOG_DIR}/sing-box-core.log"; do
      echo
      echo "===== tail ${path} ====="
      if [ -f "${path}" ]; then
        tail -n 120 "${path}" 2>/dev/null || cat "${path}" 2>/dev/null
      else
        echo "(missing)"
      fi
    done
  } >"${report}" 2>/dev/null
}

start_run() {
  ensure_data
  # 根据配置设置 IPv6 模式
  ipv6_mode="auto"
  if [ -f "${CONFIG_FILE}" ] && command -v python3 >/dev/null 2>&1; then
    ipv6_mode=$(python3 -c "import json; c=json.load(open('${CONFIG_FILE}')); print(c.get('firewall',{}).get('ipv6_mode','auto'))" 2>/dev/null || echo "auto")
  fi
  apply_ipv6_mode "${ipv6_mode}"
  # 启动前清理可能残留的防火墙规则，避免冲突
  cleanup_firewall_and_routes
  if module_disabled; then
    set_module_description "STOPPED" "$(module_disabled_reason)"
    exit 0
  fi
  start_watcher
  require_proxyd
  if [ -f "${RUN_PID}" ] && is_running_proxyd "$(cat "${RUN_PID}" 2>/dev/null)"; then
    set_module_description "RUNNING" "already active"
    exit 0
  fi
  rm -f "${RUN_PID}"
  set_module_description "STARTING" "starting service"
  nohup "${PROXYD}" -config "${CONFIG_FILE}" run >>"${LOG_DIR}/service.log" 2>&1 &
  echo "$!" >"${RUN_PID}"
  sleep 1
  if ! is_running_proxyd "$(cat "${RUN_PID}" 2>/dev/null)"; then
    write_start_failure_report
    set_module_description "ERROR" "service failed; see logs/start-failure.log"
    notify_user "KSU Proxy start failed"
    exit 1
  fi
  set_module_description "RUNNING" "service active"
  notify_user "KSU Proxy started"
  # 启动定时重启守护进程
  start_scheduled_restart
}

case "$1" in
  run|start)
    start_run
    ;;
  stop)
    # ⬇️ 立即写入 STOPPING，并在整个停止过程中保持此状态
    set_module_description "STOPPING" "stopping service"
    stop_scheduled_restart
    sync
    if [ -x "${PROXYD}" ]; then
      "${PROXYD}" -config "${CONFIG_FILE}" stop >>"${LOG_DIR}/service.log" 2>&1
    fi
    if [ -f "${RUN_PID}" ]; then
      pid="$(cat "${RUN_PID}" 2>/dev/null)"
      if is_running_proxyd "${pid}"; then
        kill "${pid}" >/dev/null 2>&1
        waited=0
        while is_running_proxyd "${pid}" && [ "${waited}" -lt 2 ]; do
          sleep 1
          waited=$((waited + 1))
        done
        if is_running_proxyd "${pid}"; then
          kill -9 "${pid}" >/dev/null 2>&1
        fi
      fi
      rm -f "${RUN_PID}"
    fi
    # 安全兜底：确保 proxyd 异常退出时残留的规则也能被清理
    cleanup_firewall_and_routes
    stop_detail="${KSP_STOP_REASON:-service stopped}"
    set_module_description "STOPPED" "${stop_detail}"
    notify_user "KSU Proxy stopped"
    ;;
  restart)
    KSP_SILENT=1 "$0" stop
    # 确保端口完全释放，避免新实例绑定冲突
    sleep 1
    start_run
    ;;
  reconcile)
    ensure_data
    require_proxyd
    "${PROXYD}" -config "${CONFIG_FILE}" reconcile >>"${LOG_DIR}/service.log" 2>&1
    ;;
  status)
    ensure_data
    require_proxyd
    "${PROXYD}" -config "${CONFIG_FILE}" status
    ;;
  *)
    echo "usage: $0 run|stop|restart|reconcile|status"
    exit 2
    ;;
esac
