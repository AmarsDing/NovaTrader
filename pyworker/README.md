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
| `PYWORKER_ROLES` | `local,file,tcp,akshare` |

`local` 需要通达信量化插件（`PYPlugins`）。本机金融终端 V7.73 没有该目录时，`/api/*` 返回 503，datahub 会标 `unavailable` 并切备源。
