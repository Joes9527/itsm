package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Problem holds the schema definition for the Problem entity.
type Problem struct {
	ent.Schema
}

// Fields of the Problem.
func (Problem) Fields() []ent.Field {
	return []ent.Field{
		field.Int("verified_version").Optional(),
		field.String("verification_digest").Optional(),
		field.Int("verified_by").Optional(),
		field.Time("verified_at").Optional(),
		field.Text("verification_note").Optional(),
		field.Text("root_cause").
			Comment("根本原因").
			Optional(),
		field.Text("workaround").
			Comment("临时解决方案").
			Optional(),
		field.Text("resolution").
			Comment("最终解决方案").
			Optional(),
		field.Text("impact").
			Comment("影响范围").
			Optional(),
		field.Int("work_item_id").
			Comment("关联的 WorkItem（tickets.id），唯一且必填；共享字段只从该 WorkItem 读取和写入"),
	}
}

// Edges of the Problem.
func (Problem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("work_item", Ticket.Type).
			Field("work_item_id").
			Unique().
			Required().
			Comment("共享字段的唯一权威 WorkItem"),
	}
}

// Indexes of the Problem.
func (Problem) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id").Unique(),
	}
}
