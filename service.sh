#!/system/bin/sh
MODDIR=${0%/*}

# 如果模块被禁用，不启动任何服务
[ -f "${MODDIR}/disable" ] && exit 0

(
  until [ "$(getprop sys.boot_completed)" = "1" ]; do
    sleep 2
  done
  "${MODDIR}/KsuProxy/scripts/ksu-proxy.sh" run
) &
