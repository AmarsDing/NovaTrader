#!/usr/bin/env bash
# Probe nt-pyworker /ak/global and /ak/macro (date shapes + item counts).
set -euo pipefail
PROBE=/tmp/nt_probe_ak.py
cat >"$PROBE" <<'PY'
import json, urllib.request
for path in ("/ak/global", "/ak/macro"):
    try:
        with urllib.request.urlopen("http://127.0.0.1:18080" + path, timeout=180) as r:
            obj = json.loads(r.read().decode())
        items = obj.get("items") or []
        print(path, "n=", len(items))
        for it in items:
            print(" ", json.dumps(it, ensure_ascii=False, default=str))
    except Exception as e:
        print(path, "ERR", type(e).__name__, e)
PY
echo dingsheng | sudo -S docker cp "$PROBE" nt-pyworker:/tmp/nt_probe_ak.py
echo dingsheng | sudo -S docker exec nt-pyworker python /tmp/nt_probe_ak.py
