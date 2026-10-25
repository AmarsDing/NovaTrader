// report_contract.go：报告契约版号与 stats payload 键集的唯一出处（W2a；UI 宪法 R-UI-25 配套）。
//
// 纪律：
//   - schema_version 只写本包常量；service 拼装回执时引用，前端守卫按同版号对齐；
//   - payload 形状破坏性变化（改键名/删键/语义变更）→ 对应版号 +1，同提交回写本文件、
//     brain.proto 注释、前端守卫；只增不破可不变版号；
//   - 「没取到≠0」：键缺失即显式缺失，消费方显「—」，禁止回填 0 冒充。
package biz

// 报告回执 schema_version 现行值；0 = 契约落地前的旧数据（前端走旧路径并给提示位）。
const (
	SchemaVersionAnalysis        = 1
	SchemaVersionBriefing        = 1
	SchemaVersionProgramReview   = 1
	SchemaVersionPredictionCheck = 1
)

// ProgramReview.kind 三态（对齐 brain.proto 注释；daily_reviews.kind 的同名字面量以本常量为准逐步收编）。
const (
	ReviewKindProgram = "program" // 八格程序版（review.go）
	ReviewKindAI      = "ai"      // 分段 AI 日复盘（review_ai.go，P2-8 六段）
	ReviewKindAIWeek  = "ai_week" // AI 周报（review_weekly.go，行落本周最后交易日）
)

// ProgramStatsKeysV1 是 kind=program 的 stats_json v1 键集（八格读数 + missing）。
// 格内样本不足/不适用时该格对应键不出；missing 为必出键，登记全部缺口。
var ProgramStatsKeysV1 = []string{
	"advance", "decline", "amount", // 格 1 涨跌家数
	"amount_avg20_ratio", "amount_avg60_ratio", // 格 2 成交额均比（样本不足时后者不出）
	"up", "down", "broken", "seal_rate", "max_height", "structure_gaps", // 格 3 涨停结构（封板率无分母时 seal_rate 不出）
	"tier_avg", "tier_median", "tier_red_rate", "tier_relimit", // 格 4 昨日涨停今表现
	"promo_12", "promo_23", "promo_high", // 格 5 晋级
	"margin_total", "margin_chg_5d", // 格 6 两融
	"index_above_20", "index_above_60", "index_above_250", // 格 7 指数位置
	"themes_top5", // 格 8 题材
	"missing", // 全格共用的显式缺失登记（必出键）
}

// AIReviewSegmentIDsV1 是 kind=ai 的 stats_json segments 键序（与 aiSegmentOrder 一一对应）。
// 段值形状：{text, points[], evidence[], degraded}。
var AIReviewSegmentIDsV1 = []string{"emotion", "capital", "theme", "lhb", "leader", "supply"}

// AIReviewStatsKeysV1 是 kind=ai 的 stats_json v1 顶层键集。
var AIReviewStatsKeysV1 = []string{
	"segments", "evidence", "omitted_count", "model", "sealed_at",
	"partial", "degraded_segments", "missing",
}

// AIWeekStatsKeysV1 是 kind=ai_week 的 stats_json v1 顶层键集；days 为逐日读数行。
var AIWeekStatsKeysV1 = []string{
	"window_start", "window_end", "day_count", "expected_days",
	"usable_days", "days", "sealed_at", "missing",
}
