#!/usr/bin/env bash
# 部署后冒烟（失败 exit 1）
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[[ -f "$DEPLOY_DIR/env/.env" ]] && { set -a; source "$DEPLOY_DIR/env/.env"; set +a; }
# shellcheck source=_lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/_lib.sh"

HTTP_PORT="${ADMIN_HTTP_PORT:-22001}"
WS_HINT="${ADMIN_WS_PORT:-22003}"
WEB_PORT="${WEB_PORT:-9302}"
NATS_HTTP="${NATS_HTTP_PORT:-28222}"
fail=0

ok() { echo "[smoke] ✓ $1"; }
bad() { echo "[smoke] ✗ $1"; fail=1; }

st=$(docker inspect --format '{{.State.Health.Status}}' nt-postgres 2>/dev/null || echo missing)
[[ "$st" == healthy ]] && ok "container nt-postgres" || bad "container nt-postgres ($st)"

st=$(docker inspect --format '{{.State.Health.Status}}' nt-redis 2>/dev/null || echo missing)
[[ "$st" == healthy ]] && ok "container nt-redis" || bad "container nt-redis ($st)"

st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
[[ "$st" == healthy ]] && ok "container nt-app" || bad "container nt-app ($st)"

docker inspect --format '{{.State.Running}}' nt-pyworker 2>/dev/null | grep -qx true \
  && ok "container nt-pyworker running" || bad "container nt-pyworker"

docker inspect --format '{{.State.Running}}' nt-web 2>/dev/null | grep -qx true \
  && ok "container nt-web running" || bad "container nt-web"

curl -sf -m 5 "http://127.0.0.1:${NATS_HTTP}/healthz" >/dev/null \
  && ok "nats /healthz" || bad "nats /healthz"

curl -sf -m 8 "http://127.0.0.1:${HTTP_PORT}/healthz" >/dev/null \
  && ok "admin /healthz :${HTTP_PORT}" || bad "admin /healthz"

curl -sf -m 5 "http://127.0.0.1:${WEB_PORT}/" >/dev/null \
  && ok "web :${WEB_PORT}" || bad "web"

LOGIN_FILE=$(mktemp)
curl -sf -m 10 -X POST "http://127.0.0.1:${HTTP_PORT}/api/v1/login" \
  -H 'Content-Type: application/json' \
  -d '{"user_name":"admin","password":"123456"}' >"$LOGIN_FILE" 2>/dev/null || true
if grep -Eqi 'access_?[Tt]oken|"token"' "$LOGIN_FILE"; then
  ok "登录 admin 返回 token"
else
  echo "[smoke] ⚠ 登录未返回 token（HTTP/WS=${HTTP_PORT}/${WS_HINT} body=$(head -c 120 "$LOGIN_FILE")）"
fi
rm -f "$LOGIN_FILE"

echo "[smoke] 宿主入口: HTTP :${HTTP_PORT}  WS :${WS_HINT}  Web :${WEB_PORT}"
[[ "$fail" == 0 ]] || exit 1
echo "[smoke] 失败项: 0"
