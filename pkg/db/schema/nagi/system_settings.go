package nagi

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"time"
)

type SystemSetting struct{ ent.Schema }

func (SystemSetting) Fields() []ent.Field {
	return []ent.Field{
		field.Int("id").SchemaType(map[string]string{"postgres": "smallint"}),
		field.JSON("payload", json.RawMessage{}),
		field.Int64("version").SchemaType(map[string]string{"postgres": "bigint"}).Annotations(entsql.DefaultExpr("1")).Default(1),
		field.Time("updated_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
		field.String("updated_by").SchemaType(map[string]string{"postgres": "text"}).Annotations(entsql.DefaultExpr("''::text")).Default(""),
	}
}
func (SystemSetting) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "system_settings", Checks: map[string]string{"system_settings_id_check": "((id = 1))", "system_settings_version_check": "((version > 0))"}}}
}
