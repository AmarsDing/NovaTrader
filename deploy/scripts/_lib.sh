# shellcheck shell=bash
# 供 deploy/smoke/build/pull/render source：统一 docker 权限（44 dingsheng 无 docker 组）
if ! docker info >/dev/null 2>&1; then
  docker() {
    echo "${SUDO_PASS:-dingsheng}" | sudo -S -p '' docker "$@"
  }
fi
