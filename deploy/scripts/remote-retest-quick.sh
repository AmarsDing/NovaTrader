#!/usr/bin/env bash
set -eu
ADMIN=http://127.0.0.1:22001
TOKEN=$(curl -sf -X POST "$ADMIN/api/v1/login" -H 'Content-Type: application/json' \
  -d '{"user_name":"admin","password":"123456"}' | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
AUTH="Authorization: Bearer $TOKEN"
for spec in 'limit_pool|{"date":"2026-10-28"}' 'daily_bar|{"date":"2026-10-27"}' 'overseas|{}' 'macro|{}' 'snapshot|{}'; do
  domain=${spec%%|*}
  body=${spec#*|}
  code=$(curl -sS -o /tmp/nt-c.json -w '%{http_code}' -X POST "$ADMIN/api/datahub/collect/$domain" \
    -H "$AUTH" -H 'Content-Type: application/json' -d "$body" --max-time 300 || echo err)
  echo "[$code] $domain $(head -c 200 /tmp/nt-c.json)"
done
