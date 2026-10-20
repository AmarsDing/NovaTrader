package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SeatStats 龙虎榜席位胜率时序（目录册增补 B2，2026-10-20）。
// 由 M02 market 服务在收盘后基于 lhb_seat × market_data 内部计算，每席位每交易日一行滚动窗口统计。
// 口径：基准价 = 上榜当日 1d 收盘 close0；T+N 收益 = close[t+N]/close0−1（分数，0.0323=3.23%）。
// 「没取到≠0」纪律：close0 或 close[t+N] 缺失/非正的样本该期不入分子分母（视作缺失，不当 0）；
// T+5 尚未发生（越过窗口末）的样本只计 sample，三列 T+5 保持 NULL；样本=0 的期三列全 NULL。
type SeatStats struct {
	ent.Schema
}

func (SeatStats) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "seat_stats"}}
}

func (SeatStats) Fields() []ent.Field {
	return []ent.Field{
		field.String("seat_name").MaxLen(128).NotEmpty(),
		field.Time("as_of").SchemaType(pgDate).Comment("统计基准日（滚动窗口的右端）"),
		field.Int("lookback").Default(60).Comment("窗口交易日数"),
		field.Int("sample").Default(0).Comment("窗口内该席位上榜次数（含 T+5 未发生）"),
		field.Int("sample_t5").Default(0).Comment("其中 T+5 已发生的子样本数"),
		field.Float("win_rate_t1").Optional().Nillable().SchemaType(numericRatio).Comment("T+1 收涨占比（0-1）"),
		field.Float("avg_ret_t1").Optional().Nillable().SchemaType(numericRatio).Comment("T+1 平均收益率"),
		field.Float("med_ret_t1").Optional().Nillable().SchemaType(numericRatio).Comment("T+1 中位数收益率"),
		field.Float("win_rate_t5").Optional().Nillable().SchemaType(numericRatio).Comment("T+5 收涨占比（0-1）"),
		field.Float("avg_ret_t5").Optional().Nillable().SchemaType(numericRatio).Comment("T+5 平均收益率"),
		field.Float("med_ret_t5").Optional().Nillable().SchemaType(numericRatio).Comment("T+5 中位数收益率"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (SeatStats) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("seat_name", "as_of").Unique(),
		index.Fields("as_of"),
	}
}
