# N2-01 trade / backtest 真实流程观测（2026-10-19）

本记录补充[基础观测合同](N2-01-消费者与环境观测-2026-10-18.md)和[业务流程扩展](N2-01-业务流程观测扩展-2026-10-18.md)，记录交易与回测的实际调用接线及本轮验证。编译注册、HTTP 可访问和业务处理结果分别呈现；不能由进程存活推定全链路健康。

## 接线与完成范围

观测沿实际调用方向接入：`trade core → Engine → ledger/outbox`，`backtest core → Usecase → ResearchRepo / ResearchExecutor`。服务原有 HTTP server 注册生成的 `/internal/observability`，admin 仍通过固定服务清单采集、认证聚合。没有新增数据库表、行情替代值、交易授权或实盘出口。

| trade 操作 | 成功含义与结果区别 |
|---|---|
| minute_notice | 可信 market 分钟信封解析通过；不表示其每根 bar 入账。坏信封拒绝，停止后回调取消，空批跳过 |
| minute_bar | 有效源分钟的持久游标与账本事务提交；无订单也可完成，不保证有成交。缺边界/非法时间拒绝，重复或旧分钟跳过，事务失败记失败 |
| risk_exit | 本地退出委托记录完成；最终风控返回拒单记 rejected，不能因返回 nil 就记成功；不表示柜台 ACK |
| plan_tick / plan_execute | ticker 一轮调用返回与实际条件执行结果分开。条件未到/数据未知跳过，非法或冲突拒绝，真实错误失败；不保证成交 |
| snapshot / intent_recovery | 完整 SIM 快照事务提交 / 原身份持久意图核对一轮返回；不表示重新发送委托或实盘已对账 |
| outbox / confirm_expiry / watch_sweep | 原投递、确认过期、盯盘撤追一轮返回；不表示下游确认或保证退出成交 |
| paper_evidence | 实际采集并调用存储返回成功；非交易采样时段跳过。不表示新增唯一记录、交易日合格或满20日 |
| market_lease | 本地 NATS Publish 返回成功；不表示行情消费者已经接收续租 |

`ApplySourceBar` 保留实际 `Consumed` 结果，重复分钟不再与新增游标提交混记；旧 `ApplyBar` 包装入口兼容已有调用。留证同样返回“本轮是否实际采集并交存储”的结果，未改变已有准入规则。

| backtest 操作 | 成功含义与结果区别 |
|---|---|
| task_claim / research_claim | 实际取得待执行任务所有权；无任务跳过，不计一次执行成功 |
| run | 实际回测执行结局；引擎失败已写 failed、外层轮询返回 nil 时仍记失败；取消记取消。成功不表示盈利或准入 |
| recovery / research_recovery | 启动遗留任务恢复 / 研究租约恢复一轮返回；不是遗留任务成功 |
| research_outbox | 实际研究 outbox 投递调用返回；不表示下游 ACK |
| research_execute | 实际 worker 状态及工件复核结局；缺配置/不可用记拒绝，失败/取消分别记录，成功不表示候选晋级 |
| research_finish | 终态回写调用返回；CAS 可能因取消/旧 owner 而未改变状态，不能据此推定任务成功 |
| research_run | 回写后重读权威 ResearchJob 终态；成功、拒绝、失败、取消分别记录，无法确认终态时跳过，避免把回写 nil 当作成功 |

沿用固定标签、60秒窗口、在途计数、一次性结束、P95直方图上界与实例 UUID。证券、事件ID、正文及账户内容不进入指标标签。新实例从未采样开始；窗口过期不会抹掉本进程累计值；旧二进制未挂载端点仍为 unsupported。

## 已执行验证

2026-10-19 在 `backend/` 执行通过（隔离 PostgreSQL 55432；研究解释器为现有独立 venv）：

```powershell
$env:NT_TEST_PG_PORT='55432'
$env:NT_TEST_RESEARCH_PYTHON='F:\股票\NovaTrader\research-worker\.venv\Scripts\python.exe'
go test -race ./pkg/observe ./app/trade/internal/biz ./app/trade/internal/core ./app/trade/internal/server ./app/backtest/internal/biz ./app/backtest/internal/core ./app/backtest/internal/data ./app/backtest/internal/server ./app/admin/internal/gateway
go test -race ./app/trade/internal/server ./app/backtest/internal/server -run TestHTTPObservationMounted -count=1 -v
go vet ./pkg/observe ./pkg/allocation ./app/trade/internal/biz ./app/trade/internal/core ./app/trade/internal/data ./app/trade/internal/service ./app/trade/internal/server ./app/backtest/internal/biz ./app/backtest/internal/core ./app/backtest/internal/data ./app/backtest/internal/server ./app/admin/internal/gateway
go build ./app/trade/cmd ./app/backtest/cmd
```

第一项所有有测试的包通过，backtest/core 无测试文件；第二项两个具名实际 Kratos server 路由测试均 PASS，响应由 protojson 解码并经 `ValidateSnapshot` 验证。实际 Python 执行覆盖合法 QuantStats 工件、不可覆盖 run 目录失败和子进程取消；数据是显式测试夹具，不是真实收益证据。

新增合同测试还覆盖：真实分钟处理的拒绝/提交/重复跳过/事务错误/停止取消，最终风控拒单不记成功；回测业务失败即便轮询 nil 也记失败；未配置 executor 拒绝；固定标签、重启复位、窗口过期、结束幂等、包装取消，以及 admin 对编译注册服务的闭集校验。之前因观测审批额度未执行的收尾命令已由本轮实际 PASS 补齐。

## 验收边界

本轮未启动完整常驻环境或做真实市场长时运行，未验证跨主机时钟、券商 ACK、全链路精确延迟、全市场多窗口资源压力及连续20个交易日准入。NATS 真实退出订阅/停止验证沿用其[独立生命周期记录](N7-交易Runner生命周期-2026-10-18.md)，本轮没有将其包装成新的全环境联调。关闭监控轮询不影响原业务；观测完成不授予下单、晋级或实盘许可。
