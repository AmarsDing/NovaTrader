# pyworker

通达信 / AkShare 边车。datahub 用 HTTP 调度，本进程不写库、不下单。

```
pyworker\.venv\Scripts\python.exe app.py
```

环境变量：

| 变量 | 默认 |
|------|------|
| `TDX_DIR` | `D:\new_tdx` |
| `PYWORKER_LISTEN` | `127.0.0.1:18080` |
| `PYWORKER_ROLES` | `local,file,tcp,akshare,baostock` |

`local` 需要通达信量化插件（`PYPlugins`）。本机金融终端 V7.73 没有该目录时，`/api/*` 返回 503，datahub 会标 `unavailable` 并切备源。

`baostock` 角色提供 `POST /bs/bars`（K 线，日线含可空 `pre_close`，volume 单位为股）与 `POST /bs/finance`（季度窗财务四表合并行，缺 `pub_date` 或公告早于 `since` 的行不入）。进程级单登录 + 全局互斥锁串行化；断线码（`-10001`/`-10002`）锁内重登并重试一次。未安装 `baostock` 包（见 `requirements.txt`）时该角色返回 503 `unavailable`，未在 `PYWORKER_ROLES` 启用时返回 404 `role disabled`。
