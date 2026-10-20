package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// EmotionDayFrozen 短线收盘取数：每个交易日收盘后把定稿的情绪读数冻结存档。
// 东财涨停/炸板/跌停池只保留约一个月；本表每个交易日固定落一份，之后长期可查。
// 对齐外部项目 vibe-astock 的「短线收盘取数」（15:35 定稿，过了时点补不回来）。
// 标量字段没取到用 NULL，不落 0；payload 存完整结构（天梯/首板/题材/昨日梯队）。
type EmotionDayFrozen struct {
	ent.Schema
}

func (EmotionDayFrozen) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "emotion_day_frozen"}}
}

func (EmotionDayFrozen) Fields() []ent.Field {
	return []ent.Field{
		field.Time("trade_date").SchemaType(pgDate),
		field.Int("up_count").Optional().Nillable().Comment("涨停只数，NULL=没取到"),
		field.Int("down_count").Optional().Nillable().Comment("跌停只数"),
		field.Int("broken_count").Optional().Nillable().Comment("炸板只数"),
		field.Float("seal_rate").Optional().Nillable().Comment("封板率=涨停/(涨停+炸板)，%"),
		field.Int("max_height").Optional().Nillable().Comment("最高连板"),
		field.Float("promo_12").Optional().Nillable().Comment("1进2 晋级率 %(昨一板今日二板只数/昨一板只数)"),
		field.Float("promo_23").Optional().Nillable().Comment("2进3 晋级率 %"),
		field.Float("promo_high").Optional().Nillable().Comment("高位晋级率 %(昨>=3板今日更高板只数/昨>=3板只数)"),
		field.Float("tier_avg").Optional().Nillable().Comment("昨日梯队今日平均涨幅 %"),
		field.Float("tier_median").Optional().Nillable().Comment("昨日梯队今日涨幅中位数 %"),
		field.Float("tier_red_rate").Optional().Nillable().Comment("昨日梯队翻红率 %"),
		field.Float("tier_relimit").Optional().Nillable().Comment("昨日梯队再涨停率 %"),
		field.JSON("ladder", map[string]any{}).Optional().Comment("连板天梯 {height:[{symbol,name,reason,open_count,first_seal_at}]}"),
		field.JSON("first_board", []any{}).Optional().Comment("首板名单 [{symbol,name,reason,seal_amount,first_seal_at}]"),
		field.JSON("theme_tree", []any{}).Optional().Comment("题材归并 top [{reason,count,max_height,symbols}]"),
		field.JSON("prev_tier", []any{}).Optional().Comment("昨日梯队今日表现 [{symbol,name,pct,relimit}]"),
		field.JSON("seal_quality", map[string]any{}).Optional().Comment("封板质量 {never_broken,resealed,early_seal}"),
		field.Ints("structure_gaps").Optional().Comment("梯队断层板位，如 [3] 表示有4板起无3板"),
		field.Bool("partial").Default(false).Comment("有任何块没取到即为 true"),
		field.Strings("missing").Optional().Comment("没取到的数据块清单，写明原因块名"),
		field.String("source").MaxLen(32).Default("").Comment("读数来源，如 limit_pool"),
		field.Time("captured_at").Default(time.Now).SchemaType(pgTimestamptz).Comment("冻结时刻"),
	}
}

func (EmotionDayFrozen) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("trade_date").Unique(),
	}
}
