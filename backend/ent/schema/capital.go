package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MoneyFlow 个股资金流向，由 M01 从 Tushare / 东财写入，M02 算资金因子（FR-02-06）。金额单位元，正为净流入。
type MoneyFlow struct {
	ent.Schema
}

func (MoneyFlow) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "money_flow"}}
}

func (MoneyFlow) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate),
		field.Float("main_net").Default(0).SchemaType(numericMoney).Comment("主力 = 超大单 + 大单"),
		field.Float("super_net").Default(0).SchemaType(numericMoney),
		field.Float("big_net").Default(0).SchemaType(numericMoney),
		field.Float("mid_net").Default(0).SchemaType(numericMoney),
		field.Float("small_net").Default(0).SchemaType(numericMoney),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (MoneyFlow) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "trade_date").Unique(),
		index.Fields("trade_date"),
	}
}

// LhbSeat 龙虎榜席位明细，由 M01 写入。同一席位同时出现在买卖榜时合并成一行。
type LhbSeat struct {
	ent.Schema
}

func (LhbSeat) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "lhb_seat"}}
}

func (LhbSeat) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate),
		field.String("reason").MaxLen(128).NotEmpty().Comment("上榜原因"),
		field.String("seat_name").MaxLen(128).NotEmpty(),
		field.Float("buy_amount").Default(0).SchemaType(numericMoney),
		field.Float("sell_amount").Default(0).SchemaType(numericMoney),
	}
}

func (LhbSeat) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "trade_date", "reason", "seat_name").Unique(),
		index.Fields("trade_date"),
	}
}

// SeatTag 席位标签，人工维护。tag：INSTITUTION 机构、HOT_MONEY 游资、QUANT 量化、NORTH 北向。
type SeatTag struct {
	ent.Schema
}

func (SeatTag) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "seat_tag"}}
}

func (SeatTag) Fields() []ent.Field {
	return []ent.Field{
		field.String("seat_name").MaxLen(128).NotEmpty(),
		field.String("tag").MaxLen(16).NotEmpty(),
		field.String("alias").MaxLen(64).Default("").Comment("游资别名，例如某某路"),
	}
}

func (SeatTag) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("seat_name").Unique(),
	}
}
