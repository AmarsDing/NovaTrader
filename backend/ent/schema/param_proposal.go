package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ParamProposal 参数进化提案。M07 设计文档第 12.2、12.3 节：只出提案，人工批准后才写 strategy_config。
// version_id 是批准后生成的参数版本，prev_version_id 是批准前的生效版本，回滚回到它。
type ParamProposal struct {
	ent.Schema
}

func (ParamProposal) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "param_proposal"}}
}

func (ParamProposal) Fields() []ent.Field {
	return []ent.Field{
		field.String("strategy").MaxLen(64).NotEmpty(),
		field.JSON("base_params", map[string]float64{}),
		field.JSON("params", map[string]float64{}),
		field.Text("reason").Default(""),
		field.Int("task_id").Optional().Nillable(),
		field.JSON("base_metrics", json.RawMessage{}).Optional(),
		field.JSON("new_metrics", json.RawMessage{}).Optional(),
		field.String("status").MaxLen(16).Default("pending").Comment("pending / approved / rejected / rolled_back"),
		field.Int("version_id").Optional().Nillable(),
		field.Int("prev_version_id").Optional().Nillable(),
		field.String("decided_by").MaxLen(64).Default(""),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
		field.Time("decided_at").Optional().Nillable().SchemaType(pgTimestamptz),
	}
}

func (ParamProposal) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("strategy", "status"),
	}
}
