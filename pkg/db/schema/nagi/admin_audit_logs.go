package nagi

import (
	"encoding/json"
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"time"
)

type AdminAuditLog struct{ ent.Schema }

func (AdminAuditLog) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id").SchemaType(map[string]string{"postgres": "bigint"}),
		field.String("actor").SchemaType(map[string]string{"postgres": "text"}),
		field.String("action").SchemaType(map[string]string{"postgres": "text"}),
		field.String("resource").SchemaType(map[string]string{"postgres": "text"}),
		field.JSON("before", json.RawMessage{}).Optional(),
		field.JSON("after", json.RawMessage{}).Optional(),
		field.Time("created_at").Annotations(entsql.DefaultExpr("now()")).Default(time.Now),
	}
}
func (AdminAuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "admin_audit_logs", Checks: map[string]string{}}}
}
