package schema

import (
	"encoding/json"
	"time"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// DatasetManifest freezes all inputs and exclusions for an M07 research run.
// Content addressing permits idempotent freezes; no update API is exposed.
type DatasetManifest struct{ ent.Schema }
func (DatasetManifest) Annotations() []schema.Annotation { return []schema.Annotation{entsql.Annotation{Table: "dataset_manifest"}} }
func (DatasetManifest) Fields() []ent.Field { return []ent.Field{
	field.String("manifest_id").MaxLen(80).NotEmpty().Unique().Immutable(),
	field.String("hash").MaxLen(64).NotEmpty().Unique().Immutable(),
	field.Int("schema_version").Default(1).Immutable(),
	field.Time("cutoff").SchemaType(pgTimestamptz).Immutable(),
	field.JSON("content", json.RawMessage{}).Immutable(),
	field.Time("created_at").Default(time.Now).SchemaType(pgTimestamptz).Immutable(),
} }
