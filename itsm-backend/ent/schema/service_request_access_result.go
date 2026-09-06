package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Verified professional result. The KAF action ledger remains execution owner.
type ServiceRequestAccessResult struct{ ent.Schema }

func (ServiceRequestAccessResult) Fields() []ent.Field {
	return []ent.Field{
		field.Int("work_item_id").Positive().Unique().Immutable(),
		field.Int("process_task_id").Positive().Immutable(),
		field.Enum("outcome").Values("granted", "already_present").Immutable(),
		field.Enum("provider").Values("graph").Immutable(),
		field.String("subject_id").NotEmpty().Immutable().Sensitive(),
		field.String("group_id").NotEmpty().Immutable(),
		field.Enum("baseline").Values("not_member", "member").Immutable(),
		field.Time("verified_at").Immutable(),
		field.Time("expires_at").Optional().Nillable().Immutable(),
		field.String("evidence_ref").NotEmpty().Immutable(),
	}
}
func (ServiceRequestAccessResult) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("work_item", Ticket.Type).Field("work_item_id").Required().Unique().Immutable(),
		edge.To("process_task", ProcessTask.Type).Field("process_task_id").Required().Unique().Immutable(),
	}
}

func (ServiceRequestAccessResult) Indexes() []ent.Index {
	return []ent.Index{index.Fields("work_item_id").Unique()}
}
