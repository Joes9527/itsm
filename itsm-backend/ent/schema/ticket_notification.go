package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TicketNotification holds the schema definition for the TicketNotification entity.
type TicketNotification struct {
	ent.Schema
}

// Fields of the TicketNotification.
func (TicketNotification) Fields() []ent.Field {
	return []ent.Field{
		field.Int("ticket_id").
			Comment("工单ID").
			Positive(),
		field.Int("user_id").
			Comment("接收人ID").
			Positive(),
		field.String("type").
			Comment("通知类型: created, assigned, status_changed, commented, sla_warning, resolved, closed").
			NotEmpty(),
		field.String("channel").
			Comment("通知渠道: email, in_app, sms").
			Default("in_app"),
		field.Text("content").
			Comment("通知内容").
			NotEmpty(),
		field.Time("sent_at").
			Comment("发送时间").
			Optional(),
		field.Time("read_at").
			Comment("阅读时间").
			Optional(),
		field.String("status").
			Comment("状态: pending, processing, sent, read").
			Default("pending"),
		field.String("delivery_key").
			Comment("内部回调投递幂等键，不对 API 暴露").
			Optional().
			Nillable().
			Sensitive(),
		field.Int("attempt_count").
			Comment("外部投递尝试次数").
			NonNegative().
			Default(0).
			StructTag(`json:"-"`),
		field.Time("next_attempt_at").
			Comment("下次允许投递时间").
			Default(time.Now).
			StructTag(`json:"-"`),
		field.String("lease_owner").
			Comment("当前投递租约持有者").
			Optional().
			Sensitive(),
		field.Time("lease_expires_at").
			Comment("当前投递租约过期时间").
			Optional().
			StructTag(`json:"-"`),
		field.String("last_error_class").
			Comment("最近一次投递失败的安全分类").
			Optional().
			MaxLen(128).
			Sensitive(),
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.Time("created_at").
			Comment("创建时间").
			Default(time.Now),
	}
}

// Edges of the TicketNotification.
func (TicketNotification) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("ticket", Ticket.Type).
			Ref("notifications").
			Field("ticket_id").
			Required().
			Unique().
			Comment("所属工单"),
		edge.From("user", User.Type).
			Ref("ticket_notifications").
			Field("user_id").
			Required().
			Unique().
			Comment("接收人"),
	}
}

// Indexes of the TicketNotification.
func (TicketNotification) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "delivery_key", "ticket_id", "user_id", "channel").Unique(),
		index.Fields("tenant_id", "status", "next_attempt_at"),
		index.Fields("tenant_id", "status", "lease_expires_at"),
	}
}
