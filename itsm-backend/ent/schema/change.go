package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Change holds the schema definition for the Change entity.
type Change struct {
	ent.Schema
}

// Fields of the Change.
func (Change) Fields() []ent.Field {
	return []ent.Field{
		field.Text("justification").
			Comment("变更理由").
			Optional(),
		field.String("type").
			Comment("变更类型").
			Default("normal"),
		field.String("impact_scope").
			Comment("影响范围").
			Default("medium"),
		field.String("risk_level").
			Comment("风险等级").
			Default("medium"),
		field.Int("work_item_id").
			Comment("关联的 WorkItem（tickets.id），唯一且必填；共享字段只从该 WorkItem 读取和写入"),
		field.Time("planned_start_date").
			Comment("计划开始时间").
			Optional(),
		field.Time("planned_end_date").
			Comment("计划结束时间").
			Optional(),
		field.Time("actual_start_date").
			Comment("实际开始时间").
			Optional(),
		field.Time("actual_end_date").
			Comment("实际结束时间").
			Optional(),
		field.Text("implementation_plan").
			Comment("实施计划").
			Optional(),
		field.Text("rollback_plan").
			Comment("回滚计划").
			Optional(),
		field.JSON("affected_cis", []string{}).
			Comment("受影响的配置项").
			Optional(),
	}
}

// Edges of the Change.
func (Change) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("work_item", Ticket.Type).
			Field("work_item_id").
			Unique().
			Required().
			Comment("共享字段的唯一权威 WorkItem"),
		edge.From("problems", Problem.Type).
			Ref("changes").
			Comment("关联的问题"),
		edge.To("pir", ChangePIR.Type).
			Comment("实施后审查"),
	}
}

// Indexes of the Change.
func (Change) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_item_id").Unique(),
	}
}
