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

// BacktestTask 回测任务。M07 设计文档第 10、15 节：单次、参数搜索、滚动前推、进化四类，异步执行。
// config 是完整任务配置；params_hash、engine_version、data_hash 用于判断重跑能否复现。
type BacktestTask struct {
	ent.Schema
}

func (BacktestTask) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "backtest_task"}}
}

func (BacktestTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("kind").MaxLen(16).Default("single").Comment("single / optimize / walkforward / evolve"),
		field.String("name").MaxLen(128).Default(""),
		field.String("strategy").MaxLen(64).NotEmpty(),
		field.String("freq").MaxLen(4).Default("1m"),
		field.Time("start_date").SchemaType(pgDate),
		field.Time("end_date").SchemaType(pgDate),
		field.JSON("config", json.RawMessage{}),
		field.String("status").MaxLen(16).Default("queued").Comment("queued / running / succeeded / failed / cancelled"),
		field.Float("progress").Default(0).SchemaType(numericRatio),
		field.String("message").MaxLen(256).Default(""),
		field.Text("error").Default(""),
		field.String("params_hash").MaxLen(64).Default(""),
		field.String("engine_version").MaxLen(16).Default(""),
		field.String("build_version").MaxLen(64).Default(""),
		field.Int64("seed").Default(1),
		field.String("data_hash").MaxLen(64).Default(""),
		field.Time("data_cutoff").Optional().Nillable().SchemaType(pgDate),
		field.Int("rerun_of").Optional().Nillable(),
		field.String("created_by").MaxLen(64).Default("local"),
		field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz),
		field.Time("started_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Time("finished_at").Optional().Nillable().SchemaType(pgTimestamptz),
	}
}

func (BacktestTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "id"),
		index.Fields("strategy", "kind", "status"),
	}
}
