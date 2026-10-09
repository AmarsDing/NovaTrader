package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// MacroSeries 宏观指标序列（CPI、PPI、PMI、LPR、M2），由 M01 经 AkShare 写入。
// 外围行情另存 overseas_quote。obs_date 为指标所属期，月度指标取当月 1 日。
type MacroSeries struct {
	ent.Schema
}

func (MacroSeries) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "macro_series"}}
}

func (MacroSeries) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").MaxLen(32).NotEmpty().Comment("CPI_YOY、PMI_MFG 等"),
		field.String("name").MaxLen(64).Default(""),
		field.Time("obs_date").SchemaType(pgDate),
		field.Float("value"),
		field.String("unit").MaxLen(16).Default(""),
		field.String("source").MaxLen(16).Default(""),
		field.Time("as_of").Default(time.Now).SchemaType(pgTimestamptz).Comment("采集时刻"),
	}
}

func (MacroSeries) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code", "obs_date").Unique(),
	}
}
