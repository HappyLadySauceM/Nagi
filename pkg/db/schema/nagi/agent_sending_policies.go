package nagi

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type AgentSendingPolicy struct{ ent.Schema }

func (AgentSendingPolicy) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Int("max_messages_per_run").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("3")).Default(3),
		field.Int("max_chars_per_message").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("500")).Default(500),
		field.Int("max_total_chars_per_run").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("1500")).Default(1500),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Int("min_interval_ms").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("700")).Default(700),
	}
}
func (AgentSendingPolicy) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "agent_sending_policies", Checks: map[string]string{"agent_sending_policies_scope_type_check": "((scope_type = ANY (ARRAY['group'::text, 'private'::text])))", "agent_sending_policies_max_messages_per_run_check": "((max_messages_per_run > 0))", "agent_sending_policies_max_chars_per_message_check": "((max_chars_per_message > 0))", "agent_sending_policies_max_total_chars_per_run_check": "((max_total_chars_per_run > 0))", "agent_sending_policies_check": "((max_chars_per_message <= max_total_chars_per_run))", "agent_sending_policies_min_interval_ms_check": "((min_interval_ms >= 0))"}}}
}
func (AgentSendingPolicy) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "scope_type", "scope_id").Unique().StorageKey("agent_sending_policies_business_key")}
}
