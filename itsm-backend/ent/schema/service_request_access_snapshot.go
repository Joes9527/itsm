package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Immutable requested access terms approved through the existing BPMN process.
type ServiceRequestAccessSnapshot struct{ ent.Schema }

func (ServiceRequestAccessSnapshot) Fields() []ent.Field {
	return []ent.Field{
		field.Int("work_item_id").Positive().Unique().Immutable(),
		field.Int("policy_id").Positive().Immutable(),
		field.Int("policy_version").Positive().Immutable(),
		field.Enum("provider").Values("graph").Immutable(),
		field.String("external_system").NotEmpty().Immutable(),
		field.String("subject_id").NotEmpty().Immutable().Sensitive(),
		field.String("group_id").NotEmpty().Immutable(),
		field.String("duration_key").NotEmpty().Immutable(),
		field.Int64("duration_seconds").Positive().Immutable(),
	}
}
func (ServiceRequestAccessSnapshot) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("work_item", Ticket.Type).Field("work_item_id").Required().Unique().Immutable(),
		edge.To("policy", CatalogAccessPolicy.Type).Field("policy_id").Required().Unique().Immutable(),
	}
}

func (ServiceRequestAccessSnapshot) Indexes() []ent.Index {
	return []ent.Index{index.Fields("work_item_id").Unique()}
}
