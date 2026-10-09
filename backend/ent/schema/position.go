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

// Position 持仓。对齐 sql/init_db.sql，并补上可卖数量，供 T+1 使用。
type Position struct {
	ent.Schema
}

func (Position) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "positions"}}
}

func (Position) Fields() []ent.Field {
	return []ent.Field{
		field.String("book").MaxLen(16).Default("paper").Comment("paper 模拟盘，live 实盘"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Int("quantity").Default(0).Comment("持仓股数"),
		field.Int("available").Default(0).Comment("可卖股数"),
		field.Float("avg_cost").Optional().Nillable().SchemaType(numericPrice),
		field.Float("current_price").Optional().Nillable().SchemaType(numericPrice),
		field.Float("pnl").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "numeric(18,4)"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (Position) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("book", "symbol").Unique(),
	}
}
