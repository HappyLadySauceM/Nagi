package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
	"time"
)

// MemoryReplacement records an explicit supersede relation without foreign keys;
// deleted memory rows leave the audit relation intact.
// MemoryReplacement 记录显式替代关系且不建立外键；记忆物理删除后关系仍保留。
type MemoryReplacement struct{ ent.Schema }

func (MemoryReplacement) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).StorageKey("replacement_id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.UUID("new_memory_id", uuid.UUID{}),
		field.UUID("old_memory_id", uuid.UUID{}),
		field.String("actor").SchemaType(map[string]string{"postgres": "text"}),
		field.String("reason").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("idempotency_key").SchemaType(map[string]string{"postgres": "text"}),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (MemoryReplacement) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "memory_replacements", Checks: map[string]string{
		"memory_replacements_idempotency_key_check": "(char_length(idempotency_key) >= 1 AND char_length(idempotency_key) <= 128)",
	}}}
}
func (MemoryReplacement) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("new_memory_id", "old_memory_id").Unique().StorageKey("memory_replacements_pair"),
		index.Fields("bot_id", "idempotency_key").Unique().StorageKey("memory_replacements_idempotency"),
		index.Fields("old_memory_id").StorageKey("memory_replacements_old"),
	}
}
