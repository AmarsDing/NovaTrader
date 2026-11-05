#!/usr/bin/env bash
set -euo pipefail
CFG=/home/dingsheng/novatrader/src/deploy/runtime/configs/config.yaml
python3 - <<'PY'
from pathlib import Path
p = Path("/home/dingsheng/novatrader/src/deploy/runtime/configs/config.yaml")
lines = p.read_text(encoding="utf-8").splitlines(True)
out = []
in_datahub = False
in_http = False
in_grpc = False
changed = 0
for line in lines:
    if line.startswith("datahub:"):
        in_datahub = True
        in_http = in_grpc = False
    elif in_datahub and line and not line[0].isspace() and not line.startswith("#"):
        in_datahub = False
        in_http = in_grpc = False
    if in_datahub and line.startswith("  http:"):
        in_http, in_grpc = True, False
    elif in_datahub and line.startswith("  grpc:"):
        in_http, in_grpc = False, True
    elif in_datahub and line.startswith("  ") and not line.startswith("    ") and ":" in line and not line.startswith("  http:") and not line.startswith("  grpc:"):
        # next top-level under datahub
        if not line.startswith("  http") and not line.startswith("  grpc"):
            in_http = in_grpc = False
    if (in_http or in_grpc) and line.strip().startswith("timeout:"):
        if "30s" in line:
            line = line.replace("30s", "180s")
            changed += 1
    out.append(line)
p.write_text("".join(out), encoding="utf-8")
print("changed_lines", changed)
PY
sed -n '89,96p' "$CFG"
export SUDO_PASS=dingsheng
source /home/dingsheng/novatrader/src/deploy/scripts/_lib.sh
docker kill nt-app || true
docker start nt-app
for i in $(seq 1 40); do
  st=$(docker inspect --format '{{.State.Health.Status}}' nt-app 2>/dev/null || echo missing)
  [[ "$st" == healthy ]] && break
  sleep 3
done
docker inspect --format '{{.State.Health.Status}}' nt-app
curl -sf http://127.0.0.1:22001/healthz; echo
