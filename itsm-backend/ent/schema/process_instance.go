package schema

import (
	"itsm-backend/internal/jsonvalue"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ProcessInstance holds the schema definition for the BPMN Process Instance entity.
type ProcessInstance struct {
	ent.Schema
}

// Fields of the ProcessInstance.
func (ProcessInstance) Fields() []ent.Field {
	return []ent.Field{
		field.Int("execution_work_item_id").Optional().Nillable().Immutable().Positive().Comment("Immutable execution WorkItem reference; historical rows remain NULL; FK managed by migration 039"),
		field.String("process_instance_id").
			Comment("流程实例ID，BPMN标准").
			Unique().
			NotEmpty(),
		field.String("start_request_digest").
			Comment("Immutable digest of a durable start request; NULL for legacy non-idempotent starts").
			Optional().Immutable().Sensitive(),
		field.String("business_key").
			Comment("业务键，关联业务实体").
			Optional(),
		field.String("business_type").
			Comment("结构化业务类型。按统一 WorkItem 设计 §15.2.2，本字段就是 WorkItem.recordClass（generic/service_request_item/incident/problem/change_request/catalog_task），词表与业务键格式的唯一权威是 common/workitemidentity；Wave-1 旧词表（ticket/change/service_request）已退役，不再写入也不被解析。Release 保留其显式遗留值 \"release\"，它不是 WorkItem。与 business_key 由同一次 TriggerProcess 调用原子写入，不从 variables JSON 里现取").
			Optional(),
		field.Int("business_id").
			Comment("结构化业务主键，恒为 WorkItem ID（tickets.id），与 business_type 成对使用；专业扩展表主键从不写入此处").
			Optional(),
		field.String("process_definition_key").
			Comment("流程定义Key").
			NotEmpty(),
		field.Int("process_definition_id").
			Comment("流程定义ID").
			Positive(),
		field.String("status").
			Comment("实例状态：running, suspended, completed, terminated").
			Default("running"),
		field.String("current_activity_id").
			Comment("当前活动ID").
			Optional(),
		field.String("current_activity_name").
			Comment("当前活动名称").
			Optional(),
		field.JSON("variables", jsonvalue.NumberMap{}).
			Comment("流程变量").
			Optional(),
		field.Time("start_time").
			Comment("开始时间").
			Default(time.Now),
		field.Time("end_time").
			Comment("结束时间").
			Optional(),
		field.Time("suspended_time").
			Comment("暂停时间").
			Optional(),
		field.String("suspended_reason").
			Comment("暂停原因").
			Optional(),
		field.Int("tenant_id").
			Comment("租户ID").
			Positive(),
		field.Int("version").
			Comment("乐观锁版本号").
			Default(1),
		field.String("initiator").
			Comment("流程发起人").
			Optional(),
		field.String("parent_process_instance_id").
			Comment("父流程实例ID").
			Optional(),
		field.String("root_process_instance_id").
			Comment("根流程实例ID").
			Optional(),
		field.JSON("state_snapshot", []byte{}).
			Comment("流程引擎状态快照").
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

// Edges of the ProcessInstance.
func (ProcessInstance) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("process_tasks", ProcessTask.Type).
			Comment("流程任务"),
		edge.To("process_variables", ProcessVariable.Type).
			Comment("流程变量"),
		edge.To("execution_history", ProcessExecutionHistory.Type).
			Comment("执行历史"),
		edge.From("definition", ProcessDefinition.Type).
			Ref("process_instances").
			Field("process_definition_id").
			Required().
			Unique(),
	}
}

// Indexes of the ProcessInstance.
func (ProcessInstance) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("process_instance_id").
			Unique(),
		index.Fields("business_key"),
		index.Fields("process_definition_key"),
		index.Fields("process_definition_id"),
		index.Fields("status"),
		index.Fields("tenant_id"),
		index.Fields("initiator"),
		index.Fields("start_time"),
		index.Fields("parent_process_instance_id"),
		index.Fields("root_process_instance_id"),
		index.Fields("tenant_id", "business_type", "business_id", "status"),
	}
}
