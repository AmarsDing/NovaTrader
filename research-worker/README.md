# NovaTrader 独立研究 worker

此目录与行情采集 `pyworker` 和交易文件单 `broker-gw` 分开。默认无交易端口、账户凭据和隐式行情下载。文件作业CLI已由Go持久研究任务接入，服务默认关闭，运行/取消/工件合同见[N6任务记录](../doc/开发文档/实施记录/N6-研究任务系统集成-2026-10-17.md)。研究输出没有生产组合执行权限。

## 安装与运行

使用 Python 3.12 创建本目录自己的虚拟环境，安装 `requirements.txt`。`requirements.lock.txt` 记录本轮 Windows Python 3.12.14 的完整依赖版本；Linux 运行环境需另做锁定和验收。禁止将依赖加入生产 `pyworker`。

```powershell
python -m venv .venv
.venv/Scripts/python.exe -m pip install -r requirements.txt
.venv/Scripts/python.exe -m unittest discover -s tests -v
.venv/Scripts/python.exe -I examples/make_synthetic_jobs.py
.venv/Scripts/python.exe -I worker.py --job examples/synthetic/performance.job.json --output runs
.venv/Scripts/python.exe -I worker.py --job examples/synthetic/portfolio.job.json --output runs
```

示例全部是 `synthetic_not_market_evidence`，空 manifest 仅用于协议演示，不代表真实市场输入。已有 `run_id` 的结果目录不能覆盖，再运行需新 run_id。每次作业写 `request.json`、`result.json`、环境指纹、输入/manifest/产物哈希；失败与不可用也有独立状态。结果不包含原始新闻正文或账户身份。

## 冻结文件合同 v1

job 字段为 `schema_version=1/run_id/kind/as_of/seed/manifest/input/options`。

- `manifest`：`{id, hash, path, sha256}`。`path` 是相对文件名；`sha256` 是文件字节哈希；内部 `id/hash` 是 Go DatasetManifestV1 身份。必须把 `/api/backtest/datasets/{manifest_id}` 返回的 **manifest_json 字符串原文**写入文件，不能经过 JavaScript Number 或 protobuf.Struct 转换。
- `input`：`{path,sha256}`。JSON 内固定 `manifest_id/manifest_hash`。Worker 复核文件、manifest、记录 payload/revision 哈希、已知可用时间与冻结截止，拒绝未来观察、凭据字段、目录逃逸、符号链接。
- Go 数值合同：精确十进制展开、去尾零、排序对象键、保留 null/[] 差异、Go HTML/U+2028/U+2029 转义。跨语言测试使用 `backend/pkg/dataset/testdata/manifest_v1.json`。
- 正式SIM NAV可使用Owner只读 `POST /api/trade/nav/export` 的非空input_json字符串，须原样传递；它只在实际初始收盘点、费用/现金对拍、逐持仓源价、显式基准及manifest均合格后生成。旧来源未知保持空输入，见[NAV记录](../doc/开发文档/实施记录/N6-01-SIM账本NAV导出-2026-10-18.md)。外部自带工件仍须证明来源；哈希只证明输入未变，不证明第三方会计真实性或历史许可。行情矩阵与信号收益不能冒充账户净值。

## 工具与实际边界

| kind | 已实现行为 | 前提与限制 |
|---|---|---|
| performance | QuantStats 同一费用后 NAV、初始净值点、显式基准；总收益/年化/回撤/Sharpe 与 Go 口径参考逐项差异；独立 HTML | `account_nav`、daily/CNY、完整 expected_dates、逐点 valuation_complete、ledger_hash/calendar_version；外部现金流必须预先单位化。报告不改账本；完整 vendor tearsheet 可选，样本不足会明确警告 |
| portfolio | PyPortfolioOpt Ledoit-Wolf 协方差 + 显式 CLARABEL/OSQP；现金底仓、单股/行业上限、锁仓、换手约束，约束残差复核 | 仅最小方差影子权重；未做股数取整、手续费预留、T+1 可卖、涨跌停、Go 最终风控，不生成订单；HRP 不宣称等风险贡献 |
| vectorbt | 冻结收益矩阵的 MA 参数筛选、决策滞后一日、独立训练/测试区间、冷/热耗时、全部试验记录 | 必须显式 `license_acknowledgement=Apache-2.0-with-Commons-Clause-reviewed` 且 `package_version` 匹配安装版本；本轮未安装/执行，不提供成交模拟；候选须回 Go 重测 |
| rd_agent | 固定镜像摘要、Linux Docker、无网络/只读根/非 root/资源时间预算、仅白名单输入、隔离产物、64KiB 日志尾部 | 默认不可用；需已审核的本地镜像、离线模型与匹配 Qlib 特征。无主机模型/交易密钥；输出大小轮询不等同磁盘配额。未执行真实 RD-Agent 容器，产物不可自动导入宿主 |
| jev_evaluate | 人工/合成标注分离，时间与事件簇隔离，规则/本地模型/Jev 对照；precision/recall/macro-F1/Brier/ECE/coverage/时延分位/输入 token/bootstrap | 至少 500 条真实中文双人复核标签尚缺。合成测试不能满足样本准入；指标只描述给定输入，不自动启用 Jev |

`result.status` 为 `succeeded/unavailable/rejected/failed/timed_out`。组合求解的 INFEASIBLE 等另列于 `solver_status`，权重为空，不能视为可执行组合。依赖缺失返回 unavailable，不替换成假数据或手写算法冒充库结果。LEAN 为可选离线差异研究，当前未引入。

Jev 线上观察器在 Go `pkg/fastjudge` 与 intel 数据层，配置及验收见 [N5/N6 实施记录](../doc/开发文档/实施记录/N5-02-N6-研究适配-2026-10-16.md)。

## 前瞻预测归档与评价

`prediction_archive` 输入 `prediction_batch`，预测写入输出根 `prediction-archive/<prediction_id>/forecast.json`，不能通过换 run_id 改写同 ID。`prediction_evaluate` 输入 `prediction_outcomes`，使用归档 SHA 引用和明确时间切分、未来已发生标签，报告校准、覆盖率与漂移；不能从当前行情倒填过去预测。详细字段可参照 `tests/test_prediction.py`，该文件所有观测明确为 synthetic。

两接口都不发布生产概率，`display_probability=null`。需要 200 条校准观测和 100 条样本外已知标签后才离线拟合 isotonic；不足时 Brier/ECE 为 null，不伪填。真实与 synthetic 不能混合。完整口径、归档持久性边界及验证见 [N5-03 实施记录](../doc/开发文档/实施记录/N5-03-预测归档与离线评价-2026-10-16.md)。

## Go 系统任务入口（2026-10-17）

新增 backtest `/v1/backtest/research/jobs` API、共享 research_job、独立常驻执行者、取消/租约恢复和 `sys.research` 同事务状态事件。默认关闭，服务端配置 `NT_RESEARCH_ENABLED/PYTHON/SCRIPT/ROOT` 才允许创建/执行，支持核心工具和预测归档评价；RD-Agent 仍仅独立 CLI。HTTP 至真实 Python、PG 状态及工件下载和 NATS 事件已实际验证。此节取代页首“尚未接入 Go 持久任务”的16日状态。详见 [17日系统集成记录](../doc/开发文档/实施记录/N6-研究任务系统集成-2026-10-17.md)。
