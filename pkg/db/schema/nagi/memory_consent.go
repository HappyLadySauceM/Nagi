package nagi

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// MemoryConsentProposal binds one approved version to a fixed disclosure prompt.
// MemoryConsentProposal 将已审批的事实版本绑定到固定披露提示。
type MemoryConsentProposal struct{ ent.Schema }

func (MemoryConsentProposal) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}),
		field.Int64("bot_id"), field.UUID("memory_id", uuid.UUID{}), field.Int64("fact_version"),
		field.Int64("subject_id"), field.UUID("source_conversation_id", uuid.UUID{}), field.Int64("source_group_id"),
		field.Int64("source_message_id").Optional().Nillable(),
		field.JSON("target_groups", json.RawMessage{}),
		field.String("display_digest").SchemaType(map[string]string{"postgres": "text"}),
		field.String("idempotency_key").SchemaType(map[string]string{"postgres": "text"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Default("pending").Annotations(entsql.DefaultExpr("'pending'::text")),
		field.Int64("state_version").Default(1).Annotations(entsql.DefaultExpr("1")),
		field.Time("created_at").Default(time.Now).Annotations(entsql.DefaultExpr("now()")), field.Time("expires_at"),
	}
}
func (MemoryConsentProposal) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_consent_proposals", Checks: map[string]string{
		"memory_consent_proposal_identity_check": "(bot_id > 0 AND subject_id > 0 AND source_group_id > 0 AND fact_version > 0 AND state_version > 0)",
		"memory_consent_proposal_targets_check":  "(jsonb_typeof(target_groups) = 'array' AND jsonb_array_length(target_groups) > 0)",
		"memory_consent_proposal_status_check":   "(status IN ('pending', 'confirmed', 'cancelled', 'invalidated'))",
	}}}
}
func (MemoryConsentProposal) Indexes() []ent.Index {
	return []ent.Index{index.Fields("id", "bot_id", "memory_id", "fact_version", "subject_id").Unique().StorageKey("memory_consent_proposal_bound_identity"), index.Fields("bot_id", "idempotency_key").Unique().StorageKey("memory_consent_proposal_idempotency"), index.Fields("bot_id", "memory_id", "fact_version").StorageKey("memory_consent_proposal_fact")}
}
func (MemoryConsentProposal) Edges() []ent.Edge {
	return []ent.Edge{edge.To("source_message", Message.Type).Field("source_message_id").Unique().Annotations(entsql.OnDelete("SET NULL")), edge.To("delivery_job", MessageJob.Type).Annotations(entsql.OnDelete("NO ACTION")), edge.To("grants", MemoryConsentGrant.Type), edge.To("events", MemoryConsentEvent.Type)}
}

// MemoryConsentGrant stores the current permission for one destination.
// MemoryConsentGrant 保存单一目标群的当前许可。
type MemoryConsentGrant struct{ ent.Schema }

func (MemoryConsentGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.Int64("bot_id"), field.UUID("memory_id", uuid.UUID{}), field.Int64("fact_version"), field.Int64("subject_id"), field.Int64("target_group_id"),
		field.UUID("proposal_id", uuid.UUID{}), field.Int64("confirmation_message_id").Optional().Nillable(),
		field.Time("confirmed_at"), field.Int64("state_version").Default(1).Annotations(entsql.DefaultExpr("1")),
		field.Time("revoked_at").Optional().Nillable(), field.String("revocation_source").SchemaType(map[string]string{"postgres": "text"}).Default("").Annotations(entsql.DefaultExpr("''::text")),
	}
}
func (MemoryConsentGrant) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_consent_grants", Checks: map[string]string{
		"memory_consent_grant_identity_check": "(bot_id > 0 AND subject_id > 0 AND target_group_id > 0 AND fact_version > 0 AND state_version > 0)",
	}}}
}
func (MemoryConsentGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "memory_id", "fact_version", "subject_id", "target_group_id").Unique().StorageKey("memory_consent_grant_current"), index.Fields("bot_id", "target_group_id", "subject_id").StorageKey("memory_consent_grant_target")}
}
func (MemoryConsentGrant) Edges() []ent.Edge {
	return []ent.Edge{edge.From("proposal", MemoryConsentProposal.Type).Ref("grants").Field("proposal_id").Unique().Required().Annotations(entsql.OnDelete("NO ACTION")), edge.To("confirmation_message", Message.Type).Field("confirmation_message_id").Unique().Annotations(entsql.OnDelete("SET NULL"))}
}

// MemoryConsentEvent preserves the disposition of a direct inbound action.
// MemoryConsentEvent 记录本人直接入站操作的处理结果。
type MemoryConsentEvent struct{ ent.Schema }

func (MemoryConsentEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.Int64("bot_id"), field.UUID("conversation_id", uuid.UUID{}), field.Int64("platform_message_id"), field.UUID("proposal_id", uuid.UUID{}),
		field.String("outcome").SchemaType(map[string]string{"postgres": "text"}), field.Time("created_at").Default(time.Now).Annotations(entsql.DefaultExpr("now()")),
	}
}
func (MemoryConsentEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_consent_events"}}
}
func (MemoryConsentEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "conversation_id", "platform_message_id").Unique().StorageKey("memory_consent_event_processed")}
}
func (MemoryConsentEvent) Edges() []ent.Edge {
	return []ent.Edge{edge.From("proposal", MemoryConsentProposal.Type).Ref("events").Field("proposal_id").Unique().Required().Annotations(entsql.OnDelete("NO ACTION"))}
}
