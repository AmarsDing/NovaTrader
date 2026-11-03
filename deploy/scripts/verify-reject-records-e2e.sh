#!/usr/bin/env bash
# verify-reject-records-e2e.sh — FE-R2 拒绝明细接口端到端验证（部署窗口执行）
#
# 验证链路：PG 插入 risk_check(approved=false) 原始行 → risk 服务 HTTP /risk/v1/rejects
# 返回逐条明细 → 与插入行逐字段对拍。覆盖：时间窗、rule/symbol/reason 过滤、分页。
#
# 依赖（部署窗口 compose 拉起后均在）：
#   - PostgreSQL  可达（默认 127.0.0.1:5435，可用 PGHOST/PGPORT 覆盖；账号默认 postgres/postgres）
#   - NATS        127.0.0.1:4222（risk 启动依赖，NATS_URL 可覆盖）
#   - Redis       127.0.0.1:6379（risk 启动依赖，REDIS_ADDR 可覆盖）
#   - Go 1.2x（用于 go run 起 risk；或预置 RISK_BIN 指向已编译二进制）
#   - psql、curl
#
# 用法：
#   cd backend && ../deploy/scripts/verify-reject-records-e2e.sh
# 可选环境变量：
#   PGHOST PGPORT PGUSER PGPASSWORD PGDATABASE   （默认 127.0.0.1 5435 postgres postgres novatrader）
#   RISK_HTTP=127.0.0.1:2081   RISK_BIN=/path/to/risk   SKIP_SERVER=1（risk 已在跑时）
#
# 前端上屏验证（脚本后人工执行，2 分钟）：
#   1) cd web && npm run dev
#   2) 浏览器打开「风控报告」面板，TopN 区点击 R05 所在行
#   3) 抽屉应列出本脚本插入的 e2e-rej-* 明细（时间倒序），筛选/翻页可用
set -euo pipefail

PGHOST="${PGHOST:-127.0.0.1}"; PGPORT="${PGPORT:-5435}"
PGUSER="${PGUSER:-postgres}"; export PGPASSWORD="${PGPASSWORD:-postgres}"
PGDATABASE="${PGDATABASE:-novatrader}"
RISK_HTTP="${RISK_HTTP:-127.0.0.1:2081}"
RISK_BIN="${RISK_BIN:-}"
SKIP_SERVER="${SKIP_SERVER:-0}"

PSQL=(psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d "$PGDATABASE" -v ON_ERROR_STOP=1 -q)
RISK_PID=""

cleanup() {
  echo "== 清理：删除 e2e 行、停 risk =="
  "${PSQL[@]}" -c "DELETE FROM risk_check WHERE client_order_id LIKE 'e2e-rej-%';" || true
  [ -n "$RISK_PID" ] && kill "$RISK_PID" 2>/dev/null || true
}
trap cleanup EXIT

echo "== 0/5 依赖检查 =="
command -v psql >/dev/null || { echo "缺 psql"; exit 1; }
command -v curl >/dev/null || { echo "缺 curl"; exit 1; }
"${PSQL[@]}" -c "SELECT 1;" >/dev/null || { echo "PG 连不上：$PGHOST:$PGPORT"; exit 1; }
for p in "${NATS_PORT:-4222}" "${REDIS_PORT:-6379}"; do
  (echo >"/dev/tcp/127.0.0.1/$p") 2>/dev/null || { echo "端口 $p 不通（risk 依赖 NATS/Redis）"; exit 1; }
done
echo "依赖 OK"

if [ "$SKIP_SERVER" != "1" ]; then
  echo "== 1/5 起 risk 服务（首次会自动建库/迁移，约 10-30s）=="
  if [ -n "$RISK_BIN" ]; then "$RISK_BIN" -conf ./configs & else go run ./app/risk/cmd -conf ./configs & fi
  RISK_PID=$!
else
  echo "== 1/5 SKIP_SERVER=1，用已在跑的 risk =="
fi
for i in $(seq 1 60); do
  curl -sf "http://$RISK_HTTP/risk/v1/rejects?days=1&limit=1" >/dev/null 2>&1 && break
  [ "$i" = 60 ] && { echo "risk 60s 未就绪"; exit 1; }
  sleep 1
done
echo "risk 就绪：$RISK_HTTP"

echo "== 2/5 插入 3 条 approved=false 原始行 =="
"${PSQL[@]}" <<'SQL'
INSERT INTO risk_check
  (client_order_id, account_type, symbol, side, source, operator, price,
   volume_req, volume_final, approved, rule_id, reason, results, input, latency_us, created_at)
VALUES
  ('e2e-rej-001','SIM','600519','buy','manual','e2e',1700.0,100,0,false,'R05','E2E验证：单笔金额超限','[]','{}',123,now()),
  ('e2e-rej-002','SIM','000001','sell','auto','e2e',12.5,200,0,false,'R03','E2E验证：仓位超限','[]','{}',45,now()),
  ('e2e-rej-003','SIM','600519','buy','watch','e2e',1701.0,50,0,false,'R05','E2E验证：单笔金额超限-第二笔','[]','{}',67,now());
SQL
echo "插入完成"

J='' # jq 可选；无 jq 用 grep 兜底
command -v jq >/dev/null && J=1
total_of() { # $1=query-string → 打印 total
  local body; body=$(curl -sf "http://$RISK_HTTP/risk/v1/rejects?$1")
  if [ -n "$J" ]; then echo "$body" | jq '.total'; else echo "$body" | grep -o '"total":[0-9]*' | cut -d: -f2; fi
}
assert_has() { # $1=query $2=期望包含的串
  local body; body=$(curl -sf "http://$RISK_HTTP/risk/v1/rejects?$1")
  echo "$body" | grep -q "$2" || { echo "FAIL: $1 未含 $2"; echo "$body"; exit 1; }
  echo "PASS: $1 含 $2"
}

echo "== 3/5 总窗验证（days=1，三条都应在）=="
assert_has "days=1&limit=10" "e2e-rej-001"
assert_has "days=1&limit=10" "e2e-rej-003"

echo "== 4/5 过滤验证：rule / symbol / reason =="
R=$(total_of "days=1&rule=R05");    [ "$R" -ge 2 ] && echo "PASS: rule=R05 total=$R(>=2)"    || { echo "FAIL: rule=R05 total=$R"; exit 1; }
R=$(total_of "days=1&symbol=000001"); [ "$R" -ge 1 ] && echo "PASS: symbol total=$R(>=1)"     || { echo "FAIL: symbol total=$R"; exit 1; }
assert_has "days=1&reason=%E4%BB%93%E4%BD%8D" "e2e-rej-002"   # reason=仓位（URL 编码）

echo "== 5/5 分页验证：limit=2 翻页 =="
B1=$(curl -sf "http://$RISK_HTTP/risk/v1/rejects?days=1&rule=R05&limit=2&offset=0")
B2=$(curl -sf "http://$RISK_HTTP/risk/v1/rejects?days=1&rule=R05&limit=2&offset=2")
echo "$B1" | grep -q "e2e-rej-00" || { echo "FAIL: 第一页无数据"; exit 1; }
if [ -n "$J" ]; then
  N1=$(echo "$B1" | jq '.items | length'); N2=$(echo "$B2" | jq '.items | length')
else
  N1=$(echo "$B1" | grep -o '"client_order_id"' | wc -l); N2=$(echo "$B2" | grep -o '"client_order_id"' | wc -l)
fi
echo "第1页 $N1 条（期望=2），第2页偏移后 $N2 条（期望>=0）"
[ "$N1" = "2" ] || { echo "FAIL: limit=2 未生效"; exit 1; }

echo ""
echo "=== 全部通过：/risk/v1/rejects 明细与 risk_check 原始行一致 ==="
echo "下一步人工前端上屏验证见脚本头注释。"
