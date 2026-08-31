package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ServiceRequest struct{ ent.Schema }

func (ServiceRequest) Fields() []ent.Field {
	return []ent.Field{
		// 基础信息
		field.Int("ticket_id").Comment("关联的Ticket ID——状态/审批/工作流全部委托给它").Positive(),
		field.Int("catalog_id").Comment("服务目录ID").Positive(),
		field.Int("ci_id").Comment("关联CI ID").Optional(),

		// 表单数据
		field.JSON("form_data", map[string]any{}).Comment("表单数据").Optional(),
		field.String("cost_center").Comment("成本中心").Optional(),
		field.String("data_classification").Comment("数据分级：public|internal|confidential").Default("internal"),
		field.Bool("needs_public_ip").Comment("是否需要公网访问").Default(false),
		field.JSON("source_ip_whitelist", []string{}).Comment("源IP白名单").Optional(),
		field.Time("expire_at").Comment("到期时间").Optional(),
		field.Bool("compliance_ack").Comment("合规条款确认").Default(false),

		// 通用层字段：所有 service_type 都适用，取代原来只进 form_data 就没人读的假字段
		field.String("contact_name").Comment("联系人姓名，默认取申请人姓名，可编辑以支持代他人提交").Optional(),
		field.String("contact_email").Comment("联系人邮箱，默认取申请人邮箱，可编辑以支持代他人提交").Optional(),
		field.Int("quantity").Comment("申请数量").Default(1).Positive(),
		field.Time("expected_at").Comment("期望交付时间").Optional(),

		// 实施信息（资源交付，不属于本次重构范围，原样保留）
		field.Int("processor_id").Comment("处理人ID").Optional(),
		field.Time("started_at").Comment("开始处理时间").Optional(),
		field.Time("completed_at").Comment("完成时间").Optional(),
		field.Text("completion_note").Comment("完成备注").Optional(),
		field.Text("last_error").Comment("最近一次错误信息").Optional(),
		field.Int("version").Comment("乐观锁版本").Default(1).Positive(),

		field.Time("deleted_at").Comment("软删除时间").Optional().Nillable(),
	}
}
func (ServiceRequest) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("work_item", Ticket.Type).
			Ref("service_request").
			Field("ticket_id").
			Unique().
			Required().
			Comment("公共字段与租户边界的权威 WorkItem"),
	}
}

func (ServiceRequest) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("ticket_id").Unique(),
		index.Fields("ci_id"),
	}
}
