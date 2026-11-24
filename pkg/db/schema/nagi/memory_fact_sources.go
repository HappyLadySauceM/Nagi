package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type MemoryFactSource struct{ ent.Schema }

func (MemoryFactSource) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}),
		field.UUID("memory_id", uuid.UUID{}),
		field.Int64("message_id"),
		field.Int64("authorization_event_id"),
		field.Time("expires_at").Optional().Nillable(),
	}
}

func (MemoryFactSource) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_fact_sources"}}
}

func (MemoryFactSource) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("memory_id", "message_id").Unique().StorageKey("memory_fact_sources_memory_message_key"),
		index.Fields("message_id").StorageKey("memory_fact_sources_message"),
	}
}

func (MemoryFactSource) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("memory", Memory.Type).Ref("fact_sources").Field("memory_id").Unique().Required().Annotations(entsql.OnDelete("CASCADE")),
		edge.From("message", Message.Type).Ref("memory_fact_sources").Field("message_id").Unique().Required().Annotations(entsql.OnDelete("CASCADE")),
	}
}
