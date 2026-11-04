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

// AIAuditRun is the durable, local source of truth for one worker execution.
// AIAuditRun 是一次 worker 执行的本地、可审计事实来源。
type AIAuditRun struct{ ent.Schema }

func (AIAuditRun) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New),
		field.Int64("job_id"),
		field.String("operation_id").SchemaType(map[string]string{"postgres": "text"}).Unique(),
		field.Int64("bot_id"),
		field.UUID("conversation_id", uuid.UUID{}),
		field.String("trace_id").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.Int64("config_version").Default(0),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}).Default("running"),
		field.String("error_code").SchemaType(map[string]string{"postgres": "text"}).Default(""),
		field.Time("started_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (AIAuditRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "ai_audit_runs", Checks: map[string]string{}}}
}

func (AIAuditRun) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "conversation_id", "started_at")}
}
