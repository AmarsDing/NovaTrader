#!/usr/bin/env bash
set -euo pipefail
PROBE=/tmp/nt_probe_global_names.py
cat >"$PROBE" <<'PY'
import akshare as ak
df = ak.index_global_spot_em()
print("n=", len(df))
for _, row in df.iterrows():
    name = str(row.get("名称", ""))
    code = str(row.get("代码", ""))
    if any(k in name for k in ("道琼", "纳斯达", "标普", "恒生", "日经", "德国", "法国", "英国", "美元", "原油", "黄金", "富时", "韩国", "台湾")) or code in ("DJI","SPX","IXIC","NDX","HSI","N225","UDI","CL","GC"):
        print(code, name, row.get("最新价"), row.get("涨跌幅"), row.get("最新行情时间"))
print("--- all codes sample ---")
print(df[["代码","名称"]].head(30).to_string(index=False))
print("...")
# fx API
import inspect
print("fx_spot_quote sig", inspect.signature(ak.fx_spot_quote))
df2 = ak.fx_spot_quote()
print("fx cols", list(df2.columns), "n", len(df2))
print(df2.head(3).to_string(index=False))
PY
echo dingsheng | sudo -S docker cp "$PROBE" nt-pyworker:/tmp/nt_probe_global_names.py
echo dingsheng | sudo -S docker exec nt-pyworker python /tmp/nt_probe_global_names.py 2>&1 | tail -n 80
