package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Department holds the schema definition for the Department entity.
type Department struct {
	ent.Schema
}

// Fields of the Department.
func (Department) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Comment("部门名称").
			NotEmpty(),
		field.String("code").
			Comment("部门代码").
			NotEmpty(),
		field.Text("description").
			Comment("部门描述").
			Optional(),
		field.Int("manager_id").
			Comment("部门经理ID").
			Optional(),
		field.Int("parent_id").
			Comment("父部门ID").
			Optional(),
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.Time("created_at").
			Comment("创建时间").
			Default(time.Now),
		field.Time("updated_at").
			Comment("更新时间").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.String("area_name").
			Comment("区域/地域名称").
			Optional(),
		field.String("org_type").
			Comment("组织类型: department=行政部门, warehouse=仓库/物流节点").
			Optional().
			Default("department"),
		field.String("node_type").
			Comment("节点类型: company=公司, branch=分公司, department=部门, team=组；空串=未分类。与 org_type 的仓库维度并存，不是同一件事").
			Optional().
			Default(""),
		field.Time("deleted_at").
			Comment("软删除时间").
			Optional().
			Nillable(),
	}
}

// Indexes of the Department.
//
// 组织编码是节点的稳定业务键，必须在一个租户内唯一：否则导入重跑会产生
// 同名重复节点，组织树无法区分，审批找人也会选错分支。
func (Department) Indexes() []ent.Index {
	return []ent.Index{
		// 与迁移 049 保持一致：**部分**唯一索引，软删除的行不占用编码。
		//
		// 这两处必须同形，否则测试与生产会分叉：Ent 生成的非部分索引会让"软删除后
		// 复用同一编码"在 sqlite 测试里直接违反约束，而生产（已应用 049 的部分索引）
		// 是允许的——于是软删除相关的行为在测试里永远覆盖不到。
		index.Fields("tenant_id", "code").
			Unique().
			Annotations(entsql.IndexWhere("deleted_at IS NULL")),
	}
}

// Edges of the Department.
func (Department) Edges() []ent.Edge {
	return []ent.Edge{
		// 树形结构关系
		edge.To("children", Department.Type).
			From("parent").
			Field("parent_id").
			Unique(),

		edge.To("users", User.Type).
			Comment("部门成员"),
		edge.To("tickets", Ticket.Type).
			Comment("部门工单"),
		edge.To("categories", TicketCategory.Type).
			Comment("部门工单分类"),
		edge.To("projects", Project.Type).
			Comment("部门项目"),
		edge.To("tags", Tag.Type).
			Comment("部门标签"),
	}
}
