package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// EquityUnlock 限售解禁计划，由 M01（datahub unlock 域）从东财数据中心 RPT_LIFT_STAGE 滚动写入。
// 每行是一只股票的一次解禁批次；free_date 是解禁上市日。M05 复盘用它评估未来 90 天筹码供给。
// 源头报表不拆股东（只有批次股东数），shares 单位万股与源头一致；
// ratio_pct / total_ratio_pct / market_value 源头没给到就留 NULL——没取到不等于 0。
type EquityUnlock struct {
	ent.Schema
}

func (EquityUnlock) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "equity_unlock"}}
}

func (EquityUnlock) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("free_date").SchemaType(pgDate).Comment("解禁上市日"),
		field.String("lift_type").MaxLen(64).Default("").Comment("解禁类型，来自源 FREE_SHARES_TYPE：首发原股东/定向增发/股权激励等"),
		field.Float("shares").Default(0).SchemaType(numericShares).Comment("解禁股数（万股，与源 ABLE_FREE_SHARES 口径一致）"),
		field.Int("batch_holder_num").Optional().Nillable().Comment("该批次股东数（源 BATCH_HOLDER_NUM）"),
		field.Float("ratio_pct").Optional().Nillable().Comment("解禁数量占流通股本比例（%，源 FREE_RATIO）"),
		field.Float("total_ratio_pct").Optional().Nillable().Comment("解禁数量占总股本比例（%，源 TOTAL_RATIO）"),
		field.Float("market_value").Optional().Nillable().SchemaType(numericMoney).Comment("解禁市值（元）；源报表无市值列时留空"),
		field.String("source").MaxLen(16).Default(""),
		field.Time("updated_at"),
	}
}

func (EquityUnlock) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "free_date", "lift_type").Unique(),
		index.Fields("free_date"),
	}
}
