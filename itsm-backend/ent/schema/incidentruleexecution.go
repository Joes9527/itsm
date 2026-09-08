package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type IncidentRuleExecution struct{ ent.Schema }

func (IncidentRuleExecution) Fields() []ent.Field {
	return []ent.Field{
		field.Int("rule_id").Comment("规则ID; absent for creation event decision").Positive().Optional(),
		field.String("execution_kind").Default("rule").Immutable(),
		field.String("execution_key").Optional().Immutable(),
		field.Int("source_event_id").Optional().Immutable().Positive(),
		field.Int("actor_id").Optional().Immutable().Positive(),
		field.String("source").Optional().Immutable(),
		field.JSON("frozen_actions", []map[string]interface{}{}).Optional().Immutable().Sensitive(),
		field.Int("incident_id").Comment("事件ID").Optional(),
		field.String("status").Comment("执行状态").Default("pending"),
		field.Text("result").Comment("执行结果").Optional(),
		field.Text("error_message").Comment("错误信息").Optional(),
		field.Time("started_at").Comment("开始时间").Default(time.Now),
		field.Time("completed_at").Comment("完成时间").Optional(),
		field.Int("execution_time_ms").Comment("执行时间(毫秒)").Optional(),
		field.JSON("input_data", map[string]interface{}{}).Comment("输入数据").Optional(),
		field.JSON("output_data", map[string]interface{}{}).Comment("输出数据").Optional(),
		field.Int("tenant_id").Comment("租户ID").Positive(),
		field.Time("created_at").Comment("创建时间").Default(time.Now),
		field.Time("updated_at").Comment("更新时间").Default(time.Now).UpdateDefault(time.Now),
	}
}

func (IncidentRuleExecution) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("incident", Incident.Type).Field("incident_id").Unique().Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("source_event", OutboxEvent.Type).Field("source_event_id").Unique().Immutable().Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("action_receipts", IncidentRuleActionReceipt.Type).StorageKey(edge.Symbol("incident_rule_action_receipt_execution_fk")),
		edge.From("rule", IncidentRule.Type).
			Ref("rule_executions").
			Field("rule_id").
			Unique().
			Comment("规则"),
	}
}

func (IncidentRuleExecution) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "execution_key").Unique(),
		index.Fields("tenant_id", "source_event_id"),
	}
}
