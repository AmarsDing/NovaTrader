#!/usr/bin/env bash
# Hot-patch pyworker app.py into running nt-pyworker and restart.
set -euo pipefail
SRC="${1:-/home/dingsheng/novatrader/src/pyworker/app.py}"
if [[ ! -f "$SRC" ]]; then
  echo "missing $SRC" >&2
  exit 1
fi
echo dingsheng | sudo -S docker cp "$SRC" nt-pyworker:/app/app.py
echo dingsheng | sudo -S docker restart nt-pyworker
sleep 8
echo dingsheng | sudo -S docker exec nt-pyworker python -c 'import app; print("app ok", hasattr(app,"ymd_str"), hasattr(app,"ak_global"))'
curl -sf --max-time 5 http://127.0.0.1:22001/healthz && echo || true
