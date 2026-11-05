#!/usr/bin/env bash
# Lower eastmoney QPS/concurrency on 44 to reduce push2 EOF under rate limits.
set -euo pipefail
CFG="${1:-/home/dingsheng/novatrader/src/deploy/runtime/configs/config.yaml}"
python3 - <<'PY' "$CFG"
import re, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    text = f.read()

def patch_eastmoney(block: str) -> str:
    block = re.sub(r"(qps:\s*)\d+", r"\g<1>2", block, count=1)
    block = re.sub(r"(concurrency:\s*)\d+", r"\g<1>2", block, count=1)
    return block

# Match eastmoney section until next sibling key at same indent (4 spaces + word)
pat = re.compile(r"(^[ \t]*eastmoney:\n(?:[ \t]+.+\n)*)", re.M)

def repl(m):
    return patch_eastmoney(m.group(1))

new, n = pat.subn(repl, text, count=1)
if n != 1:
    print(f"WARN: expected 1 eastmoney block, got {n}", file=sys.stderr)
with open(path, "w", encoding="utf-8") as f:
    f.write(new)
print("patched", path)
# show block
for m in pat.finditer(new):
    print(m.group(1))
    break
PY
echo dingsheng | sudo -S docker kill nt-app >/dev/null || true
echo dingsheng | sudo -S docker start nt-app
sleep 25
curl -sf http://127.0.0.1:22001/healthz && echo
