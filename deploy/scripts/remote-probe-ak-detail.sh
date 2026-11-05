#!/usr/bin/env bash
set -euo pipefail
PROBE=/tmp/nt_probe_ak_detail.py
cat >"$PROBE" <<'PY'
import traceback
import akshare as ak

def try_call(name, fn):
    print("==", name)
    try:
        df = fn()
        if df is None:
            print("  None")
            return
        print("  shape", getattr(df, "shape", None), "cols", list(getattr(df, "columns", []))[:12])
        print("  head", df.head(2).to_dict("records") if hasattr(df, "head") else df)
    except Exception as e:
        print("  ERR", type(e).__name__, e)
        traceback.print_exc(limit=2)

try_call("stock_us_spot_em", lambda: ak.stock_us_spot_em())
try_call("fx_spot_quote", lambda: ak.fx_spot_quote(symbol="美元人民币"))
try_call("futures_oil", lambda: ak.futures_foreign_commodity_realtime(symbol="NYMEX原油"))
try_call("futures_gold", lambda: ak.futures_foreign_commodity_realtime(symbol="COMEX黄金"))
try_call("sox", lambda: ak.macro_global_sox_index())
for fn_name in ("index_global_spot_em", "index_global_hist_em", "stock_us_famous_spot_em", "currency_boc_sina"):
    fn = getattr(ak, fn_name, None)
    if fn is None:
        print("==", fn_name, "MISSING")
        continue
    try_call(fn_name, fn)
PY
echo dingsheng | sudo -S docker cp "$PROBE" nt-pyworker:/tmp/nt_probe_ak_detail.py
echo dingsheng | sudo -S docker exec nt-pyworker python /tmp/nt_probe_ak_detail.py 2>&1 | tail -n 120
