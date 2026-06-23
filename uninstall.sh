#!/system/bin/sh
MODDIR=${0%/*}
"${MODDIR}/KsuProxy/scripts/ksu-proxy.sh" stop >/dev/null 2>&1
rm -rf /data/adb/ksu-proxy
