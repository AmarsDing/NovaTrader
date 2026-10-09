package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RiskCheck 每次事前风控校验一行（M08 FR-08-09）：订单、结论、逐条规则结果和当时的输入快照。
// 由 risk 服务异步批量写入，只追加不修改。
type RiskCheck struct {
	ent.Schema
}

func (RiskCheck) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "risk_check"}}
}

func (RiskCheck) Fields() []ent.Field {
	return []ent.Field{
		field.String("client_order_id").MaxLen(64).NotEmpty(),
		field.String("account_type").MaxLen(8).Comment("SIM / LIVE"),
		field.String("symbol").MaxLen(16),
		field.String("side").MaxLen(8).Comment("buy / sell"),
		field.String("source").MaxLen(8).Comment("auto / watch / manual"),
		field.String("operator").MaxLen(64).Optional().Default(""),
		field.Float("price").SchemaType(numericPrice),
		field.Int("volume_req").Comment("申报数量"),
		field.Int("volume_final").Default(0).Comment("放行数量，缩量时小于申报数量"),
		field.Bool("approved"),
		field.String("rule_id").MaxLen(8).Optional().Default("").Comment("拒绝时的规则编号 R00–R15"),
		field.Text("reason").Optional().Default(""),
		field.JSON("results", json.RawMessage{}).Comment("逐条规则结果"),
		field.JSON("input", json.RawMessage{}).Comment("校验时的输入快照"),
		field.Int64("latency_us").Default(0),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (RiskCheck) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("client_order_id"),
		index.Fields("created_at"),
		index.Fields("approved", "rule_id"),
	}
}
