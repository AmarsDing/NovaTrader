package schema

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// PaperRunSample is append-only real server-clock evidence, never a manually incremented trading-day counter.
// Daily qualification is computed from all required session slots and a closing reconciliation proof.
type PaperRunSample struct{ ent.Schema }
func(PaperRunSample)Annotations()[]schema.Annotation{return []schema.Annotation{entsql.Annotation{Table:"paper_run_sample"}}}
func(PaperRunSample)Fields()[]ent.Field{return []ent.Field{
	field.String("book").MaxLen(16).NotEmpty().Immutable(),
	field.String("account_id").MaxLen(64).NotEmpty().Immutable(),
	field.String("trading_day").MaxLen(10).NotEmpty().Immutable(),
	field.Time("slot_start").SchemaType(pgTimestamptz).Immutable(),
	field.String("instance_id").MaxLen(64).NotEmpty().Immutable(),
	field.Time("observed_at").SchemaType(pgTimestamptz).Immutable(),
	field.JSON("observation",json.RawMessage{}).Immutable(),
	field.String("evidence_hash").MaxLen(64).NotEmpty().Immutable(),
}}
func(PaperRunSample)Indexes()[]ent.Index{return []ent.Index{index.Fields("book","account_id","slot_start","instance_id").Unique(),index.Fields("book","account_id","trading_day")}}
