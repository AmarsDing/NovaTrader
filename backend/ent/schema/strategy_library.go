package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// StrategyLibrary 策略库。对齐 sql/init_db.sql。
type StrategyLibrary struct {
	ent.Schema
}

func (StrategyLibrary) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "strategy_library"}}
}

func (StrategyLibrary) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").MaxLen(100).NotEmpty().Unique(),
		field.Text("description").Optional().Nillable(),
		field.Text("code").Optional().Nillable(),
		field.JSON("backtest_result", map[string]any{}).Optional(),
		field.String("status").MaxLen(20).Default("pending").
			Comment("pending / active / deprecated"),
	}
}
