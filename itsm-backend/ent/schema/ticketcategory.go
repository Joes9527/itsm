package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// TicketCategory holds the schema definition for the TicketCategory entity.
type TicketCategory struct {
	ent.Schema
}

// Fields of the TicketCategory.
func (TicketCategory) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Comment("分类名称").
			NotEmpty(),
		field.Text("description").
			Comment("分类描述").
			Optional(),
		field.String("code").
			Comment("分类代码").
			Unique().
			NotEmpty(),
		field.Int("parent_id").
			Comment("父分类ID").
			Optional(),
		field.Int("level").
			Comment("分类层级").
			Default(1),
		field.Int("sort_order").
			Comment("排序顺序").
			Default(0),
		field.Bool("is_active").
			Comment("是否启用").
			Default(true),
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.Int("department_id").
			Comment("所属部门ID").
			Optional(),
		field.String("itsm_type").
			Comment("ITSM类型: Request/Incident/Change").
			Optional(),
		field.String("default_priority").
			Comment("默认优先级: P1/P2/P3/P4").
			Optional(),
		field.String("sla_tier").
			Comment("SLA等级: 标准服务/快速标准服务/审批类服务/安全响应服务等").
			Optional(),
		field.String("default_resolver").
			Comment("默认处理团队/角色").
			Optional(),
		field.Bool("is_user_facing").
			Comment("是否面向用户展示").
			Default(true),
		field.Time("created_at").
			Comment("创建时间").
			Default(time.Now),
		field.Time("updated_at").
			Comment("更新时间").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the TicketCategory.
func (TicketCategory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tickets", Ticket.Type).
			Comment("此分类下的工单"),
		edge.To("children", TicketCategory.Type).
			Comment("子分类"),
		edge.From("parent", TicketCategory.Type).
			Ref("children").
			Field("parent_id").
			Unique().
			Comment("父分类"),
		edge.From("department", Department.Type).
			Ref("categories").
			Field("department_id").
			Unique().
			Comment("所属部门"),
	}
}
