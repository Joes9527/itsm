package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// IntakeRequest coordinates tenant-scoped idempotency for one intake command.
type IntakeRequest struct {
	ent.Schema
}

// Fields of the IntakeRequest.
func (IntakeRequest) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Immutable().Positive(),
		field.Int("actor_id").Immutable().Positive(),
		field.String("channel").Immutable().NotEmpty(),
		field.String("operation").Immutable().NotEmpty(),
		field.String("idempotency_key").Immutable().NotEmpty().Sensitive(),
		field.String("request_digest").Immutable().NotEmpty().Sensitive(),
		field.String("digest_version").Immutable().NotEmpty(),
		field.String("status").Default("pending"),
		field.Int("work_item_id").Optional().Nillable().Positive(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("completed_at").Optional().Nillable(),
	}
}

// Indexes of the IntakeRequest.
func (IntakeRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "actor_id", "channel", "operation", "idempotency_key").Unique(),
		index.Fields("tenant_id", "work_item_id"),
	}
}
