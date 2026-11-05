#!/usr/bin/env bash
# 构建 novatrader-app / pyworker / web 三镜像
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="${SRC_DIR:?请设置 SRC_DIR}"
# shellcheck source=_lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"
[[ -f "$DEPLOY_DIR/env/.env" ]] && { set -a; source "$DEPLOY_DIR/env/.env"; set +a; }

APP_IMG="${NT_APP_IMAGE:-novatrader-app:runtime}"
PY_IMG="${NT_PYWORKER_IMAGE:-novatrader-pyworker:runtime}"
WEB_IMG="${NT_WEB_IMAGE:-novatrader-web:runtime}"
GOLANG_IMAGE="${GOLANG_IMAGE:-golang:1.26}"
NODE_IMAGE="${NODE_IMAGE:-node:20-bookworm}"
ALPINE_MIRROR="${ALPINE_MIRROR:-mirrors.tuna.tsinghua.edu.cn}"

BUILD_CTX="$DEPLOY_DIR/build-ctx"
rm -rf "$BUILD_CTX"
mkdir -p "$BUILD_CTX/bin"

SERVICES=(admin datahub market intel brain strategy risk backtest trade notify ntmcp)

echo "== [build] Go 二进制（$GOLANG_IMAGE）=="
if [[ -d "$DEPLOY_DIR/prebuilt" ]] && [[ -x "$DEPLOY_DIR/prebuilt/admin" ]]; then
  echo "[build] 使用 prebuilt/"
  cp "$DEPLOY_DIR/prebuilt/"* "$BUILD_CTX/bin/" || true
  chmod +x "$BUILD_CTX/bin/"*
else
  docker run --rm \
    -e GOPROXY=https://goproxy.cn,direct \
    -e CGO_ENABLED=0 -e GOOS=linux -e GOARCH=amd64 \
    -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
    -v "$SRC_DIR/backend:/src" -w /src \
    -v nt_gocache:/go -v nt_gomodcache:/go/pkg/mod \
    "$GOLANG_IMAGE" \
    sh -c '
      set -e
      mkdir -p /src/bin_deploy
      for s in admin datahub market intel brain strategy risk backtest trade notify; do
        echo "  go build $s"
        go build -buildvcs=false -trimpath -ldflags "-s -w" -o "/src/bin_deploy/$s" "./app/$s/cmd"
      done
      # nt-mcp 机器面（三仓联动 P3）：入口在 backend/cmd/ntmcp，不在 app/ 下
      echo "  go build ntmcp"
      go build -buildvcs=false -trimpath -ldflags "-s -w" -o "/src/bin_deploy/ntmcp" "./cmd/ntmcp"
      chown -R "$HOST_UID:$HOST_GID" /src/bin_deploy
    '
  cp "$SRC_DIR/backend/bin_deploy/"* "$BUILD_CTX/bin/"
  rm -rf "$SRC_DIR/backend/bin_deploy"
  chmod +x "$BUILD_CTX/bin/"*
fi

for s in "${SERVICES[@]}"; do
  [[ -x "$BUILD_CTX/bin/$s" ]] || { echo "[build] 缺少二进制 $s"; exit 1; }
done

cp "$DEPLOY_DIR/docker/entrypoint.sh" "$BUILD_CTX/entrypoint.sh"
chmod +x "$BUILD_CTX/entrypoint.sh"
cp "$DEPLOY_DIR/docker/Dockerfile.runtime" "$BUILD_CTX/Dockerfile"
docker build --build-arg "ALPINE_MIRROR=$ALPINE_MIRROR" -t "$APP_IMG" "$BUILD_CTX"
echo "[build] OK → $APP_IMG"

echo "== [build] pyworker =="
PY_CTX="$DEPLOY_DIR/build-ctx-py"
rm -rf "$PY_CTX" && mkdir -p "$PY_CTX"
cp "$SRC_DIR/pyworker/requirements.txt" "$SRC_DIR/pyworker/app.py" "$PY_CTX/"
cp "$DEPLOY_DIR/docker/Dockerfile.pyworker" "$PY_CTX/Dockerfile"
docker build -t "$PY_IMG" "$PY_CTX"
rm -rf "$PY_CTX"
echo "[build] OK → $PY_IMG"

echo "== [build] web =="
WEB_CTX="$DEPLOY_DIR/build-ctx-web"
rm -rf "$WEB_CTX" && mkdir -p "$WEB_CTX"
# 在 node 容器内 npm ci && build（挂 npm cache 卷）
docker run --rm \
  -e VITE_DEFAULT_HOST="${VITE_DEFAULT_HOST:-192.168.4.44}" \
  -e VITE_DEFAULT_HTTP_PORT="${VITE_DEFAULT_HTTP_PORT:-22001}" \
  -e VITE_DEFAULT_WS_PORT="${VITE_DEFAULT_WS_PORT:-22003}" \
  -e HOST_UID="$(id -u)" -e HOST_GID="$(id -g)" \
  -v "$SRC_DIR/web:/src" -w /src \
  -v nt_npm_cache:/root/.npm \
  "$NODE_IMAGE" \
  bash -c '
    set -e
    if [[ -f package-lock.json ]]; then npm ci --prefer-offline; else npm install; fi
    npm run build:fast
    chown -R "$HOST_UID:$HOST_GID" /src/dist || true
  '
cp -a "$SRC_DIR/web/dist" "$WEB_CTX/dist"
cp "$DEPLOY_DIR/docker/web.nginx.conf" "$WEB_CTX/web.nginx.conf"
cp "$DEPLOY_DIR/docker/Dockerfile.web" "$WEB_CTX/Dockerfile"
docker build -t "$WEB_IMG" "$WEB_CTX"
rm -rf "$WEB_CTX"
echo "[build] OK → $WEB_IMG"

rm -rf "$BUILD_CTX"
echo "[build] 全部完成"
