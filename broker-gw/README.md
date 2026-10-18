# NovaTrader broker-gw 隔离文件单网关

本目录是独立 Python 3.12+ 进程，只使用标准库，与行情 `pyworker` 分离。当前版本固定为 `ISOLATED`，只接受由 `init` 创建的空隔离根目录。没有切换 LIVE 的参数，也没有接入 `trade` 实盘出口。

现已实现从文件发布、协议模拟回报到查询和对账的可运行链路，以及可显式启动的 mTLS JSON 服务。协议依据、支持字段和未核验项见 [protocol-manifest.json](protocol-manifest.json)。官方文档已核对，真实终端版本、实际导出文件及编码尚待现场验证。

## 运行隔离演示

在 `broker-gw` 目录执行，`python` 使用本机 Python 3.12 或更高版本：

```powershell
$demoRoot = Join-Path $env:TEMP ('novatrader-broker-demo-' + [guid]::NewGuid().ToString('N'))
python -m novatrader_broker init --root $demoRoot
python -m novatrader_broker demo --root $demoRoot --account TEST_ACCOUNT
python -m novatrader_broker health --root $demoRoot --account TEST_ACCOUNT
```

`demo` 使用全新隔离目录，模拟买入 200 股、成交 100 股、撤销剩余。输出包含 `FILE_PUBLISHED`、回报到达前的 `UNKNOWN`、`PARTIAL`、真实模拟回报决定的 `CANCELLED`，最后给出 `ISOLATED_MATCHED`。模拟器不包含交易所撮合、手续费或完整交易规则，不能替代 trade 的 SIM 引擎或连续交易日证据。

`submit`、`cancel`、`reconcile` 从标准输入读取 JSON，`query` 使用 `--sid`。示例：

```powershell
'{"sid":"demo-another-1","symbol":"600519.SH","side":"buy","price":"10.50","volume":200}' |
  python -m novatrader_broker submit --root $demoRoot --account TEST_ACCOUNT
python -m novatrader_broker query --root $demoRoot --account TEST_ACCOUNT --sid demo-another-1
```

价格使用十进制字符串。金额、数量、身份及协议枚举严格校验；不对不合法价格静默四舍五入，不猜北交所映射。板块手数、停牌、涨跌停、风控和授权由 trade/risk 负责，网关本身不赋予下单资格。

## 文件与恢复合同

```text
隔离根目录/
  .novatrader-isolated.json
  input/                      已完整发布的 CSV 与 .fin
  output/TEST_ACCOUNT/        终端协议模拟器的五类输出
  staging/                    输入目录外暂存；完整写入并 fsync
  spool/<identity-sha256>/     持久运输回执，不是业务订单数据库
```

官方扫描器在发现新增/修改 CSV 时就可以处理；`.fin` 仅表示不再追加，允许终端清理文件。发布使用同文件系统硬链接，使完整 CSV 一次可见且不会覆盖目标。输入目录、暂存、spool 必须处于本地文件系统；拒绝 UNC、符号链接和 junction；不支持硬链接的文件系统会失败。断电后的文件系统持久性尚未现场验证。

先持久占用 `account + kind + sid/request_id`，再发布文件。同一身份同一内容返回原运输结果，内容不同报冲突；终端清理 CSV 后也不能重写。占用后崩溃、磁盘满、权限失败或发布状态不确定都返回/恢复为 `UNKNOWN`，只查原 sid。不得通过删 spool、更换 sid 或反复提交绕过不确定状态。保留 spool 与业务 PG 意图的对应关系，归档规则待正式回报接入后制定。

覆盖文件采用打开前后 inode/长度/mtime 检查，追加文件只读完整行。缺失、截断、未知枚举、身份矛盾、非有限金额、错误数量、陈旧/未来 mtime 都阻断就绪。重复成交按账户、sid、exec_id 去重；同身份内容冲突停止对账；`recv_at` 改变不会产生新业务成交版本。状态包含 source `updated_at` 与内容版本，不因晚到旧回报回退成交数量。

单文件最多 8 MiB/100000 行。当前重读完整前缀，超过限制明确阻塞，没有偷偷丢数据或跳过游标。消费端须按订单/成交身份持久去重并实现归档、断点和回放后才能启用长期运行；本网关不另建 SQLite 或第二套订单账本。

`reports_ready` 表示文件齐全、可解析且覆盖文件 mtime 在阈值内，不能证明终端在线或账户已登录。`terminal_login_verified=false`、`cross_file_atomic=false`、`can_open_live=false` 始终如实返回。空持仓文件的完整表头表示零持仓；缺失文件不表示零。

## 双向 TLS 传输

接口先定义于 [gateway.proto](../backend/api/broker/v1/gateway.proto)，生成 Go protobuf/HTTP/gRPC 合同。实际 Python 服务使用 **HTTPS JSON**，未运行 gRPC listener。Go 调用口为 [pkg/brokerif/remote](../backend/pkg/brokerif/remote/client.go)，不会实现把文件写入等同 ACK 的旧 `brokerif.Adapter`。

服务默认不启动。复制 `tls.example.json` 到隔离运行配置并配置临时测试 CA、服务端证书和密钥，服务端证书必须包含客户端实际访问地址的 SAN。允许列表填写测试客户端叶证书 DER 的 SHA256 小写十六进制指纹。示例中的占位符故意不能通过启动校验。

```powershell
python -m novatrader_broker serve --root $demoRoot --account TEST_ACCOUNT --tls-config C:/isolated/tls.json
```

双方校验证书，服务端还校验客户端指纹；每个进程固定一个账户，所有请求必须带 `X-NT-Protocol: eastmoney-file-csv-isolated-v1` 与 `X-NT-Account: TEST_ACCOUNT`。没有无 TLS 开关、跳过证书验证开关或默认公共监听。跨机测试需在独立证书和隔离目录准备后显式配置地址。

| 接口 | 语义 |
| --- | --- |
| `POST /v1/broker/orders`，`{"order":{...}}` | 发布运输意图，返回 FILE_PUBLISHED/UNKNOWN，不是 ACK |
| `GET /v1/broker/orders/{sid}` | 原 sid 委托/成交证据与 status_version |
| `POST /v1/broker/cancels`，`{"request_id":"...","sid":"..."}` | 发布撤单请求；只有状态 5 表示已撤 |
| `GET /v1/broker/snapshot` | 五类文件证据、摘要及可用性 |
| `GET /v1/broker/health` | 文件就绪、运输未知、订单未知计数 |
| `POST /v1/broker/reconcile`，`{"expected":{...}}` | 比较调用方可信账户快照，无账本写入 |

对账 expected 必须包含 `account_id/balance/available/frozen/positions/orders`；持仓为 `symbol/volume/available_now`；委托为 `sid/state/filled_volume`。检查资金误差、总量/可卖、在途和未知单、累计成交数量及金额。结果只为 `ISOLATED_MATCHED` 或 `BLOCKED`，不自行恢复开仓。

网络超时后的调用方必须保留原意图、按原 sid 查询，不自动重试 POST。即便查询返回 `ACKNOWLEDGED`，也要检查 `evidence_ready`、来源及业务资格；过期证据不能成为新的批准。撤单发布后仍需等回报再计算剩余量，不能立即追价。

## 测试与验收边界

```powershell
python -m unittest discover -s tests -v
# 在 backend 目录；必须设置绝对 Python 路径，否则跨进程测试明确 SKIP
$env:NT_BROKER_PYTHON = 'C:/path/to/python.exe'
go test -race -count=1 ./pkg/brokerif/emfile ./pkg/brokerif/remote ./api/broker/v1
```

测试只使用系统临时隔离目录、临时证书和 loopback 随机端口；包括真正子进程退出后恢复。测试结束关闭 Python TLS 进程并清理临时目录。磁盘满/权限失败采用故障注入，不修改磁盘权限或填满真实磁盘。

当前未验收：实际券商终端、真实 CSV 编码/列映射、跨主机 mTLS 运维、终端登录探测、持续 trade PG 回报事务/outbox、输出轮换、断电恢复及真实交易日准入。`trade` 实盘通道保持未接入；任何现场验证另按准入与授权执行。
