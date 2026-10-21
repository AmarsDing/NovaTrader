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

// NotifyMessage 是一条通知的投递记录（M10）。
// 上游事件按 event_id 只记一次；合并窗口内的重复并入先到的那条。
type NotifyMessage struct {
	ent.Schema
}

func (NotifyMessage) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "notify_message"}}
}

func (NotifyMessage) Fields() []ent.Field {
	return []ent.Field{
		field.String("event_id").MaxLen(64).NotEmpty().Unique(),
		field.String("trace_id").MaxLen(64).Optional().Default(""),
		field.String("source").MaxLen(64).Optional().Default(""),
		field.String("subject").MaxLen(128).Optional().Default(""),
		field.String("category").MaxLen(32).NotEmpty(),
		field.String("priority").MaxLen(16).NotEmpty(),
		field.String("title").MaxLen(256).Optional().Default(""),
		field.Text("body").Optional().Default(""),
		field.JSON("fields", map[string]string{}).Optional(),
		field.String("dedup_key").MaxLen(256).Optional().Default(""),
		field.Int("merge_count").Default(0),
		field.Int("merged_into").Optional().Nillable(),
		field.String("status").MaxLen(16).Default("pending"),
		field.Text("rendered").Optional().Default(""),
		field.String("desktop_status").MaxLen(16).Optional().Default(""),
		field.Int("desktop_attempts").Default(0),
		field.Text("desktop_error").Optional().Default(""),
		field.String("feishu_status").MaxLen(16).Optional().Default(""),
		field.Int("feishu_attempts").Default(0),
		field.Text("feishu_error").Optional().Default(""),
		field.Time("created_at").Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("sent_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		// read_at 前端逐条已读的回写时刻（M10，G3 前端切片先行只落 localStorage）。
		// 首次已读为准：回写只填 NULL 行，重复点击不覆盖；未读保持 NULL，不用零值冒充。
		field.Time("read_at").Optional().Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (NotifyMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("dedup_key", "created_at"),
		index.Fields("status", "created_at"),
	}
}
