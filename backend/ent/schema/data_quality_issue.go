package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// DataQualityIssue 采集校验与多源对账结果，M01 FR-01-06、FR-01-15。
// 同一问题重复出现只累加 hits；symbol 为空表示整域问题。
// check_name：empty 空结果、stale 不是最新、outlier 异常值、timestamp 时间戳异常、
// completeness 完整率不足、reconcile 多源不一致。
type DataQualityIssue struct {
	ent.Schema
}

func (DataQualityIssue) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "data_quality_issue"}}
}

func (DataQualityIssue) Fields() []ent.Field {
	return []ent.Field{
		field.String("domain").MaxLen(32).NotEmpty(),
		field.String("check_name").MaxLen(16).NotEmpty(),
		field.String("symbol").MaxLen(16).Default(""),
		field.Time("trade_date").SchemaType(pgDate),
		field.String("severity").MaxLen(8).Default("warn").Comment("warn / error"),
		field.String("source").MaxLen(16).Default(""),
		field.String("other_source").MaxLen(16).Default("").Comment("对账时的另一来源"),
		field.Text("message").Default(""),
		field.JSON("detail", map[string]any{}).Optional(),
		field.Int("hits").Default(1),
		field.Time("first_seen").Default(time.Now).SchemaType(pgTimestamptz),
		field.Time("last_seen").Default(time.Now).SchemaType(pgTimestamptz),
	}
}

func (DataQualityIssue) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("domain", "check_name", "symbol", "trade_date").Unique(),
		index.Fields("trade_date"),
	}
}
