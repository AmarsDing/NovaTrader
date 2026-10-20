package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ReportEstimate 券商研报盈利预测（目录册增补 B4，2026-10-20）。
// 来源：东财 reportapi 研报列表的预测列（EPS×3 年/PE×3 年/目标价区间/评级与动作），
// 一行 = 一份研报（info_code 幂等锚）× 一个预测年度（est_year = 披露年 +0/1/2）。
// PIT 口径明说：不同券商、不同时点研报各自成行、互不覆盖——新研报 = 新 info_code = 新行。
// 同一份研报多次重采按 (info_code, est_year) 幂等 upsert 同一行，upsert 合同三条：
// ①批内按幂等键去重（同键双现取最后一份）；②冲突只刷锚列与 source；③披露列
// 「有值才覆盖、nil 保留旧值」——缺披露≠撤披露，绝不把已披露值抹回 NULL
// （同 stock_basic 可空字段条件覆盖先例）。供一致预期「多券商分歧」截面按 publish_date 窗口查询。
// 「没取到≠0」纪律：eps/pe/目标价/评级/动作等披露列缺披露时保持 NULL（源头空串/非数→NULL），
// 禁止落 0 或空串冒充有披露；eps 与 pe 同为 NULL 的年份不建行（写入侧纪律，
// 与解析层双 nil 年度点整条丢弃同则）；org_sname 为签发主体必有列，源头空串的行不入库。
type ReportEstimate struct {
	ent.Schema
}

func (ReportEstimate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "report_estimate"}}
}

func (ReportEstimate) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty().Comment("证券代码，归一化 600519.SH"),
		field.Time("publish_date").SchemaType(pgDate).Comment("研报发布日期"),
		field.String("org_code").MaxLen(16).Optional().Nillable().Comment("东财机构码；源头偶发空串→NULL"),
		field.String("org_sname").MaxLen(32).NotEmpty().Comment("签发机构短名（研报必有主体）"),
		field.String("info_code").MaxLen(32).NotEmpty().Comment("东财研报 ID，幂等锚"),
		field.Int("est_year").Comment("预测年度（披露年 +0/1/2）"),
		field.Float("eps").Optional().Nillable().SchemaType(numericPrice).Comment("预测每股收益（元）；缺披露 NULL"),
		field.Float("pe").Optional().Nillable().SchemaType(numericPrice).Comment("预测市盈率（倍）；缺披露 NULL"),
		field.Float("aim_price_low").Optional().Nillable().SchemaType(numericPrice).Comment("目标价下限（元）；多数研报无目标价→NULL"),
		field.Float("aim_price_high").Optional().Nillable().SchemaType(numericPrice).Comment("目标价上限（元）；同上"),
		field.String("rating").MaxLen(16).Optional().Nillable().Comment("本次评级原文；空串→NULL 不空串占位"),
		field.String("last_rating").MaxLen(16).Optional().Nillable().Comment("前次评级原文；空串→NULL"),
		field.Int("rating_change").Optional().Nillable().Comment("评级动作枚举（东财无公开口径，原样透传）；非数→NULL"),
		field.Int("report_type").Optional().Nillable().Comment("研报类型枚举（同上原样透传）；非数→NULL"),
		field.String("researcher").MaxLen(64).Optional().Nillable().Comment("研究员；空串→NULL"),
		field.String("industry").MaxLen(32).Optional().Nillable().Comment("东财行业名；空串→NULL"),
		field.String("source").MaxLen(16).NotEmpty().Comment("采集源标识（如 eastmoney）；写入侧必须显式赋值，不留 Default 逃路"),
	}
}

func (ReportEstimate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("info_code", "est_year").Unique(),
		index.Fields("symbol", "publish_date"),
		index.Fields("publish_date"),
	}
}
