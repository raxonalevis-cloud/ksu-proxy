#!/system/bin/sh
# TTLink 风格：echo 输出被 KernelSU Manager 捕获展示为进度页面
# 同步执行（非异步），KSU 会显示进度文字直到脚本退出
MODDIR=${0%/*}

if [ ! -f "${MODDIR}/disable" ]; then
  echo "🔄 KSU Proxy 重启中..."
  echo "   ⏹️ 停止服务 → ⏸️ 等待端口释放 → 🚀 启动服务"
  "${MODDIR}/KsuProxy/scripts/ksu-proxy.sh" restart
  echo "✅ 重启完成"
else
  echo "⚠️ 模块已禁用，无法重启"
  sleep 1
fi
