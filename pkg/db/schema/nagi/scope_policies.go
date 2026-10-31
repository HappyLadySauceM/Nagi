package nagi

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

type ScopePolicy struct{ ent.Schema }

func (ScopePolicy) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.Bool("enabled").Annotations(entsql.DefaultExpr("false")).Default(false),
		field.String("reply_mode").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'mention_only'::text")).Default("mention_only"),
		field.Bool("store_messages").Annotations(entsql.DefaultExpr("true")).Default(true),
		field.Bool("allow_memory_write").Annotations(entsql.DefaultExpr("false")).Default(false),
		field.Bool("allow_history_read").Annotations(entsql.DefaultExpr("false")).Default(false),
		field.Int("min_reply_interval_ms").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("10000")).Default(10000),
		field.Int("max_replies_per_hour").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.JSON("tool_permissions", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.Int64("version").SchemaType(map[string]string{"postgres": "bigint"}).Annotations(entsql.DefaultExpr("1")).Default(1),
		field.JSON("previous_reply_permission", json.RawMessage{}).Optional(),
		field.Int("max_messages_per_run").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Int("max_chars_per_message").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("4500")).Default(4500),
		field.Int("max_total_chars_per_run").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Int("message_interval_ms").SchemaType(map[string]string{"postgres": "integer"}).Annotations(entsql.DefaultExpr("700")).Default(700),
	}
}
func (ScopePolicy) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "scope_policies", Checks: map[string]string{"scope_policies_scope_type_check": "((scope_type = ANY (ARRAY['group'::text, 'private'::text])))", "scope_policies_reply_mode_check": "((reply_mode = ANY (ARRAY['mention_only'::text, 'intent_based'::text, 'always_reply'::text, 'observe_only'::text])))", "scope_policies_message_interval_ms_check": "(((message_interval_ms >= 0) AND (message_interval_ms <= 60000)))"}}}
}
func (ScopePolicy) Indexes() []ent.Index {
	return []ent.Index{index.Fields("bot_id", "scope_type", "scope_id").Unique().StorageKey("scope_policies_business_key")}
}

func (ScopePolicy) Edges() []ent.Edge {
	return []ent.Edge{edge.From("bot_id_ref", BotAccount.Type).Ref("scope_policies_bot_id").Field("bot_id").Unique().Annotations(entsql.OnDelete("NO ACTION")).Required()}
}
