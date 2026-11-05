#!/usr/bin/env bash
set -euo pipefail
cd /home/dingsheng/novatrader/src/deploy
export SRC_DIR=/home/dingsheng/novatrader/src
export SUDO_PASS=dingsheng
source scripts/_lib.sh
export -f docker 2>/dev/null || true
set -a; source env/.env; set +a

echo "== continue: build images =="
bash scripts/build_image.sh

echo "== continue: app up =="
docker compose -f compose/base.compose.yml -f compose/app.compose.yml --env-file env/.env up -d

echo "== continue: wait app =="
for i in $(seq 1 60); do
  st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
  [[ "$st" == healthy ]] && break
  [[ $i == 60 ]] && { echo "nt-app not healthy"; docker logs --tail 100 nt-app; exit 1; }
  sleep 5
done
echo "nt-app healthy"

bash scripts/smoke.sh
echo CONTINUE_OK
