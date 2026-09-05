package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"time"
)

// IncidentRuleActionReceipt exists only when its domain effects committed.
type IncidentRuleActionReceipt struct{ ent.Schema }

func (IncidentRuleActionReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").Positive().Immutable(),
		field.Int("execution_id").Positive().Immutable(),
		field.Int("action_index").NonNegative().Immutable(),
		field.Time("completed_at").Default(time.Now).Immutable(),
	}
}
func (IncidentRuleActionReceipt) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("execution", IncidentRuleExecution.Type).Ref("action_receipts").Field("execution_id").Unique().Required().Immutable(),
	}
}
func (IncidentRuleActionReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("tenant_id", "execution_id", "action_index").Unique()}
}
