package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// User holds the schema definition for the User entity.
type User struct {
	ent.Schema
}

// Fields of the User.
func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("username").
			Comment("用户名").
			Unique().
			NotEmpty(),
		field.String("email").
			Comment("邮箱").
			Unique().
			NotEmpty(),
		field.String("name").
			Comment("姓名").
			NotEmpty(),
		field.String("role").
			Comment("角色代码，对应 roles 表的 code 字段").
			NotEmpty().
			Default("end_user"),
		field.String("department").
			Comment("部门").
			Optional(),
		field.Int("department_id").
			Comment("部门ID").
			Optional(),
		field.String("phone").
			Comment("电话").
			Optional(),
		field.String("feishu_open_id").
			Comment("飞书用户OpenID").
			Optional().
			Unique(),
		field.String("password_hash").
			Comment("密码哈希").
			NotEmpty(),
		field.Bool("active").
			Comment("是否激活").
			Default(true),
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
		field.Enum("msp_role").
			Comment("MSP角色: provider_admin=MSP管理员, provider_agent=MSP客服, customer_user=客户用户").
			Values("provider_admin", "provider_agent", "customer_user").
			Optional(),
		field.Int("assigned_by_msp_id").
			Comment("MSP分配人ID").
			Optional(),
		field.Bool("is_bootstrap_admin").
			Comment("是否通过bootstrap token创建").
			Default(false),
		field.String("gender").
			Comment("性别: male/female，留空表示未填写").
			Optional(),
		field.Bool("is_leader").
			Comment("是否为部门负责人/领导，用于组织架构展示").
			Default(false),
		field.String("function_line").
			Comment("职能条线：HR 系统里跨法人实体的横向职能分组（如'SPT_资讯科技服务部'），" +
				"独立于 department_id 代表的正式组织树——同一条线的人可能分散在不同法人实体/" +
				"仓库下面。来自 ehr-data.xlsx person 表的 depart_line 字段。").
			Optional(),
		field.Int("manager_id").
			Comment("直属上级的用户ID（汇报线，个人级别，跟 department.manager_id 那种" +
				"部门级负责人是两个概念）。来自 ehr-data.xlsx person 表的 direct_supervisor" +
				"字段（格式\"姓名:工号\"，按工号匹配到 username）。跟 department.manager_id" +
				"一样是普通整型外键，不建 ent edge。0/未设置表示无记录或没匹配上。").
			Optional(),
	}
}

// Edges of the User.
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("department_ref", Department.Type).
			Ref("users").
			Field("department_id").
			Unique(),
		edge.From("tenant", Tenant.Type).
			Ref("users").
			Field("tenant_id").
			Required().
			Unique(),
		edge.To("tickets", Ticket.Type).
			Comment("用户提交的工单"),
		edge.To("assigned_tickets", Ticket.Type).
			Comment("分配给用户的工单"),
		edge.To("ticket_comments", TicketComment.Type).
			Comment("工单评论"),
		edge.To("ticket_attachments", TicketAttachment.Type).
			Comment("工单附件"),
		edge.To("ticket_notifications", TicketNotification.Type).
			Comment("工单通知"),
		edge.To("notification_preferences", NotificationPreference.Type).
			Comment("通知偏好"),
		edge.To("roles", Role.Type).
			Comment("用户角色"),
		edge.To("version_changelogs", ProcessVersionChangelog.Type).
			Comment("版本变更日志"),
		edge.To("groups", Group.Type).
			Comment("用户所属组"),
		edge.To("msp_allocations", MSPAllocation.Type).
			Comment("MSP用户分配"),
		edge.To("article_sessions", KnowledgeArticleSession.Type).
			Comment("文章协作会话"),
		edge.To("article_participations", KnowledgeArticleParticipant.Type).
			Comment("文章协作参与"),
		edge.To("pir_reviews", ChangePIR.Type).
			Comment("PIR审查记录"),
		edge.To("tool_invocations", ToolInvocation.Type).
			Comment("AI 工具调用记录"),
	}
}
