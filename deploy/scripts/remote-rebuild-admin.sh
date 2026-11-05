#!/usr/bin/env bash
# 仅重编 admin 并打进 novatrader-app:runtime（其它二进制复用）
set -euo pipefail
export SUDO_PASS=dingsheng
cd /home/dingsheng/novatrader/src/deploy
source scripts/_lib.sh
set -a; source env/.env; set +a
export SRC_DIR=/home/dingsheng/novatrader/src

# 同步本机已改的 gate.go 应已在 src 树；若仅改超时，确保文件在
echo "== build admin =="
docker run --rm \
  -e GOPROXY=https://goproxy.cn,direct \
  -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=amd64 \
  -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
  -v "$SRC_DIR/backend:/src" -w /src \
  -v nt_gocache:/go -v nt_gomodcache:/go/pkg/mod \
  "${GOLANG_IMAGE:-golang:1.26}" \
  sh -c 'go build -buildvcs=false -trimpath -ldflags "-s -w" -o /src/bin_deploy/admin ./app/admin/cmd && chown -R "$HOST_UID:$HOST_GID" /src/bin_deploy'

mkdir -p build-ctx/bin
cid=$(docker create novatrader-app:runtime)
docker cp "$cid:/app/bin/." build-ctx/bin/
docker rm "$cid"
cp "$SRC_DIR/backend/bin_deploy/admin" build-ctx/bin/admin
chmod +x build-ctx/bin/*
cp docker/entrypoint.sh build-ctx/entrypoint.sh
chmod +x build-ctx/entrypoint.sh
cp docker/Dockerfile.runtime build-ctx/Dockerfile
docker build --build-arg ALPINE_MIRROR=mirrors.tuna.tsinghua.edu.cn -t novatrader-app:runtime build-ctx
rm -rf build-ctx "$SRC_DIR/backend/bin_deploy"

docker compose -f compose/base.compose.yml -f compose/app.compose.yml --env-file env/.env up -d --force-recreate app
for i in $(seq 1 40); do
  st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
  [[ "$st" == healthy ]] && break
  sleep 3
done
docker inspect --format '{{.State.Health.Status}}' nt-app
curl -sf http://127.0.0.1:22001/healthz; echo
echo REBUILD_ADMIN_OK
