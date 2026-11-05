#!/usr/bin/env bash
# 从源码 configs 渲染到 deploy/runtime/configs（kratos 不展开 env，必须写实值）
set -euo pipefail
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC_DIR="${SRC_DIR:-$(cd "$DEPLOY_DIR/.." && pwd)}"
[[ -f "$DEPLOY_DIR/env/.env" ]] || { echo "[render] 缺少 env/.env"; exit 1; }
set -a; source "$DEPLOY_DIR/env/.env"; set +a

OUT="$DEPLOY_DIR/runtime/configs"
rm -rf "$OUT"
mkdir -p "$OUT"
cp -a "$SRC_DIR/backend/configs/." "$OUT/"

CFG="$OUT/config.yaml"
[[ -f "$CFG" ]] || { echo "[render] 缺少 config.yaml"; exit 1; }

# 定向改写（nt-mcp 开关 / 数据源开关 / 超时 / 容器内绝对路径）。
# 实现要点：逐行扫描 + 缩进栈求键路径，O(n)，只改首个命中行。
# 历史坑（2026-11-03 44 实录）：原实现用 `(?m)^\s*key:` 形态正则，
# 在部分 config.yaml 内容下触发指数级回溯，python3 卡死 >3min，部署卡在渲染步；
# 改成线性扫描后同文件 <10ms，且语义等价（首个命中、保留行尾注释、未命中保持原样）。
python3 - <<'PY' "$CFG" "${NTMCP_ENABLED:-false}"
import sys

path = sys.argv[1]
ntmcp_enabled = sys.argv[2].strip().lower()

with open(path, encoding="utf-8") as f:
    lines = f.readlines()


def set_nested(keys, value):
    """把键路径以 keys 结尾的首个 `key: value` 行的值改为 value，返回是否命中。"""
    stack = []  # [(indent, key)] 仅记录「无值」的容器键
    for i, line in enumerate(lines):
        s = line.strip()
        if not s or s.startswith("#"):
            continue
        indent = len(line) - len(line.lstrip(" "))
        if ":" not in s:
            continue
        key, rest = s.split(":", 1)
        key = key.strip()
        while stack and stack[-1][0] >= indent:
            stack.pop()
        chain = [k for _, k in stack] + [key]
        if len(chain) >= len(keys) and chain[-len(keys):] == keys:
            comment = ""
            if "#" in rest:
                comment = "   #" + rest.split("#", 1)[1].rstrip()
            lines[i] = " " * indent + key + ": " + value + comment + "\n"
            return True
        if rest.strip() == "" or rest.strip().startswith("#"):
            stack.append((indent, key))
    return False


missing = []
for keys, value in (
    (["ntmcp", "enabled"], ntmcp_enabled),
    (["tdx_local", "enabled"], "false"),
    (["tdx_file", "enabled"], "false"),
    (["tdx_tcp", "enabled"], "false"),
    (["akshare", "enabled"], "true"),
    (["baostock", "enabled"], "true"),
    (["akshare", "timeout"], "180s"),
    (["eastmoney", "timeout"], "60s"),
    (["eastmoney", "qps"], "2"),
    (["eastmoney", "concurrency"], "2"),
    (["datahub", "http", "timeout"], "180s"),
    (["datahub", "grpc", "timeout"], "180s"),
    (["auth", "modefile"], '"/app/configs/casbin/auth_model.conf"'),
    (["log", "file", "filename"], "./logs/server.log"),
):
    if not set_nested(keys, value):
        missing.append(".".join(keys))

with open(path, "w", encoding="utf-8", newline="\n") as f:
    f.writelines(lines)

print("[render] patched", path)
print("[render] ntmcp.enabled =", ntmcp_enabled)
if missing:
    print("[render] 未命中键（保持原样）:", ", ".join(missing))
PY

# 基础设施：容器服务名（同 nt-net）
# 注意：sed 分隔符避开 URL 中的 /
PW_ESC=$(printf '%s' "${PG_PASSWORD}" | sed 's/[&/\]/\\&/g')

# postgres
sed -i \
  -e 's|^  host:.*|  host: postgres|' \
  -e 's|^  port:.*|  port: 5432|' \
  -e "s|^  user:.*|  user: ${PG_USER:-postgres}|" \
  -e "s|^  password:.*|  password: ${PW_ESC}|" \
  -e "s|^  database:.*|  database: ${PG_DB:-novatrader}|" \
  "$CFG"

# redis / nats
sed -i \
  -e 's|^  addr: 127.0.0.1:6379|  addr: redis:6379|' \
  -e 's|^  url: nats://127.0.0.1:4222|  url: nats://nats:4222|' \
  "$CFG"

# pyworker：容器服务名；Linux 无通达信 → 关闭 tdx_*；打开 baostock
sed -i \
  -e 's|url: http://127.0.0.1:18080|url: http://pyworker:18080|g' \
  "$CFG"


echo "[render] → $OUT"
