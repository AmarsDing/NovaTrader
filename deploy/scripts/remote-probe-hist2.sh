#!/usr/bin/env bash
set -euo pipefail
PROBE=/tmp/nt_probe_hist2.py
cat >"$PROBE" <<'PY'
import time, akshare as ak
for sym in ("美元指数", "道琼斯", "标普500", "纳斯达克", "恒生指数", "日经225", "英国富时100", "德国DAX30"):
    time.sleep(2)
    try:
        df = ak.index_global_hist_em(symbol=sym)
        last = df.iloc[-1].to_dict()
        print(sym, "ok n=", len(df), "last=", {k: last.get(k) for k in ("日期","代码","名称","最新价","今开")})
    except Exception as e:
        print(sym, "ERR", type(e).__name__, e)
PY
echo dingsheng | sudo -S docker cp "$PROBE" nt-pyworker:/tmp/nt_probe_hist2.py
echo dingsheng | sudo -S docker exec nt-pyworker python /tmp/nt_probe_hist2.py 2>&1 | tail -n 40
