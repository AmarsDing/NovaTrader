package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TradeFill 成交。一笔委托可以有多笔成交，费用按本笔计算。
type TradeFill struct {
	ent.Schema
}

func (TradeFill) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trade_fill"}}
}

func (TradeFill) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_order_id").MaxLen(64).NotEmpty(),
		field.String("book").MaxLen(16).Default("paper"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("side").MaxLen(8).NotEmpty(),
		field.Int("qty"),
		field.Float("price").SchemaType(numericPrice),
		field.Float("amount").SchemaType(numericMoney),
		field.Float("fee").SchemaType(numericMoney),
		field.String("signal_id").MaxLen(64).Default(""),
		field.String("strategy_version").MaxLen(64).Default(""),
		// exec_id 是券商成交回报的柜台成交号（M13-T04 实盘回报归并）。
		// 可空：SIM 撮合成交没有柜台成交号，PG 唯一索引天然跳过 NULL；
		// LIVE 成交必须带来 exec_id，幂等键见下面 (book, exec_id) 唯一索引。
		field.String("exec_id").MaxLen(96).Optional().Nillable(),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (TradeFill) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_order_id"),
		index.Fields("book", "created_at"),
		// 回报重放兜底：PG 唯一约束让重复 exec_id 静默跳过（DoNothing），
		// biz 侧 HasFillExec 预检是正常路径，唯一索引防并发与修复脚本漏判。
		index.Fields("book", "exec_id").Unique(),
	}
}
