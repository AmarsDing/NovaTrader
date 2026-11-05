#!/usr/bin/env bash
# NovaTrader 一键部署（幂等）。在目标机：cd <src>/deploy && bash scripts/deploy.sh
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export SRC_DIR="$(cd "$DEPLOY_DIR/.." && pwd)"
cd "$DEPLOY_DIR"

[[ -f env/.env ]] || { echo "[deploy] 缺少 env/.env —— cp env/env.example env/.env 后填写"; exit 1; }
set -a; source env/.env; set +a

# shellcheck source=_lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"
# 子脚本继承 docker 包装（bash）
export -f docker 2>/dev/null || true
export SUDO_PASS="${SUDO_PASS:-dingsheng}"

DC="docker compose"
COMPOSE_BASE=(-f compose/base.compose.yml --env-file env/.env)
COMPOSE_APP=(-f compose/base.compose.yml -f compose/app.compose.yml --env-file env/.env)

DATA="${NT_DATA_ROOT:-/mnt/exdata/novatrader/data}"
mkdir -p "$DATA"/{postgres,redis,nats,app-logs,secrets}
mkdir -p /mnt/exdata/novatrader/pkg 2>/dev/null || true
# runtime 镜像用户 nt=10001；bind 挂载目录必须可写
chown -R 10001:10001 "$DATA/app-logs" "$DATA/secrets" 2>/dev/null || \
  echo "${SUDO_PASS:-dingsheng}" | sudo -S chown -R 10001:10001 "$DATA/app-logs" "$DATA/secrets"

echo "== [1/6] 渲染配置 =="
bash scripts/render_config.sh

echo "== [2/6] 拉取基础镜像 =="
bash scripts/pull-images.sh

echo "== [3/6] 基础设施 up =="
$DC "${COMPOSE_BASE[@]}" up -d

NEED_RECREATE=0
for c in nt-postgres nt-redis nt-nats; do
  docker inspect "$c" >/dev/null 2>&1 || continue
  docker inspect "$c" --format '{{range $k,$v := .NetworkSettings.Networks}}{{println $k}}{{end}}' \
    | grep -qx 'nt-net' || { NEED_RECREATE=1; echo "[deploy] ⚠ $c 未挂 nt-net"; }
done
if [[ "$NEED_RECREATE" == 1 ]]; then
  echo "[deploy] 强制重建 base 以对齐网络"
  $DC "${COMPOSE_BASE[@]}" up -d --force-recreate
fi

echo "== [4/6] 等待 base 健康 =="
for i in $(seq 1 40); do
  curl -sf -m 3 "http://127.0.0.1:${NATS_HTTP_PORT:-28222}/healthz" >/dev/null && break
  [[ $i == 40 ]] && { echo "[deploy] ✗ nt-nats 未就绪"; docker logs --tail 40 nt-nats; exit 1; }
  sleep 3
done
echo "[deploy] nt-nats ready ✓"
for c in nt-postgres nt-redis; do
  for i in $(seq 1 60); do
    st=$(docker inspect --format '{{.State.Health.Status}}' "$c" 2>/dev/null || echo missing)
    [[ "$st" == healthy ]] && break
    [[ $i == 60 ]] && { echo "[deploy] ✗ $c 未健康 ($st)"; docker logs --tail 50 "$c"; exit 1; }
    sleep 3
  done
  echo "[deploy] $c healthy ✓"
done

echo "== [5/6] 构建应用镜像并 up =="
bash scripts/build_image.sh
$DC "${COMPOSE_APP[@]}" up -d

for i in $(seq 1 60); do
  st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
  [[ "$st" == healthy ]] && break
  [[ $i == 60 ]] && { echo "[deploy] ✗ nt-app 未健康"; docker logs --tail 80 nt-app; exit 1; }
  sleep 5
done
echo "[deploy] nt-app healthy ✓"

echo "== [6/6] 冒烟 =="
bash scripts/smoke.sh
echo "[deploy] 完成"
