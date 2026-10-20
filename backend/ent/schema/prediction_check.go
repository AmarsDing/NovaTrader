// Package schema — prediction_check：明日验证对账。信号日给出的候选名单，在下一交易日
// 收盘后回填实际表现（涨幅/是否涨停/命中），形成可回看的战绩账目。
// 纪律：涨跌幅等标量用 NULL 表示没取到，禁止落 0；missing 记录没取到的数据块。
package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PredictionCheck 明日验证：信号日名单 × 对账日表现。对账对象来源目前有
// briefing（晨报 watchlist），之后可扩展 ai 日报候选等。
type PredictionCheck struct {
	ent.Schema
}

func (PredictionCheck) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "prediction_check"}}
}

func (PredictionCheck) Fields() []ent.Field {
	return []ent.Field{
		field.Time("signal_date").SchemaType(pgDate).Comment("信号日：名单给出的交易日"),
		field.Time("check_date").SchemaType(pgDate).Comment("对账日：回填表现的下一交易日"),
		field.String("source").MaxLen(16).NotEmpty().Comment("名单来源：briefing 晨报 watchlist"),
		field.JSON("candidates", []interface{}{}).Optional().Comment("[{symbol,name,reason,text}]"),
		field.JSON("results", []interface{}{}).Optional().Comment("[{symbol,pct,relimit,hit}]"),
		field.Int("candidate_count").Optional().Nillable(),
		field.Int("hit_count").Optional().Nillable().Comment("次日收涨只数"),
		field.Int("relimit_count").Optional().Nillable().Comment("次日再涨停只数"),
		field.Float("hit_rate").Optional().Nillable().Comment("命中率=收涨只数/候选只数，%"),
		field.Float("avg_pct").Optional().Nillable().Comment("次日平均涨幅，%"),
		field.Float("max_pct").Optional().Nillable().Comment("次日最高涨幅，%"),
		field.Bool("partial").Default(false).Comment("有任何块没取到即为 true"),
		field.Strings("missing").Optional().Comment("没取到的数据块清单"),
		field.Time("computed_at").SchemaType(pgTimestamptz).Default(time.Now),
	}
}

func (PredictionCheck) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("check_date", "source").Unique(),
		index.Fields("signal_date"),
	}
}
