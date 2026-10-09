package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// TradeCash 账户资金。一个账本一行。模拟盘满 paper_days 个交易日后才允许实盘开仓。
type TradeCash struct {
	ent.Schema
}

func (TradeCash) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trade_cash"}}
}

func (TradeCash) Fields() []ent.Field {
	return []ent.Field{
		field.String("book").MaxLen(16).NotEmpty().Unique(),
		field.Float("cash").SchemaType(numericMoney),
		field.Float("realized_pnl").Default(0).SchemaType(numericMoney),
		field.Int("paper_days").Default(0).Comment("模拟盘已过的交易日数"),
		field.Bool("open_halted").Default(false).Comment("对账严重不平后停止开仓"),
		field.String("halt_reason").MaxLen(200).Default(""),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}
