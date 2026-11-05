#!/usr/bin/env bash
set -euo pipefail
PROBE=/tmp/nt_probe_hist.py
cat >"$PROBE" <<'PY'
import inspect, akshare as ak, time
print("hist sig", inspect.signature(ak.index_global_hist_em))
for sym in ("UDI", "DJI", "SPX", "NDX", "NDQ", "HSI", "N225", "SP500"):
    time.sleep(1.5)
    try:
        df = ak.index_global_hist_em(symbol=sym)
        print(sym, "ok", len(df), "last", df.iloc[-1].to_dict() if len(df) else None)
    except Exception as e:
        print(sym, "ERR", type(e).__name__, e)
# retry spot once after cooldown
time.sleep(3)
try:
    df = ak.index_global_spot_em()
    print("spot ok", len(df))
    print(df[["代码","名称","最新价"]].to_string(index=False))
except Exception as e:
    print("spot ERR", e)
PY
echo dingsheng | sudo -S docker cp "$PROBE" nt-pyworker:/tmp/nt_probe_hist.py
echo dingsheng | sudo -S docker exec nt-pyworker python /tmp/nt_probe_hist.py 2>&1 | tail -n 100
