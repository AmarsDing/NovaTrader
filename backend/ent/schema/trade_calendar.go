package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TradeCalendar 交易日。休市和调休以国务院放假安排为准，交易所临时停市可另改一行。
type TradeCalendar struct {
	ent.Schema
}

func (TradeCalendar) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trade_calendar"}}
}

func (TradeCalendar) Fields() []ent.Field {
	return []ent.Field{
		field.Time("day").Unique().
			SchemaType(map[string]string{dialect.Postgres: "date"}),
		field.Bool("is_open"),
		field.String("note").MaxLen(64).Default(""),
	}
}

func (TradeCalendar) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("is_open"),
	}
}
