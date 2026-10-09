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

// StrategyCandidate 选股候选。M06 设计文档第 10、11 节：盘前池、盘中池，以及入选和漏选的 T+1/T+3/T+5 后验。
// 同一交易日、同一模板、同一代码只有一行，多次扫描只更新分数和阶段。
type StrategyCandidate struct {
	ent.Schema
}

func (StrategyCandidate) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "strategy_candidates"}}
}

func (StrategyCandidate) Fields() []ent.Field {
	numericScore := map[string]string{dialect.Postgres: "numeric(8,4)"}
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.String("strategy").MaxLen(64).NotEmpty(),
		field.Int("strategy_version_id").Optional().Nillable(),
		field.String("stock_code").MaxLen(16).NotEmpty(),
		field.String("stock_name").MaxLen(32).Default(""),
		field.String("pool").MaxLen(16).Default("pre").Comment("pre 盘前池，intraday 盘中池"),
		field.String("stage").MaxLen(16).Default("ranked").Comment("ranked / ai_scored / selected"),
		field.String("miss_reason").MaxLen(32).Default("").Comment("没入选的原因，入选后清空"),
		field.Float("rule_score").Default(0).SchemaType(numericScore),
		field.Float("ai_score").Optional().Nillable().SchemaType(numericScore),
		field.Float("final_score").Optional().Nillable().SchemaType(numericScore),
		field.Int("signal_id").Optional().Nillable(),
		field.Float("ref_price").SchemaType(numericPrice).Comment("后验基准价：入选为入场价，漏选为首次进前 100 时的价格"),
		field.Float("ret_t1").Optional().Nillable().SchemaType(numericRatio),
		field.Float("ret_t3").Optional().Nillable().SchemaType(numericRatio),
		field.Float("ret_t5").Optional().Nillable().SchemaType(numericRatio),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(pgTimestamptz),
	}
}

func (StrategyCandidate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date", "strategy", "stock_code").Unique(),
		index.Fields("trade_date", "stage"),
	}
}
