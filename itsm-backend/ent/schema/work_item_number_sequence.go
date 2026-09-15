package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// WorkItemNumberSequence holds the tenant/month sequence used to allocate
// authoritative WorkItem numbers.
type WorkItemNumberSequence struct {
	ent.Schema
}

// Fields of the WorkItemNumberSequence.
func (WorkItemNumberSequence) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Immutable().Positive(),
		field.String("period").Immutable().MinLen(6).MaxLen(6),
		field.Int64("last_value").Default(0).NonNegative(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the WorkItemNumberSequence.
func (WorkItemNumberSequence) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "period").Unique()}
}
