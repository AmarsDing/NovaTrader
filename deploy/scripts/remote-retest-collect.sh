#!/usr/bin/env bash
# Retest datahub collect domains after pyworker/QPS fixes.
set -euo pipefail
ADMIN="${ADMIN:-http://127.0.0.1:22001}"
USER="${NT_USER:-admin}"
PASS="${NT_PASS:-123456}"
TOKEN=$(curl -sf -X POST "$ADMIN/api/v1/login" -H 'Content-Type: application/json' \
  -d "{\"user_name\":\"$USER\",\"password\":\"$PASS\"}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["token"])')
AUTH="Authorization: Bearer $TOKEN"

collect() {
  local domain="$1"
  local body="${2:-{}}"
  local t0=$(date +%s%3N)
  local code body_out
  body_out=$(curl -sS -o /tmp/nt-collect-body.json -w '%{http_code}' -X POST \
    "$ADMIN/api/datahub/collect/$domain" -H "$AUTH" -H 'Content-Type: application/json' \
    -d "$body" --max-time 300 || echo curl_err)
  local t1=$(date +%s%3N)
  local ms=$((t1 - t0))
  local snippet
  snippet=$(head -c 240 /tmp/nt-collect-body.json 2>/dev/null || true)
  echo "[$body_out] ${domain} ${ms}ms ${snippet}"
}

echo "=== collect retest $(date -Iseconds) ==="
collect security '{}'
collect snapshot '{}'
collect daily_bar '{"date":"2026-10-28"}'
collect limit_pool '{"date":"2026-10-28"}'
collect macro '{}'
collect overseas '{}'
collect hot_rank '{}'
collect sector '{}'

echo
echo "=== sources summary ==="
curl -sf "$ADMIN/api/datahub/sources" -H "$AUTH" -o /tmp/nt-sources.json
python3 - <<'PY'
import json
from collections import defaultdict
obj = json.load(open("/tmp/nt-sources.json", encoding="utf-8"))
g = defaultdict(lambda: defaultdict(int))
for s in obj.get("sources") or []:
    g[s.get("source", "?")][s.get("state", "?")] += 1
for name in sorted(g):
    parts = " ".join(f"{k}={v}" for k, v in sorted(g[name].items()))
    print(f"{name}: {parts}")
PY
