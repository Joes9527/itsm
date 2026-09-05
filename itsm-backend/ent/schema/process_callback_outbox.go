package schema

import (
	"itsm-backend/internal/jsonvalue"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ProcessCallbackOutbox holds durable callback delivery state for BPMN execution.
type ProcessCallbackOutbox struct {
	ent.Schema
}

// Fields of the ProcessCallbackOutbox.
func (ProcessCallbackOutbox) Fields() []ent.Field {
	return []ent.Field{
		field.String("execution_key").Unique().NotEmpty(),
		field.Int("tenant_id").Positive(),
		field.Int("process_instance_id").Positive(),
		field.Int("process_task_id").Optional().Positive(),
		field.String("task_id").Optional(),
		field.String("callback_kind").NotEmpty(),
		field.String("handler_id").NotEmpty(),
		field.String("task_type").NotEmpty(),
		field.String("element_id").NotEmpty(),
		field.String("action").Optional(),
		field.String("config_ref").
			Comment("可信连接器配置引用；端点和凭据在执行时解析，绝不持久化到回调载荷").
			Optional(),
		field.JSON("variables", jsonvalue.NumberMap{}).
			Comment("按处理器声明字段过滤后的非敏感业务载荷").
			Optional(),
		field.Bool("optional_declared").Default(false),
		field.String("status").Default("pending"),
		field.Int("attempt_count").NonNegative().Default(0),
		field.Time("next_attempt_at").Default(time.Now),
		field.String("lease_owner").Optional(),
		field.Time("lease_expires_at").Optional(),
		field.String("last_error_class").Optional().MaxLen(128),
		field.Time("completed_at").Optional(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Indexes of the ProcessCallbackOutbox.
func (ProcessCallbackOutbox) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "status", "next_attempt_at"),
		index.Fields("tenant_id", "status", "lease_expires_at"),
		index.Fields("tenant_id", "process_instance_id", "status"),
		index.Fields("tenant_id", "process_task_id"),
		index.Fields("execution_key").Unique(),
	}
}
