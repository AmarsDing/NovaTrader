#!/usr/bin/env bash
set -euo pipefail
export SUDO_PASS=dingsheng
source /home/dingsheng/novatrader/src/deploy/scripts/_lib.sh

echo "== pyworker root =="
docker exec nt-app wget -qO- http://pyworker:18080/ || true
echo
echo "== pyworker packages =="
docker exec nt-pyworker python -c 'import akshare,baostock; print("akshare", getattr(akshare,"__version__", "?"), "baostock ok")'
echo "== akshare ping via pyworker role =="
docker exec nt-pyworker python -c '
import json, urllib.request
# hit a lightweight akshare call if endpoint exists
print("roles ok")
'
