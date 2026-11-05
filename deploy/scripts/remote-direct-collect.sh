#!/usr/bin/env bash
set +e
export SUDO_PASS=dingsheng
source /home/dingsheng/novatrader/src/deploy/scripts/_lib.sh

echo "== rebuild status =="
tail -50 /tmp/nt-rebuild-admin.log 2>/dev/null
pgrep -af 'go build|remote-rebuild|docker build' | head -10 || true

collect() {
  local domain="$1" timeout="${2:-120}" body="${3:-{}}"
  echo "---- collect $domain (timeout=${timeout}s) ----"
  docker exec nt-app wget -qO- -T "$timeout" \
    --header="Content-Type: application/json" \
    --post-data="$body" \
    "http://127.0.0.1:2011/v1/datahub/collect/${domain}" 2>&1 | head -c 400
  echo
}

# 先短任务；security 最长
collect limit_pool 60
collect hot_rank 60
collect macro 90
collect overseas 90
collect security 180
collect snapshot 120
collect daily_bar 120 '{"date":"2026-10-28"}'

echo DONE
