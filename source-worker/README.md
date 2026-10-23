# NovaTrader 可选历史与宏观补源

独立、默认关闭、只读的采集 worker，不导入生产 `pyworker`，不读写业务数据库、账户和交易服务，不执行远程 SKILL。Go datahub 是统一校验、PIT 修订与业务入库的唯一入口。现已实现可选 HTTP 补源适配；生产配置没有启用它，也不具备 Snapshot 实时行情能力。

已经实现两个有限能力：

- `tdx_history`：参考 A-Stock-Data V3.10.2 公布的格式，使用标准库读取通达信官方盘后 ZIP/COD/MD1，范围明确为未复权、有有效 OHLC 的 A 股。单位为元/股/元；11 条 close-only 无有效 OHLC 的停牌记录在本次真实包中明确排除，不编成可执行 bar。北交所历史文件边界、重复代码/序号、长度、编码、非有限数字和逐市场空结果均校验。
- `openbb_macro`：可选 `openbb-core==2.0.0` / `openbb-fred==2.0.0`，真实 provider 是圣路易斯联储 FRED，白名单 CPIAUCSL/FEDFUNDS/UNRATE。从服务器环境读取 `FRED_API_KEY`，失败输出不包含 key/URL异常正文。只支持已审阅 V5 FRED 命名空间；缺依赖、版本变化或缺 key 返回 unavailable。尚未对真实 FRED 运行验收。

两个能力都不伪造发布时间。通达信包的 HTTP Last-Modified 只是 hint；FRED current-series 模型会丢弃 vintage 字段，不能当作 ALFRED 发布历史。输出 `available_at=null`、`training_ready=false`，只能作为历史展示/对拍候选；经 PIT 证据核验后才能进历史研究。

原 POC 命令继续保留，用于冻结原包和最多 30 条证据样本。从本目录运行，一个作业 stdin JSON，一次最多 64KiB；只接受固定操作与参数，输出目录由启动参数指定：

```powershell
'{"schema_version":1,"enabled":true,"operation":"tdx_history","date":"2025-01-06"}' |
  python worker.py --output-dir evidence/tdx-2025-01-06

'{"schema_version":1,"enabled":true,"operation":"openbb_macro","series":"CPIAUCSL","start":"2024-01-01","end":"2025-01-01"}' |
  python worker.py --output-dir evidence/openbb-fred

python -m unittest discover -s tests -v
```

省略 `enabled:true` 返回 disabled。非成功状态 exit code 2，包括 not_published、rate_limited、empty_result、format_changed、unavailable 和 artifact_error；不会把 HTTP 200 空体标正常。下载20秒超时，压缩体8MiB、解压100MiB上限，无无限重试。离线测试不联网。

冻结工件使用内容寻址：原始字节 `.raw`、元数据与30条样本 `.json`，先 fsync 临时文件，再原子发布，存在同名但内容不同则拒绝。SDK 工件明确标 `sdk_normalized_result_json`，不冒充原始 FRED HTTP 响应。代码不执行安装命令；如要测试 OpenBB，另建隔离环境按 `requirements-openbb.txt` 安装，并在实际安装后归档完整传递依赖 lock 与环境 hash。当前文件只固定直接包版本。

2026-10-16 POC 实际结果：通达信2025-01-06包包含5370条有效 A 股，已冻结沪深北各10条真实样本及原包。见 `evidence/tdx-probe.json`。OpenBB 探针明确缺少服务器 FRED_API_KEY，见 `evidence/openbb-probe.json`，没有生成伪宏观样本。当时10项独立测试通过。详见 [POC 实施记录](../doc/开发文档/实施记录/N4-02-历史与宏观源POC-2026-10-16.md)。

## HTTP 完整记录合同与 Go 接线

从本目录运行下列命令只启动默认关闭的本机 HTTP 接口，收集请求仍返回 disabled：

```powershell
python http_worker.py --port 18082 --evidence-dir runtime/source-evidence
```

接口只监听 `127.0.0.1`。`POST /v1/source/collect` 接受版本 1 的固定 `tdx_history/date` 或 `openbb_macro/series/start/end` 参数，不接受 URL、文件路径或请求级 enable。最多两个并发操作，请求 4KiB，响应和原始压缩包 8MiB，完整记录最多 10000 条；不存在后台无限排队。`GET /healthz` 仅表示进程状态，provider 为 unprobed、覆盖率未知。

成功响应明确区分 `records` 全量与旧 `.json` 工件里的 30 条 samples，带 `record_count`、`records_sha256`、`provider_raw_sha256`、`provider_raw_kind`、`acquisition_mode` 和 `observed_at`。`available_at` 仍为 null，`training_ready` 为 false。SDK 规范化字节和原始 HTTP ZIP 字节使用不同 raw kind。

以下显式启用示例只用于离线录制验收：

```powershell
python http_worker.py --enabled --recorded-only --port 18082 --evidence-dir runtime/recorded-evidence
```

该模式只重放内部固定的 `evidence/tdx-2025-01-06/dfca352934d3681cb9e8e5644f0492d02a562d0a13f751acbc1d85febdf970f5.raw`，校验 SHA256，响应为 `recorded_provider_bytes`。不允许请求另一天或另一个路径。它不代表今日行情或独立备源。未加 `--recorded-only` 的显式启用模式才会访问固定官方 provider；本轮没有做在线 provider 验收。

Go 可使用现有配置映射显式添加补源。下列示例保持关闭，仅展示配置名称；不会修改生产配置：

```yaml
datahub:
  sources:
    tdx_http_archive:
      enabled: false
      official: true
      url: http://127.0.0.1:18082
      timeout: 30s
    openbb_fred:
      enabled: false
      official: true
      url: http://127.0.0.1:18082
      timeout: 30s
```

启用还需把对应名称加入现有 `domains.daily_bar.sources` 或 `domains.macro.sources`；只开放盘后 1d 日线和三个白名单宏观序列。Go URL 只能使用带端口的 `http://127.0.0.1`，不带凭证/路径/参数，不跟随重定向或使用系统代理。适配器不实现快照和证券基础信息接口。worker 没有数据库接口，但这不等于部署已有操作系统级 CPU、内存或文件权限沙箱。

Go 检查完整记录、交易日、沪深北市场声明、OHLC、hash 与采集时钟；采集时钟不能用来补历史发布时间或参与稳定修订身份。仓储同一事务预留内容寻址 outbox、追加 PIT、更新 current-view。补源只能补缺项或修订同补源 current 行，不能覆盖主源。宏观单位未知或超过现有 current 字段 16 字节时只留完整 PIT，不写失去单位含义的 current 数值。覆盖率缺分母时保持 `coverage_unknown`。

2026-10-19 验收：Python 17 项测试通过；`pkg/dataset` 和 datahub 全部 internal 包的 Go race 回归共 53 个测试及子测试通过，0 失败、0 测试跳过（core/service 无测试文件）。隔离 PG55432 实际完成已有原包全部5370条 → Python HTTP → Go → PostgreSQL，验证重复请求只保留一份批次 outbox、同源修订、20并发竞争、主源保护和故障回滚。真实 OpenBB/FRED 缺 key 保持 unavailable；宏观持久化边界用明确标识的合成夹具验证。详见 [统一接入实施记录](../doc/开发文档/实施记录/N4-02-历史补源统一接入-2026-10-19.md)。

上游代码许可与内容hash见 `NOTICE.md` / `upstream-lock.json`。未对数据的商业使用或再分发权限作自动认定。
