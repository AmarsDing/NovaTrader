#!/usr/bin/env bash
# 经 daocloud 拉取依赖镜像并 retag；不改 daemon.json（避免重启 docker）。
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=_lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"
[[ -f "$DEPLOY_DIR/env/.env" ]] && { set -a; source "$DEPLOY_DIR/env/.env"; set +a; }
MIRROR="${NT_MIRROR:-docker.m.daocloud.io/library}"

pull_one() {
  local img="$1" src="${2:-}"
  if docker image inspect "$img" >/dev/null 2>&1; then
    echo "[pull] 既有 ✓ $img"
    return 0
  fi
  if [[ -z "$src" ]]; then
    case "$img" in
      */*) src="docker.m.daocloud.io/$img" ;;
      *) src="$MIRROR/$img" ;;
    esac
  fi
  echo "[pull] $src → $img"
  docker pull "$src"
  docker tag "$src" "$img"
  docker rmi "$src" >/dev/null 2>&1 || true
}

pull_one "${PG_IMAGE:-pgvector/pgvector:pg18}" "${NT_PGVECTOR_MIRROR:-docker.m.daocloud.io/pgvector/pgvector:pg18}"
pull_one "${REDIS_IMAGE:-redis:7}"
pull_one "${NATS_IMAGE:-nats:2.10}"
pull_one "${ALPINE_IMAGE:-alpine:latest}"
pull_one "${GOLANG_IMAGE:-golang:1.26}"
pull_one "${PYTHON_IMAGE:-python:3.11-slim}"
pull_one "${NGINX_IMAGE:-nginx:1.28.1-alpine}"
pull_one "${NODE_IMAGE:-node:20-bookworm}"
echo "[pull] 完成"
