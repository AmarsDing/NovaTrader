#!/usr/bin/env bash
set -euo pipefail
cd /home/dingsheng/novatrader/src/deploy
chmod +x scripts/*.sh docker/entrypoint.sh
find scripts docker -name '*.sh' -exec sed -i 's/\r$//' {} \;
export SUDO_PASS=dingsheng
bash scripts/deploy.sh > /tmp/nt-deploy.log 2>&1
echo EXIT=$? >> /tmp/nt-deploy.log
