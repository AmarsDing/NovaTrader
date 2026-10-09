package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Sector 行业与概念板块，由 M01 从通达信板块文件或东财同步。
type Sector struct {
	ent.Schema
}

func (Sector) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sector"}}
}

func (Sector) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").MaxLen(32).NotEmpty(),
		field.String("name").MaxLen(64).NotEmpty(),
		field.String("kind").MaxLen(16).NotEmpty().Comment("industry / concept"),
		field.String("source").MaxLen(16).Default(""),
	}
}

func (Sector) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("code").Unique(),
		index.Fields("kind"),
	}
}

// SectorMember 板块成分。关系表代替图数据库；out_date 为空表示仍是成分，回测按日期取当时成分。
type SectorMember struct {
	ent.Schema
}

func (SectorMember) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sector_member"}}
}

func (SectorMember) Fields() []ent.Field {
	return []ent.Field{
		field.String("sector_code").MaxLen(32).NotEmpty(),
		field.String("symbol").MaxLen(16).NotEmpty(),
		field.Time("in_date").SchemaType(pgDate),
		field.Time("out_date").Optional().Nillable().SchemaType(pgDate),
	}
}

func (SectorMember) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("sector_code", "symbol", "in_date").Unique(),
		index.Fields("symbol"),
	}
}

// SectorHeat 板块热度截面，对应 M02 FR-02-05、FR-02-07。as_of 08:50 那行只有外围冲击。
type SectorHeat struct {
	ent.Schema
}

func (SectorHeat) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "sector_heat"}}
}

func (SectorHeat) Fields() []ent.Field {
	return []ent.Field{
		field.String("sector_code").MaxLen(32).NotEmpty(),
		field.Time("trade_date").SchemaType(pgDate),
		field.Time("as_of").SchemaType(pgTimestamptz),
		field.Int("rank").Default(0),
		field.Float("heat").Default(0),
		field.Float("avg_pct").Default(0),
		field.Int("up_limit_count").Default(0),
		field.Float("advance_ratio").Default(0),
		field.Float("amount").Default(0).SchemaType(numericAmount),
		field.Int("member_count").Default(0),
		field.String("leader_symbol").MaxLen(16).Default(""),
		field.String("leader_reason").MaxLen(64).Default(""),
		field.Float("overseas_impulse").Default(0).Comment("外围映射冲击，%"),
	}
}

func (SectorHeat) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("sector_code", "trade_date", "as_of").Unique(),
		index.Fields("trade_date", "as_of"),
	}
}
