package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// ConnectorConfig holds the schema definition for the ConnectorConfig entity.
// 连接器配置持久化：provision 时落库，后端重启后从库中加载自动恢复，
// 避免连接器（如 msgraph-email）因进程重启而丢失。
type ConnectorConfig struct {
	ent.Schema
}

// Fields of the ConnectorConfig.
func (ConnectorConfig) Fields() []ent.Field {
	return []ent.Field{
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.String("name").
			Comment("连接器名称，如 msgraph-email / feishu").
			NotEmpty(),
		field.String("provider").
			Comment("连接器类型，如 microsoft / feishu / dingtalk").
			NotEmpty(),
		field.Bool("enabled").
			Comment("是否启用").
			Default(false),
		field.Text("credentials").
			Comment("凭据（JSON，含敏感字段，后续加密）").
			Optional(),
		field.Text("settings").
			Comment("设置（JSON）").
			Optional(),
		field.Text("labels").
			Comment("标签（JSON）").
			Optional(),
		field.Time("created_at").
			Comment("创建时间").
			Default(time.Now),
		field.Time("updated_at").
			Comment("更新时间").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}
