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

// AIAuditEvent records one ordered operation of an audited worker execution.
// AIAuditEvent 记录一次受审计 worker 执行中的有序操作。
type AIAuditEvent struct{ ent.Schema }

func (AIAuditEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.UUID("run_id", uuid.UUID{}),
		field.String("operation_id").SchemaType(map[string]string{"postgres": "text"}),
		field.Int("sequence"),
		field.Int("schema_version").Default(1),
		field.String("stage").SchemaType(map[string]string{"postgres": "text"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Default("started"),
		field.String("error_code").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.JSON("input", json.RawMessage{}).Optional(),
		field.JSON("output", json.RawMessage{}).Optional(),
		field.Time("started_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (AIAuditEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("run_id", "sequence").Unique(),
		index.Fields("run_id", "operation_id").Unique(),
	}
}

func (AIAuditEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ai_audit_events", Checks: map[string]string{}}}
}
