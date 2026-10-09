package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// FinanceReport 排雷用的财务数据：业绩预告、快报、关键指标，由 M01 写入。
// 各源字段差异大，原样放 data；summary 是一句话摘要。同一报告期修正公告按 ann_date 各存一行。
type FinanceReport struct {
	ent.Schema
}

func (FinanceReport) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "finance_report"}}
}

func (FinanceReport) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("end_date").SchemaType(pgDate).Comment("报告期"),
		field.String("kind").MaxLen(16).NotEmpty().Comment("forecast / express / indicator"),
		field.Time("ann_date").SchemaType(pgDate).Comment("公告日；源头缺失时取报告期"),
		field.String("forecast_type").MaxLen(16).Default("").Comment("预增、预减、扭亏、首亏等"),
		field.Float("net_profit").Optional().Nillable().SchemaType(numericAmount).Comment("净利润，元"),
		field.Float("net_profit_yoy").Optional().Nillable().Comment("净利润同比，%"),
		field.Text("summary").Default(""),
		field.JSON("data", map[string]any{}).Optional(),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (FinanceReport) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "end_date", "kind", "ann_date").Unique(),
		index.Fields("ann_date"),
	}
}
