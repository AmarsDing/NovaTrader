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

// TradeSignal 标准化交易信号。对齐 doc/NovaTrader智脑方案.md 4.1.2，M06 设计文档第 7 节追加了字段。
// 价格由规则给出，模型只提供评分。book 把模拟盘和实盘分开。confidence_score 存综合分。
// 状态只能经 strategy 服务的 Transition 修改，每次修改同时写 signal_events。
type TradeSignal struct {
	ent.Schema
}

func (TradeSignal) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "trade_signals"}}
}

func (TradeSignal) Fields() []ent.Field {
	return []ent.Field{
		field.String("book").MaxLen(16).Default("paper").Comment("paper 模拟盘，live 实盘"),
		field.Time("signal_time").
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.String("stock_code").MaxLen(16).NotEmpty(),
		field.String("stock_name").MaxLen(32).NotEmpty(),
		field.Enum("signal_type").Values("buy", "sell", "hold"),
		field.Float("entry_price").SchemaType(numericPrice),
		field.Float("stop_loss").SchemaType(numericPrice),
		field.Float("take_profit").SchemaType(numericPrice),
		field.Text("signal_reasoning").NotEmpty(),
		field.Float("confidence_score").
			SchemaType(map[string]string{dialect.Postgres: "numeric(8,4)"}).
			Comment("0 到 100"),
		field.String("status").MaxLen(16).Default("pending"),
		field.Float("executed_price").Optional().Nillable().SchemaType(numericPrice),
		field.Int("executed_volume").Optional().Nillable(),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// 以下为 M06 追加，全部有默认值或可空。
		field.String("strategy").MaxLen(64).Default("").Comment("模板名，如 first_board_weak2strong"),
		field.Int("strategy_version_id").Optional().Nillable().Comment("出信号时生效的 strategy_version"),
		field.Time("trade_date").Optional().Nillable().SchemaType(pgDate),
		field.Float("entry_low").Optional().Nillable().SchemaType(numericPrice),
		field.Float("entry_high").Optional().Nillable().SchemaType(numericPrice),
		field.Float("position_pct").Default(0).SchemaType(numericRatio).Comment("建议仓位占权益比例"),
		field.Time("valid_until").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Int("hold_days_max").Default(0).Comment("时间止损，交易日；0 为不限"),
		field.Float("rule_score").Default(0).SchemaType(map[string]string{dialect.Postgres: "numeric(8,4)"}),
		field.Float("ai_score").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "numeric(8,4)"}),
		field.JSON("dims", map[string]float64{}).Optional().Comment("四维分：technical news capital external"),
		field.Strings("evidence").Optional().Comment("M05 引用的情报或因子 ID"),
		field.Bool("ai_degraded").Default(false),
		field.Bool("high_value").Default(false),
		field.String("exit_kind").MaxLen(32).Default("").Comment("卖出原因，见 pkg/rules Exit*"),
		field.Float("atr").Optional().Nillable().SchemaType(numericPrice),
		field.String("trace_id").MaxLen(64).Default(""),
		// 买入信号 done → closed 时由 M09 回写。pnl_pct 是百分比，与 M04 案例一致。
		field.Float("exit_price").Optional().Nillable().SchemaType(numericPrice).Comment("平仓价"),
		field.Float("pnl_pct").Optional().Nillable().SchemaType(numericRatio).Comment("平仓收益率，百分比，3.2 表示 +3.2%"),
		field.Time("closed_at").Optional().Nillable().SchemaType(pgTimestamptz),
		field.Int("holding_days").Optional().Nillable().Comment("买入日之后到平仓日的交易日数"),
	}
}

func (TradeSignal) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("signal_time"),
		index.Fields("stock_code"),
		index.Fields("status"),
		index.Fields("book", "signal_time"),
		index.Fields("stock_code", "signal_type", "created_at"),
		index.Fields("trade_date", "signal_type"),
	}
}
