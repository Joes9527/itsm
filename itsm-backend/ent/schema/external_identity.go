package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ExternalIdentity maps a provider subject to an existing tenant user. The
// external assertion itself is never persisted here.
type ExternalIdentity struct {
	ent.Schema
}

// Fields of the ExternalIdentity.
func (ExternalIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Immutable().Positive(),
		field.String("provider").Immutable().NotEmpty(),
		field.String("workspace").Immutable().NotEmpty().Sensitive(),
		field.String("subject").Immutable().NotEmpty().Sensitive(),
		field.Int("user_id").Immutable().Positive(),
		field.Bool("active").Default(true),
		field.Int("version").Default(1).Positive(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the ExternalIdentity.
func (ExternalIdentity) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("provider", "workspace", "subject").Unique(),
		index.Fields("tenant_id", "user_id"),
	}
}
