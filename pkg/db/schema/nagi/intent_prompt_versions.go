// Intent prompt versions keep locally reviewed snapshots and activation state.
// 意图 Prompt 版本保存本地审查快照与发布状态。
package nagi

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IntentPromptVersion struct{ ent.Schema }

func (IntentPromptVersion) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("prompt_id").SchemaType(map[string]string{"postgres": "text"}),
		field.String("prompt_kind").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'intent_participation'::text")).Default("intent_participation"),
		field.String("name").SchemaType(map[string]string{"postgres": "text"}),
		field.String("provider").SchemaType(map[string]string{"postgres": "text"}),
		field.Int64("bot_id").SchemaType(map[string]string{"postgres": "bigint"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.String("scope_type").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'account'::text")).Default("account"),
		field.Int64("scope_id").SchemaType(map[string]string{"postgres": "bigint"}).Annotations(entsql.DefaultExpr("0")).Default(0),
		field.Int("version").SchemaType(map[string]string{"postgres": "integer"}),
		field.String("status").SchemaType(map[string]string{"postgres": "text"}),
		field.String("source").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("'local'::text")).Default("local"),
		field.String("source_ref").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("hash").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.String("note").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
		field.JSON("content", json.RawMessage{}).Annotations(entsql.DefaultExpr("'{}'::jsonb")).Default(json.RawMessage("{}")),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.Time("published_at").Optional().Nillable(),
		field.Time("archived_at").Optional().Nillable(),
	}
}

func (IntentPromptVersion) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "intent_prompt_versions", Checks: map[string]string{
		"intent_prompt_versions_status_check":     "((status = ANY (ARRAY['draft'::text, 'published'::text, 'archived'::text])))",
		"intent_prompt_versions_provider_check":   "((provider = ANY (ARRAY['jev'::text, 'openai'::text])))",
		"intent_prompt_versions_kind_check":       "((prompt_kind = ANY (ARRAY['chat_reply'::text, 'intent_participation'::text, 'memory_extraction'::text])))",
		"intent_prompt_versions_scope_type_check": "((scope_type = ANY (ARRAY['account'::text, 'group'::text, 'private'::text])))",
		"intent_prompt_versions_scope_id_check":   "((scope_id = 0))",
		"intent_prompt_versions_version_check":    "((version > 0))",
	}}}
}

func (IntentPromptVersion) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("prompt_id", "bot_id", "scope_type", "scope_id", "provider", "version").Unique().StorageKey("intent_prompt_versions_layer_version_key"),
		index.Fields("prompt_id", "status").StorageKey("intent_prompt_versions_prompt_status"),
	}
}
