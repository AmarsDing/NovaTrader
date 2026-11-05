#!/usr/bin/env bash
set -eu
CFG=/home/dingsheng/novatrader/src/deploy/runtime/configs/config.yaml
# Replace first timeout under akshare block via awk (avoid hung python -c multiline on some hosts)
awk '
  BEGIN{in_ak=0; done=0}
  /^[[:space:]]*akshare:[[:space:]]*$/ {in_ak=1; print; next}
  in_ak && /^[[:space:]]*[a-z_]+:[[:space:]]*$/ && $0 !~ /^[[:space:]]*akshare:/ {
    in_ak=0
  }
  in_ak && !done && /^[[:space:]]*timeout:[[:space:]]*/ {
    sub(/timeout:[[:space:]]*.*/, "timeout: 180s")
    done=1
  }
  {print}
' "$CFG" > /tmp/nt-config-patched.yaml
mv /tmp/nt-config-patched.yaml "$CFG"
grep -A6 'akshare:' "$CFG" | head -8
echo dingsheng | sudo -S docker restart nt-app
for i in $(seq 1 15); do
  if curl -sf --max-time 3 http://127.0.0.1:22001/healthz >/dev/null; then
    echo "healthz ok"
    curl -sf --max-time 3 http://127.0.0.1:22001/healthz; echo
    exit 0
  fi
  sleep 2
done
echo "healthz timeout" >&2
exit 1
