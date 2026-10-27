package gateway

import (
	adminv1 "server/api/admin/v1"
	"sync"
)

// 配置 schema 下发目录的唯一事实源（本批 = 后端目录，前端 config-catalog.ts 待 7b
// 改由 GET /api/config/schema 拉取后整体迁移；本批仅交付端点与目录本身）。
// 同步纪律：新键先在 pkg/params（validate.go 的 numericRules / appAllowed）登记
// 以通过 ValidatePatch，再在这里挂目录项；config-schema_test.go 的 conformance
// 测试以 ValidatePatch 单点打满整个目录——未登记键会被 CI 抓住。
//
// kind 语义对齐前端 FieldKind：number|int|text|bool|json|secret|select。
// store 语义对齐前端 FieldStore：config|risk。
//
// 数据纪律：fallback 必须等于引擎兜底（DefaultRules/config.yaml），不能照抄旧前端
// 硬编码（旧盘曾把急拉阈值写成 3/5 而引擎是 1.5/3、tdx_local.on 写成 true 而
// E04 裁决为 false）——schema 一经下发就成为全站事实源，错位会被用户当真值。

func numF(key, label, hint, fallback, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{Key: key, Label: label, Hint: hint, Kind: "number", Fallback: fallback, Store: "config", Section: section}
}
func intF(key, label, hint, fallback, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{Key: key, Label: label, Hint: hint, Kind: "int", Fallback: fallback, Store: "config", Section: section}
}
func riskF(key, label, hint, fallback, kind, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{Key: "risk." + key, Label: label, Hint: hint, Kind: kind, Fallback: fallback, Store: "risk", Section: section}
}
func blobF(jsonKey, label, hint, fallback, kind, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{Key: jsonKey, JsonKey: jsonKey, Label: label, Hint: hint, Kind: kind, Fallback: fallback, Store: "config", Section: section}
}
func appF(key, label, hint, fallback, kind, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{Key: key, Label: label, Hint: hint, Kind: kind, Fallback: fallback, Store: "config", Section: section}
}
func limF(key, label, fallback, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{
		Key: key, Label: label, Kind: "int", Fallback: fallback, Store: "config", Section: section,
		Hint: "留空跟随配置文件（当前 " + fallback + "）", Clearable: true,
	}
}
func pickF(key, label, hint, fallback string, options []*adminv1.ConfigSchemaOption, section string) *adminv1.ConfigSchemaField {
	return &adminv1.ConfigSchemaField{Key: key, Label: label, Hint: hint, Kind: "select", Fallback: fallback, Store: "config", Section: section, Options: options}
}

var modelTierOptions = []*adminv1.ConfigSchemaOption{
	{Value: "small", Label: "小模型"},
	{Value: "large", Label: "大模型"},
}

// buildConfigSchemaCatalog 构造完整目录。新增字段只在这里追加。
func buildConfigSchemaCatalog() []*adminv1.ConfigSchemaCategory {
	categories := []*adminv1.ConfigSchemaCategory{
		{
			Id: "datasource", Title: "数据接口",
			Note: "每一路单独填写。保存后几秒内盖过配置文件。没填的令牌和地址继续用配置文件或环境变量。",
			Fields: []*adminv1.ConfigSchemaField{
				appF("app.ds.tushare.on", "启用", "日线、资金、龙虎榜", "true", "bool", "Tushare"),
				appF("app.ds.tushare.token", "令牌", "填了就优先于环境变量 TUSHARE_TOKEN", "", "secret", "Tushare"),
				limF("app.ds.tushare.qps", "每秒请求", "2", "Tushare"),
				limF("app.ds.tushare.quota", "每日配额", "0", "Tushare"),
				limF("app.ds.tushare.timeout_sec", "超时秒数", "15", "Tushare"),
				appF("app.ds.tavily.on", "启用", "新闻搜索", "true", "bool", "Tavily"),
				appF("app.ds.tavily.token", "密钥", "填了就优先于环境变量 TAVILY_API_KEY", "", "secret", "Tavily"),
				appF("app.ds.tavily.feeds", "查询词", "逗号分隔，例如 A股 快讯,证监会", "A股 快讯,证监会", "text", "Tavily"),
				limF("app.ds.tavily.timeout_sec", "超时秒数", "20", "Tavily"),
				appF("app.ds.pyworker", "边车地址", "通达信和 AkShare 共用这个 pyworker", "http://127.0.0.1:18080", "text", "通达信与 AkShare"),
				// E04 裁决：tdx_local 结构性死源默认关（configs/config.yaml tdx_local.enabled:false）
				appF("app.ds.tdx_local.on", "通达信官方", "盘中快照", "false", "bool", "通达信与 AkShare"),
				appF("app.ds.tdx_file.on", "通达信文件", "本机 vipdoc", "true", "bool", "通达信与 AkShare"),
				appF("app.ds.tdx_tcp.on", "通达信行情", "非官方 TCP", "true", "bool", "通达信与 AkShare"),
				appF("app.ds.akshare.on", "AkShare", "分钟线与外围备源", "true", "bool", "通达信与 AkShare"),
				limF("app.ds.tdx_local.timeout_sec", "超时秒数", "3", "通达信与 AkShare"),
				limF("app.ds.tdx_file.timeout_sec", "超时秒数", "60", "通达信与 AkShare"),
				limF("app.ds.tdx_tcp.timeout_sec", "超时秒数", "8", "通达信与 AkShare"),
				limF("app.ds.akshare.timeout_sec", "超时秒数", "30", "通达信与 AkShare"),
				appF("app.ds.eastmoney.on", "东方财富", "涨停池、资金备源。公开接口，不用令牌", "true", "bool", "公开行情"),
				appF("app.ds.sina.on", "新浪", "快照备源。公开接口，不用令牌", "true", "bool", "公开行情"),
				appF("app.ds.tencent.on", "腾讯", "快照备源。公开接口，不用令牌", "true", "bool", "公开行情"),
				appF("app.ds.cninfo.on", "巨潮", "公告。公开接口，不用令牌", "true", "bool", "公开行情"),
				limF("app.ds.eastmoney.qps", "每秒请求", "4", "公开行情"),
				limF("app.ds.eastmoney.quota", "每日配额", "0", "公开行情"),
				limF("app.ds.eastmoney.timeout_sec", "超时秒数", "10", "公开行情"),
				{Key: "app.ds.eastmoney.margin_reports", Label: "两融报表名",
					Hint: "两份名单逗号分隔，报表轮换时应急换名；留空用内置默认 RPTA_WEB_RZRQ_GGMX 等",
					Kind: "text", Fallback: "", Store: "config", Section: "公开行情",
					Clearable: true, Wide: true},
				limF("app.ds.sina.qps", "每秒请求", "8", "公开行情"),
				limF("app.ds.sina.quota", "每日配额", "0", "公开行情"),
				limF("app.ds.sina.timeout_sec", "超时秒数", "5", "公开行情"),
				limF("app.ds.tencent.qps", "每秒请求", "8", "公开行情"),
				limF("app.ds.tencent.quota", "每日配额", "0", "公开行情"),
				limF("app.ds.tencent.timeout_sec", "超时秒数", "5", "公开行情"),
				limF("app.ds.cninfo.qps", "每秒请求", "2", "公开行情"),
				limF("app.ds.cninfo.quota", "每日配额", "0", "公开行情"),
				limF("app.ds.cninfo.timeout_sec", "超时秒数", "15", "公开行情"),
				appF("app.ds.rss.on", "启用", "打开后按订阅地址拉取", "false", "bool", "RSS"),
				{Key: "app.ds.rss.feeds", Label: "订阅地址", Hint: "一行一个，或用逗号分隔", Kind: "text", Fallback: "", Store: "config", Section: "RSS", Wide: true},
				appF("app.ds.sina7x24.on", "新浪 7×24 快讯", "快讯副线，与东财快讯互备；需 Referer", "true", "bool", "快讯与回补"),
				limF("app.ds.sina7x24.timeout_sec", "超时秒数", "10", "快讯与回补"),
				appF("app.ds.baostock.on", "Baostock 回补", "盘后日线/财务三表备源。pyworker 装好 baostock 前保持关闭", "false", "bool", "快讯与回补"),
				appF("app.ds.openbb_fred.on", "OpenBB FRED", "宏观备源。装配后生效", "false", "bool", "备用源（随版本装配，默认关）"),
				appF("app.ds.openbb_fred.key", "FRED 密钥", "留空跟随配置文件", "", "secret", "备用源（随版本装配，默认关）"),
				appF("app.ds.tdx_http_archive.on", "通达信历史存档", "盘后日线备源。装配后生效", "false", "bool", "备用源（随版本装配，默认关）"),
			},
		},
		{
			Id: "models", Title: "模型",
			Note: "地址留空的档位继续用配置文件。填上后几秒内，新的研判、情报重评和嵌入会走这里。",
			Fields: []*adminv1.ConfigSchemaField{
				appF("app.brain.large.url", "大模型地址", "OpenAI 兼容，例如 http://127.0.0.1:8000/v1", "", "text", "智脑"),
				appF("app.brain.large.model", "大模型名", "综合、消息、外围、晨报、追问", "Qwen3.5-122B-A10B-GPTQ-Int4", "text", "智脑"),
				appF("app.brain.large.key", "大模型密钥", "本地 vLLM 通常留空", "", "secret", "智脑"),
				appF("app.brain.small.url", "小模型地址", "可以和大模型是同一个服务", "", "text", "智脑"),
				appF("app.brain.small.model", "小模型名", "技术面、资金面、异动解读", "Qwen2.5-7B-Instruct-GPTQ-Int8", "text", "智脑"),
				appF("app.brain.small.key", "小模型密钥", "本地 vLLM 通常留空", "", "secret", "智脑"),
				appF("app.intel.enabled", "情报重评", "打开后用模型修正规则分", "false", "bool", "情报"),
				appF("app.intel.url", "情报模型地址", "小模型和大模型共用这个地址", "http://127.0.0.1:8000/v1", "text", "情报"),
				appF("app.intel.small", "情报小模型", "先打分", "", "text", "情报"),
				appF("app.intel.large", "情报大模型", "重要消息再复核。留空则只走小模型", "", "text", "情报"),
				appF("app.intel.key", "情报模型密钥", "", "", "secret", "情报"),
				pickF("app.kb.provider", "嵌入方式", "不嵌入时知识库只做关键词检索", "none", []*adminv1.ConfigSchemaOption{
					{Value: "none", Label: "不嵌入"},
					{Value: "openai", Label: "OpenAI 兼容向量"},
					{Value: "hash", Label: "哈希（仅开发）"},
				}, "知识库"),
				appF("app.kb.endpoint", "嵌入地址", "openai 时必填", "", "text", "知识库"),
				appF("app.kb.model", "嵌入模型", "维度必须是 1024", "BAAI/bge-large-zh-v1.5", "text", "知识库"),
				appF("app.kb.key", "嵌入密钥", "", "", "secret", "知识库"),
			},
		},
		{
			Id: "agents", Title: "智能体",
			Note: "四个维度加综合、晨报、解读、追问。权重保存后按比例归一。下一次研判生效。",
			Fields: []*adminv1.ConfigSchemaField{
				pickF("app.agent.technical", "技术面", "", "small", modelTierOptions, "档位"),
				pickF("app.agent.capital", "资金面", "", "small", modelTierOptions, "档位"),
				pickF("app.agent.news", "消息面", "", "large", modelTierOptions, "档位"),
				pickF("app.agent.external", "外围", "", "large", modelTierOptions, "档位"),
				pickF("app.agent.synthesis", "综合", "", "large", modelTierOptions, "档位"),
				pickF("app.agent.briefing", "晨报", "", "large", modelTierOptions, "档位"),
				pickF("app.agent.explain", "异动解读", "", "small", modelTierOptions, "档位"),
				pickF("app.agent.ask", "追问", "", "large", modelTierOptions, "档位"),
				appF("app.agent.w.technical", "技术面权重", "四维权重合计不为 1 时会按比例归一", "0.3", "number", "权重"),
				appF("app.agent.w.news", "消息面权重", "", "0.25", "number", "权重"),
				appF("app.agent.w.capital", "资金面权重", "", "0.3", "number", "权重"),
				appF("app.agent.w.external", "外围权重", "", "0.15", "number", "权重"),
			},
		},
		{
			Id: "notify", Title: "通知",
			Note: "飞书地址留空时只发桌面。保存后下一则通知开始使用。",
			Fields: []*adminv1.ConfigSchemaField{
				appF("app.notify.webhook", "飞书 Webhook", "留空则只走桌面通知", "", "secret", ""),
				appF("app.notify.secret", "飞书签名密钥", "机器人要求签名时填写", "", "secret", ""),
			},
		},
		{
			Id: "funnel", Title: "选股漏斗",
			Note: "硬过滤。下次扫描读取，写入参数表。",
			Fields: []*adminv1.ConfigSchemaField{
				intF("m06.min_list_days", "上市天数", "不足这个自然日数的新股不进漏斗", "90", ""),
				numF("m06.min_amount", "昨日成交额", "单位元。低于此值淘汰", "50000000", ""),
				intF("m06.rule_top_n", "规则保留", "规则分排序后留下的数量", "100", ""),
			},
		},
		{
			Id: "score", Title: "评分与信号",
			Note: "融合权重和信号节拍。下次扫描读取。",
			Fields: []*adminv1.ConfigSchemaField{
				numF("m06.rule_weight", "规则权重", "和模型权重一起用", "0.4", "融合"),
				numF("m06.ai_weight", "模型权重", "模型不可用时整笔退回规则分", "0.6", "融合"),
				numF("m06.select_score", "入选分", "综合分达到即入选", "75", "融合"),
				numF("m06.high_score", "高价值分", "达到后界面按高分信号显示", "85", "融合"),
				numF("m06.stage_coef.ICE", "冰点系数", "情绪阶段 ICE", "0.8", "情绪系数"),
				numF("m06.stage_coef.RECOVER", "修复系数", "情绪阶段 RECOVER", "0.95", "情绪系数"),
				numF("m06.stage_coef.WARM", "温和系数", "情绪阶段 WARM", "1", "情绪系数"),
				numF("m06.stage_coef.HOT", "火热系数", "情绪阶段 HOT", "1.05", "情绪系数"),
				numF("m06.stage_coef.FADE", "退潮系数", "情绪阶段 FADE", "0.85", "情绪系数"),
				intF("m06.ai_top_n", "送模型数量", "规则分靠前的才做四维研判", "30", "信号"),
				intF("m06.max_daily_signals", "每日买入信号", "当天最多发出的买入信号数", "5", "信号"),
				intF("m06.signal_ttl_minutes", "有效分钟", "盘中最晚收到 14:57", "30", "信号"),
				intF("m06.buy_cooldown_minutes", "买入冷却", "同一只再次买入的间隔，分钟", "240", "信号"),
				intF("m06.sell_cooldown_minutes", "卖出冷却", "同一只再次卖出的间隔，分钟", "5", "信号"),
			},
		},
		{
			Id: "price", Title: "价格与仓位",
			Note: "入场、止损、止盈和单票仓位由规则计算。下次扫描读取。",
			Fields: []*adminv1.ConfigSchemaField{
				intF("m06.atr_period", "ATR 周期", "日线根数", "14", ""),
				numF("m06.stop_atr", "止损 ATR", "止损距离，同时受最大止损比例约束", "2", ""),
				numF("m06.take_atr", "止盈 ATR", "止盈距离", "3", ""),
				numF("m06.max_stop_pct", "最大止损", "0.07 表示 7%", "0.07", ""),
				numF("m06.entry_band_atr", "入场带宽", "入场区间上下各这么多个 ATR", "0.25", ""),
				numF("m06.sell_slip_pct", "卖出滑点", "0.005 表示 0.5%", "0.005", ""),
				numF("m06.position_per_stock", "单票仓位", "信号给出的仓位比例", "0.05", ""),
				numF("m06.normal_position_factor", "常态系数", "乘在单票仓位上", "0.6", ""),
			},
		},
		{
			Id: "first", Title: "首板弱转强",
			Note: "模板一的阈值。下次扫描读取。",
			Fields: []*adminv1.ConfigSchemaField{
				intF("m06.fb.no_limit_days", "无涨停天数", "昨日之前多少个交易日不能收盘涨停", "5", ""),
				numF("m06.fb.amount_ratio", "放量倍数", "昨日成交额相对前 5 日均额", "1.5", ""),
				numF("m06.fb.gap_min", "高开下限", "开盘涨幅，0 表示不要求高开", "0", ""),
				numF("m06.fb.gap_max", "高开上限", "0.06 表示 6%", "0.06", ""),
				numF("m06.fb.strength_min", "现价强度", "当前涨幅下限", "0.02", ""),
				numF("m06.fb.big_amount", "大额加分", "昨日成交额达到即加分，单位元", "300000000", ""),
				intF("m06.fb.hold_days", "最长持有", "交易日", "2", ""),
			},
		},
		{
			Id: "pullback", Title: "强势回踩",
			Note: "模板二的阈值。下次扫描读取。",
			Fields: []*adminv1.ConfigSchemaField{
				intF("m06.pd.lookback", "回看天数", "用来确认强势和高点", "10", ""),
				numF("m06.pd.strong_gain", "强势涨幅", "区间涨幅达到才算强势", "0.2", ""),
				numF("m06.pd.dd_min", "回撤下限", "距高点回撤", "0.08", ""),
				numF("m06.pd.dd_max", "回撤上限", "超过则不再算回踩", "0.2", ""),
				numF("m06.pd.dd_ideal", "理想回撤", "打分时最理想的回撤", "0.12", ""),
				numF("m06.pd.ma_band", "均线偏离", "现价相对 MA10 的最大偏离", "0.03", ""),
				numF("m06.pd.shrink_max", "缩量上限", "昨日量相对前 5 日均量", "0.7", ""),
				numF("m06.pd.rebound_min", "反弹下限", "现价高出当日最低的比例", "0.01", ""),
				numF("m06.pd.floor_pct", "昨收地板", "现价不低于昨收的比例", "0.98", ""),
				intF("m06.pd.hold_days", "最长持有", "交易日", "5", ""),
			},
		},
		{
			Id: "risk", Title: "风控",
			Note: "保存生成新版本，风控约3秒轮询加载；以服务加载回执为准。比例用小数，0.05 表示 5%。",
			Fields: []*adminv1.ConfigSchemaField{
				riskF("live_admitted", "实盘准入", "此处只能关闭准入；开启须走准入流程。关闭后L2及以上会降到L1", "false", "bool", "准入"),
				riskF("allow_st_buy", "允许买 ST", "默认关闭", "false", "bool", "准入"),
				riskF("min_list_days", "上市天数", "风控侧的上市天数下限", "5", "int", "准入"),
				riskF("blacklist", "黑名单", "逗号分隔代码，如 600000.SH", "", "text", "准入"),
				riskF("bad_news_sentiment", "利空情感", "-1 到 0，低于此值视为突发利空", "-0.5", "number", "准入"),
				riskF("max_single_pct", "单票上限", "单只股票占总资产的上限", "0.05", "number", "仓位"),
				riskF("max_total_pct", "总仓上限", "股票仓位合计上限", "0.8", "number", "仓位"),
				riskF("max_sector_pct", "板块上限", "同一板块合计上限", "0.3", "number", "仓位"),
				riskF("max_daily_buys", "每日买入笔数", "当天买入次数上限", "10", "int", "仓位"),
				riskF("min_turnover", "最低成交额", "单位元", "50000000", "number", "仓位"),
				riskF("max_participation", "成交占比", "单笔相对成交额的上限", "0.01", "number", "仓位"),
				riskF("max_orders_per_sec", "每秒申报", "必须低于 300", "5", "int", "频率"),
				riskF("max_orders_per_day", "每日申报", "必须低于 20000", "1000", "int", "频率"),
				riskF("daily_loss_pct", "当日亏损", "达到后触发保护", "0.02", "number", "熔断"),
				riskF("index_drop_pct", "指数下跌", "指数跌幅达到后收缩", "0.02", "number", "熔断"),
				riskF("burst_rate_max", "炸板率", "超过后收缩", "0.5", "number", "熔断"),
				riskF("risk_level", "风险度下限", "0 到 3，和行情风险度取高者", "0", "int", "熔断"),
				riskF("stop_pct", "硬止损", "持仓亏损比例", "0.05", "number", "出场"),
				riskF("index_plunge_5m", "指数急跌", "5 分钟涨跌幅，负数", "-0.015", "number", "出场"),
				riskF("trail_start_pct", "移动止盈启动", "浮盈达到后开始跟踪", "0.05", "number", "出场"),
				riskF("trail_drawdown_pct", "移动回撤", "从高点回撤这么多就出", "0.03", "number", "出场"),
				riskF("time_stop_days", "时间止损天数", "持有达到后检查收益", "2", "int", "出场"),
				riskF("time_stop_min_gain", "时间止损收益", "到期时收益低于此值则出", "0.01", "number", "出场"),
				riskF("exit_slip_ticks", "出场滑点档", "按最小价位计算", "2", "int", "出场"),
				riskF("market_down_sec", "行情中断秒", "行情停更这么久停止开仓", "30", "int", "出场"),
				riskF("quote_max_delay_ms", "行情延迟", "毫秒，超过则这笔不按实时处理", "10000", "int", "新鲜度"),
				riskF("account_max_age_sec", "账户延迟", "秒，账户快照太旧则拒绝", "60", "int", "新鲜度"),
				riskF("reject_alert_window_sec", "拒单窗口", "秒", "300", "int", "拒单告警"),
				riskF("reject_alert_min", "拒单最少笔数", "0 表示关闭告警", "20", "int", "拒单告警"),
				riskF("reject_alert_rate", "拒单率", "窗口内拒单比例", "0.5", "number", "拒单告警"),
				riskF("reject_alert_cooldown_sec", "告警冷却", "秒", "1800", "int", "拒单告警"),
				riskF("phase_scale", "情绪仓位系数", "JSON。键是阶段名，值是 0 到 1", `{"":0.5,"FADE":0.3,"HOT":1,"ICE":0.2,"RECOVER":0.5,"WARM":0.8}`, "json", "系数"),
				riskF("risk_scale", "风险度系数", "JSON。键 0 到 3", `{"0":1,"1":0.7,"2":0.4,"3":0}`, "json", "系数"),
			},
		},
		{
			Id: "sentiment", Title: "情绪阶段", BlobKey: "market.sentiment.rules",
			Note: "行情服务在当日预热时读入。盘中保存后，下一次预热才换上，通常是下一交易日或重启行情服务。",
			Fields: []*adminv1.ConfigSchemaField{
				blobF("ice_max_up", "冰点涨停家数", "涨停少于此值，且跌停够多，判冰点", "30", "int", "阶段"),
				blobF("ice_min_down", "冰点跌停家数", "和涨停家数一起判断冰点", "30", "int", "阶段"),
				blobF("ice_max_profit", "冰点赚钱效应", "低于此值判冰点，单位是涨跌幅百分点", "-3", "number", "阶段"),
				blobF("fade_min_broken_rate", "退潮炸板率", "0.4 表示 40%", "0.4", "number", "阶段"),
				blobF("hot_min_up", "火热涨停", "家数", "80", "int", "阶段"),
				blobF("hot_max_broken_rate", "火热炸板率上限", "低于此值才算火热", "0.25", "number", "阶段"),
				blobF("hot_min_height", "火热高度", "最高连板", "5", "int", "阶段"),
				blobF("hot_min_profit", "火热赚钱效应", "百分点", "3", "number", "阶段"),
				blobF("warm_min_up", "温和涨停", "家数", "50", "int", "阶段"),
				blobF("warm_max_broken_rate", "温和炸板率上限", "低于此值才算温和", "0.35", "number", "阶段"),
				blobF("warm_min_profit", "温和赚钱效应", "百分点", "0", "number", "阶段"),
				blobF("index_symbol", "指数代码", "用来算风险度", "000001.SH", "text", "风险度"),
				blobF("risk3_index_pct", "极高：指数", "当日涨跌幅百分点，更低则风险度 3", "-3", "number", "风险度"),
				blobF("risk3_index_5m", "极高：5 分钟", "百分点", "-1.5", "number", "风险度"),
				blobF("risk2_index_pct", "高：指数", "百分点", "-2", "number", "风险度"),
				blobF("risk2_index_5m", "高：5 分钟", "百分点", "-1", "number", "风险度"),
				blobF("risk1_index_pct", "关注：指数", "百分点", "-1", "number", "风险度"),
				blobF("risk1_broken_rate", "关注：炸板率", "达到则风险度至少 1", "0.4", "number", "风险度"),
				// 引擎兜底 = pkg/market/sentiment.go DefaultRules（SurgePct=1.5, SurgeMinutes=3）
				blobF("surge_pct", "急拉幅度", "百分点，给异动提醒", "1.5", "number", "异动"),
				blobF("surge_minutes", "急拉分钟", "在这么多分钟内完成急拉", "3", "int", "异动"),
			},
		},
		{
			Id: "sector", Title: "板块热度", BlobKey: "market.sector.weights",
			Note: "和情绪阶段同一时刻读入，下一次行情预热后生效。",
			Fields: []*adminv1.ConfigSchemaField{
				blobF("pct", "涨幅权重", "板块涨幅在热度里的权重", "1", "number", ""),
				blobF("limit", "涨停权重", "涨停家数的权重", "1", "number", ""),
				blobF("breadth", "广度权重", "上涨家数的权重", "5", "number", ""),
				blobF("overseas", "外围权重", "外围联动的权重", "0.5", "number", ""),
				blobF("min_members", "最少成分", "少于此数的板块不参与", "5", "int", ""),
				blobF("leader_min_amount", "龙头成交额", "单位元", "100000000", "number", ""),
			},
		},
	}
	// BlobKey 回填：blob 字段的 BlobKey = 分类.BlobKey（对齐前端 fieldId(blobKey, field)）。
	for _, c := range categories {
		if c.BlobKey == "" {
			continue
		}
		for _, f := range c.Fields {
			f.BlobKey = c.BlobKey
			if f.JsonKey == "" {
				f.JsonKey = f.Key
			}
		}
	}
	return categories
}

// 目录惰性构建：pb 类型每次只会被同一进程构造一次，热路径零分配。
var configSchemaPayload = sync.OnceValue(func() *adminv1.ConfigSchemaReply {
	return &adminv1.ConfigSchemaReply{SchemaVersion: 1, Categories: buildConfigSchemaCatalog()}
})
