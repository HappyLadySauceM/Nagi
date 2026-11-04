package nagi

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// AIAuditArtifact is metadata for an authorized media artifact. Bytes and
// remote object lifecycle are intentionally handled by the media worker.
// AIAuditArtifact 是已授权媒体工件的元数据；字节与远程对象生命周期由媒体 worker 负责。
type AIAuditArtifact struct{ ent.Schema }

func (AIAuditArtifact) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("run_id", uuid.UUID{}),
		field.UUID("event_id", uuid.UUID{}).Optional().Nillable(),
		field.String("kind").SchemaType(map[string]string{"postgres": "text"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Default("pending"),
		field.String("sha256").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.String("content_type").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.Int64("size_bytes").Default(0),
		field.String("object_key").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.String("failure_code").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.Time("expires_at").Optional().Nillable(),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}

func (AIAuditArtifact) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ai_audit_artifacts", Checks: map[string]string{}}}
}

func (AIAuditArtifact) Indexes() []ent.Index {
	return []ent.Index{index.Fields("run_id", "created_at")}
}
