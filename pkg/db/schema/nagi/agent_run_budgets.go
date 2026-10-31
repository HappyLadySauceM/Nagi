package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type AgentRunBudget struct{ ent.Schema }

func (AgentRunBudget) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"), field.Int64("job_id").Unique(),
		field.Int("reserved_messages").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Int("reserved_chars").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (AgentRunBudget) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "agent_run_budgets", Checks: map[string]string{}}}
}

func (AgentRunBudget) Edges() []ent.Edge {
	return []ent.Edge{edge.From("job_id_ref", MessageJob.Type).Ref("agent_run_budgets_job_id").Field("job_id").Unique().Annotations(entsql.OnDelete("CASCADE")).Required()}
}

func (AgentRunBudget) Indexes() []ent.Index {
	return []ent.Index{index.Fields("job_id").Unique().StorageKey("agent_run_budgets_job_key")}
}
