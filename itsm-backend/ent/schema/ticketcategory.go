package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
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
			Comment("分类代码（租户内唯一，创建后不可修改）").
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
		edge.To("default_catalogs", ServiceCatalog.Type).
			Comment("把该分类作为默认 CTI 的服务目录项"),
	}
}

// Indexes of the TicketCategory.
//
// code 的唯一范围是租户内，而不是全表：分类由各租户独立维护，跨租户重名/同码是
// 合法业务事实。表级全局唯一会让第二个租户无法创建同名代码，与 service 层按租户
// 判重以及多租户产品契约不一致。迁移 048 在同一次变更中放宽该约束。
func (TicketCategory) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "code").Unique(),
	}
}
