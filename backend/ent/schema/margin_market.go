package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MarginMarket 全市场两融余额/买卖汇总，交易所维度（SSE/SZSE/BSE 每交易日一行）。
// 由 M01 datahub margin_mkt 域次交易日盘前（T+1）写入，主源 Tushare margin 接口。
// 与 margin_detail 个股明细互补：这里答「全市场杠杆水位」而不是「单票杠杆」。
// 金额为元，融券卖出量单位为股（Tushare 源为万股/手？入库前按源口径换元/股——
// 实际 Tushare margin 返回值为元与股，见 data/tushare.go MarginMkt 注释）。
// 数值列全部 Nillable：源端某列缺发时保持 NULL，禁止落 0 冒充（0.数据纪律 §3）。
type MarginMarket struct {
	ent.Schema
}

func (MarginMarket) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "margin_market"}}
}

func (MarginMarket) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.String("exchange").MaxLen(8).NotEmpty().Comment("SSE/SZSE/BSE"),
		field.Float("rzye").Optional().Nillable().SchemaType(numericAmount).Comment("融资余额，元"),
		field.Float("rzmre").Optional().Nillable().SchemaType(numericAmount).Comment("融资买入额，元"),
		field.Float("rzche").Optional().Nillable().SchemaType(numericAmount).Comment("融资偿还额，元"),
		field.Float("rqye").Optional().Nillable().SchemaType(numericAmount).Comment("融券余额，元"),
		field.Int64("rqmcl").Optional().Nillable().Comment("融券卖出量，股"),
		field.Float("rzrqye").Optional().Nillable().SchemaType(numericAmount).Comment("融资融券余额，元"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (MarginMarket) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "exchange").Unique(),
		index.Fields("trade_date"),
	}
}
