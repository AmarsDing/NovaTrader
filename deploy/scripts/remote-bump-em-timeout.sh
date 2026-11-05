#!/usr/bin/env bash
set -euo pipefail
python3 - <<'PY'
import re
path = "/home/dingsheng/novatrader/src/deploy/runtime/configs/config.yaml"
text = open(path, encoding="utf-8").read()
new = re.sub(
    r"(?m)(^\s*eastmoney:\n(?:^[ \t]+(?!timeout:).+\n)*)(^[ \t]+timeout:\s*)10s",
    r"\g<1>\g<2>60s",
    text,
    count=1,
)
open(path, "w", encoding="utf-8").write(new)
m = re.search(r"(?m)^\s*eastmoney:\n(?:^[ \t]+.+\n){0,8}", new)
print(m.group(0) if m else "NO_MATCH")
print("changed", text != new)
PY
export SUDO_PASS=dingsheng
source /home/dingsheng/novatrader/src/deploy/scripts/_lib.sh
docker restart nt-app
for i in $(seq 1 40); do
  st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
  [[ "$st" == healthy ]] && break
  sleep 3
done
docker inspect --format '{{.State.Health.Status}}' nt-app
curl -sf http://127.0.0.1:22001/healthz; echo
