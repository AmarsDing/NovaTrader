#!/bin/sh
# 同容器拉起全部 Go 业务进程；保留 127.0.0.1:20xx 互访。
# 先起 datahub 完成 ent/pgvector 迁移，再拉其余，避免 CREATE EXTENSION 竞态。
set -eu
CONF="${NT_CONF:-/app/configs}"
LOG_DIR="${NT_LOG_DIR:-/app/logs}"
mkdir -p "$LOG_DIR" /app/secrets

PIDS=""
start() {
  name="$1"
  bin="/app/bin/$name"
  if [ ! -x "$bin" ]; then
    echo "[entrypoint] missing $bin" >&2
    exit 1
  fi
  echo "[entrypoint] start $name"
  "$bin" -conf "$CONF" >>"$LOG_DIR/$name.log" 2>&1 &
  PIDS="$PIDS $!"
}

wait_http() {
  port="$1"
  i=0
  while [ "$i" -lt 60 ]; do
    if wget -qO- "http://127.0.0.1:${port}/healthz" >/dev/null 2>&1; then
      return 0
    fi
    i=$((i + 1))
    sleep 2
  done
  echo "[entrypoint] timeout waiting :${port}/healthz" >&2
  return 1
}

# 1) datahub 独占迁移窗口
start datahub
wait_http 2011 || true
sleep 3

# 2) 其余业务
start market
start intel
start brain
start strategy
start risk
start backtest
start trade
start notify
sleep 2
start admin
wait_http 2001 || true

term() {
  echo "[entrypoint] signal → stop"
  for p in $PIDS; do
    kill "$p" 2>/dev/null || true
  done
  wait || true
  exit 0
}
trap term INT TERM

while true; do
  for p in $PIDS; do
    if ! kill -0 "$p" 2>/dev/null; then
      echo "[entrypoint] pid $p exited" >&2
      term
    fi
  done
  sleep 3
done
