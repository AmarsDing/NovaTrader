# NovaTrader 可选历史与宏观源 POC

独立、默认关闭、只读的采集 worker，不导入生产 `pyworker`，不读写业务数据库、账户和交易服务，不执行远程 SKILL。Go datahub 仍是后续正式入库的唯一入口；当前仅做冻结样本与协议验证，未注册到生产主备行情链。

已经实现两个有限能力：

- `tdx_history`：参考 A-Stock-Data V3.10.2 公布的格式，使用标准库读取通达信官方盘后 ZIP/COD/MD1，范围明确为未复权、有有效 OHLC 的 A 股。单位为元/股/元；11 条 close-only 无有效 OHLC 的停牌记录在本次真实包中明确排除，不编成可执行 bar。北交所历史文件边界、重复代码/序号、长度、编码、非有限数字和逐市场空结果均校验。
- `openbb_macro`：可选 `openbb-core==2.0.0` / `openbb-fred==2.0.0`，真实 provider 是圣路易斯联储 FRED，白名单 CPIAUCSL/FEDFUNDS/UNRATE。从服务器环境读取 `FRED_API_KEY`，失败输出不包含 key/URL异常正文。只支持已审阅 V5 FRED 命名空间；缺依赖、版本变化或缺 key 返回 unavailable。尚未对真实 FRED 运行验收。

两个能力都不伪造发布时间。通达信包的 HTTP Last-Modified 只是 hint；FRED current-series 模型会丢弃 vintage 字段，不能当作 ALFRED 发布历史。输出 `available_at=null`、`training_ready=false`，只能作为历史展示/对拍候选；经 PIT 证据核验后才能进历史研究。

从本目录运行，一个作业 stdin JSON，一次最多 64KiB；只接受固定操作与参数，输出目录由启动参数指定：

```powershell
'{"schema_version":1,"enabled":true,"operation":"tdx_history","date":"2025-01-06"}' |
  python worker.py --output-dir evidence/tdx-2025-01-06

'{"schema_version":1,"enabled":true,"operation":"openbb_macro","series":"CPIAUCSL","start":"2024-01-01","end":"2025-01-01"}' |
  python worker.py --output-dir evidence/openbb-fred

python -m unittest discover -s tests -v
```

省略 `enabled:true` 返回 disabled。非成功状态 exit code 2，包括 not_published、rate_limited、empty_result、format_changed、unavailable 和 artifact_error；不会把 HTTP 200 空体标正常。下载20秒超时，压缩体8MiB、解压100MiB上限，无无限重试。离线测试不联网。

冻结工件使用内容寻址：原始字节 `.raw`、元数据与30条样本 `.json`，先 fsync 临时文件，再原子发布，存在同名但内容不同则拒绝。SDK 工件明确标 `sdk_normalized_result_json`，不冒充原始 FRED HTTP 响应。代码不执行安装命令；如要测试 OpenBB，另建隔离环境按 `requirements-openbb.txt` 安装，并在实际安装后归档完整传递依赖 lock 与环境 hash。当前文件只固定直接包版本。

2026-10-16 实际结果：通达信2025-01-06包包含5370条有效 A 股，已冻结沪深北各10条真实样本及原包。见 `evidence/tdx-probe.json`。OpenBB 探针明确缺少服务器 FRED_API_KEY，见 `evidence/openbb-probe.json`，没有生成伪宏观样本。10项独立测试通过。详见 [实施记录](../doc/开发文档/实施记录/N4-02-历史与宏观源POC-2026-10-16.md)。

上游代码许可与内容hash见 `NOTICE.md` / `upstream-lock.json`。未对数据的商业使用或再分发权限作自动认定。
