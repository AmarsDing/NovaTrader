package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// EtfShare ETF 份额快照，由 M01（datahub etf_share 域）从交易所官方端点写入：
// SSE query.sse.com.cn commonQuery（STAT_DATE 按日归档，可历史回补）与
// SZSE fund.szse.cn ShowReport（当日快照，T 晚预估 / T+1 早确认双 vintage，历史不可查）各管本所。
// shares 单位万份，与源头口径一致（equity_unlock「单位随源」先例）；shares 恒正——源头未给该券
// 就没有这一行（无行≠0），解析不到/非有限/非正的行采集侧整行跳过并聚合 Issue 留痕。
// (symbol, trade_date, as_of) 多 vintage 并存（PIT）：SZSE 预估与确认同 trade_date 各自成行，
// 消费侧取 max(as_of)≤查询日；申赎变动 delta 在 M02 读侧推导——基期（前一个有行日）缺披露时
// delta 保持 NULL，前后两日真相等才落 0（真 0 合法）；SZSE 漏采的交易日永久无行，缺口诚实呈现。
type EtfShare struct {
	ent.Schema
}

func (EtfShare) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "etf_share"}}
}

func (EtfShare) Fields() []ent.Field {
	return []ent.Field{
		field.String("symbol").MaxLen(16).NotEmpty().Comment("内部形 510050.SH / 159915.SZ"),
		field.Time("trade_date").SchemaType(pgDate).Comment("份额所属交易日：SSE=STAT_DATE；SZSE=dqgm 列头内嵌日"),
		field.Time("as_of").SchemaType(pgDate).Comment("观测 vintage 日（采集日）；同 trade_date 的预估/确认各自成行"),
		field.Float("shares").SchemaType(numericShares).Comment("份额（万份，与源一致）；恒正，写入侧已行级校验"),
		field.String("etf_type").MaxLen(16).Optional().Nillable().Comment("SSE 口径（单市/跨市/跨境/其他）；两市分列不混，SZSE 行留空"),
		field.String("fund_category").MaxLen(32).Optional().Nillable().Comment("SZSE tzlb 投资类别；SSE 行留空"),
		field.String("name").MaxLen(64).Default("").Comment("快照冗余（SEC_NAME/jjmc）"),
		field.String("manager").MaxLen(64).Optional().Nillable().Comment("SZSE glrmc 管理人；SSE 源无→NULL"),
		field.Time("listing_date").Optional().Nillable().SchemaType(pgDate).Comment("SZSE ssrq 上市日期；SSE 源无→NULL"),
		field.String("source").MaxLen(16).Default("").Comment("sse / szse"),
		field.Time("updated_at"),
	}
}

func (EtfShare) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("symbol", "trade_date", "as_of").Unique(),
		index.Fields("trade_date"),
		index.Fields("symbol", "trade_date"),
	}
}
