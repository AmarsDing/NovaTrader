#!/usr/bin/env bash
set -euo pipefail
cd /home/dingsheng/novatrader/src/deploy
export SUDO_PASS=dingsheng
source scripts/_lib.sh
set -a; source env/.env; set +a

mkdir -p build-ctx/bin
cid=$(docker create novatrader-app:runtime)
docker cp "$cid:/app/bin/." build-ctx/bin/
docker rm "$cid"
cp docker/entrypoint.sh build-ctx/entrypoint.sh
chmod +x build-ctx/entrypoint.sh build-ctx/bin/*
cp docker/Dockerfile.runtime build-ctx/Dockerfile
docker build --build-arg ALPINE_MIRROR=mirrors.tuna.tsinghua.edu.cn -t novatrader-app:runtime build-ctx
rm -rf build-ctx

docker compose -f compose/base.compose.yml -f compose/app.compose.yml --env-file env/.env up -d --force-recreate app web

echo "waiting app..."
for i in $(seq 1 60); do
  st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
  [[ "$st" == healthy ]] && break
  [[ $i == 60 ]] && { docker logs --tail 80 nt-app; exit 1; }
  sleep 5
done

docker ps --filter name=nt- --format '{{.Names}} {{.Status}}'
bash scripts/smoke.sh
echo FIX_OK
