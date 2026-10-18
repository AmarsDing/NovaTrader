package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// DatasetRecord is an append-only point-in-time revision captured by M01.
// Publication availability is separate from collection time; missing publication time stays unknown.
type DatasetRecord struct{ ent.Schema }
func (DatasetRecord) Annotations() []schema.Annotation { return []schema.Annotation{entsql.Annotation{Table: "dataset_record"}} }
func (DatasetRecord) Fields() []ent.Field { return []ent.Field{
	field.String("domain").MaxLen(32).NotEmpty().Immutable(),
	field.String("business_key").MaxLen(256).NotEmpty().Immutable(),
	field.String("source").MaxLen(64).NotEmpty().Immutable(),
	field.Time("event_time").SchemaType(pgTimestamptz).Immutable(),
	field.Time("available_at").Optional().Nillable().SchemaType(pgTimestamptz).Immutable(),
	field.String("availability").MaxLen(16).Default("unknown").Immutable(),
	field.String("availability_evidence").MaxLen(256).Default("").Immutable(),
	field.Time("observed_at").SchemaType(pgTimestamptz).Immutable(),
	field.String("raw_hash").MaxLen(64).NotEmpty().Immutable(),
	field.String("revision_hash").MaxLen(64).NotEmpty().Unique().Immutable(),
	field.JSON("payload", json.RawMessage{}).Immutable(),
} }
func (DatasetRecord) Indexes() []ent.Index { return []ent.Index{index.Fields("domain", "business_key", "event_time"), index.Fields("observed_at"), index.Fields("available_at")} }
