package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// KafTaskCompletionReceipt records callback progress for one KAF action ledger.
type KafTaskCompletionReceipt struct {
	ent.Schema
}

// Fields of the KafTaskCompletionReceipt.
func (KafTaskCompletionReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.Int("ledger_id").Immutable().Positive(),
		field.Int("tenant_id").Immutable().Positive(),
		field.String("task_id").Immutable().NotEmpty(),
		field.String("status").Default("callback_pending"),
		field.String("error_code").Optional(),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the KafTaskCompletionReceipt.
func (KafTaskCompletionReceipt) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ledger_id").Unique(),
		index.Fields("tenant_id", "task_id"),
	}
}
