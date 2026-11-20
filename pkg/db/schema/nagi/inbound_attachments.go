package nagi

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// InboundAttachment records the object reference and the work needed to
// converge storage after the message transaction commits.
type InboundAttachment struct{ ent.Schema }

func (InboundAttachment) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.Int64("message_id"),
		field.Int("segment_index").NonNegative(),
		field.String("kind"),
		field.JSON("source", json.RawMessage{}).Optional(),
		field.String("status").Default("pending").Annotations(entsql.DefaultExpr("'pending'::text")),
		field.String("failure_code").Default("").Annotations(entsql.DefaultExpr("''::text")),
		field.Int("attempts").Default(0).Annotations(entsql.DefaultExpr("0")),
		field.String("filename").Default("").Annotations(entsql.DefaultExpr("''::text")),
		field.String("content_type").Default("").Annotations(entsql.DefaultExpr("''::text")),
		field.Int64("size_bytes").Default(0).Annotations(entsql.DefaultExpr("0")),
		field.String("sha256").Default("").Annotations(entsql.DefaultExpr("''::text")),
		field.String("bucket").Default("").Annotations(entsql.DefaultExpr("''::text")),
		field.String("object_key").Default("").Annotations(entsql.DefaultExpr("''::text")),
		field.UUID("lease_token", uuid.UUID{}).Optional().Nillable(),
		field.Time("lease_expires_at").Optional().Nillable(),
		field.Time("next_run_at").Default(time.Now).Annotations(entsql.DefaultExpr("now()")),
		field.Time("expires_at"),
	}
}

func (InboundAttachment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("message_id", "segment_index").Unique(),
		index.Fields("status", "next_run_at", "id"),
		index.Fields("expires_at", "id"),
	}
}

func (InboundAttachment) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "inbound_attachments"}}
}
