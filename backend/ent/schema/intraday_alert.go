package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IntradayAlert 盘中异动流水（M02 §6 / P0-4 异动流）。market 引擎每轮快照检出的异动落一张表，
// 供复盘回看当日异动序列与前端异动流面板初始拉取；实时推送走 market.alert（WS alert/move）。
// 口径对齐 vibe-astock watch.ts：急拉 surge（SurgeMinutes 分钟涨幅 ≥ SurgePct%，默认 3 分钟 1.5%）、
// 封板 limit_up、开板 break；同股票同类 5 分钟去重；竞价落定（09:25 截首张）清零去重状态。
// 「没取到 ≠ 0」：pct/price 为 NULLable，快照里算不出涨幅的异动留 NULL。
type IntradayAlert struct {
	ent.Schema
}

func (IntradayAlert) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intraday_alerts"}}
}

func (IntradayAlert) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate).Comment("交易日，落盘日"),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.String("kind").MaxLen(16).NotEmpty().Comment("surge / limit_up / break"),
		field.Float("pct").Optional().Nillable().Comment("急拉窗口涨幅 %，封/开板该字段为当日涨跌幅"),
		field.Float("price").Optional().Nillable().Comment("触发时现价"),
		field.String("name").MaxLen(32).Default(""),
		field.Time("fired_at").SchemaType(pgTimestamptz).Comment("触发时刻（快照时间）"),
	}
}

func (IntradayAlert) Indexes() []ent.Index {
	return []ent.Index{
		// 幂等键：同一触发时刻的同股票同类异动只落一行（重放/重启不重复）。
		index.Fields("symbol", "kind", "fired_at").Unique(),
		index.Fields("trade_date", "fired_at"),
	}
}
