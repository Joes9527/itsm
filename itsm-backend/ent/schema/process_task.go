package schema

import (
	"itsm-backend/internal/jsonvalue"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ProcessTask holds the schema definition for the BPMN Process Task entity.
type ProcessTask struct {
	ent.Schema
}

// Fields of the ProcessTask.
func (ProcessTask) Fields() []ent.Field {
	return []ent.Field{
		field.String("task_id").
			Comment("任务ID，BPMN标准").
			Unique().
			NotEmpty(),
		field.Int("process_instance_id").
			Comment("流程实例ID").
			Positive(),
		field.String("process_definition_key").
			Comment("流程定义Key").
			NotEmpty(),
		field.String("task_definition_key").
			Comment("任务定义Key").
			NotEmpty(),
		field.String("task_name").
			Comment("任务名称").
			NotEmpty(),
		field.String("task_type").
			Comment("任务类型：user_task, service_task, script_task, manual_task").
			Default("user_task"),
		field.String("assignee").
			Comment("任务负责人").
			Optional(),
		field.String("candidate_users").
			Comment("候选用户，逗号分隔").
			Optional(),
		field.String("candidate_groups").
			Comment("候选组，逗号分隔").
			Optional(),
		field.String("status").
			Comment("任务状态：created, assigned, started, completed, cancelled").
			Default("created"),
		field.String("priority").
			Comment("优先级：low, normal, high, urgent").
			Default("normal"),
		field.Time("due_date").
			Comment("到期时间").
			Optional(),
		field.Time("created_time").
			Comment("创建时间").
			Default(time.Now),
		field.Time("assigned_time").
			Comment("分配时间").
			Optional(),
		field.Time("started_time").
			Comment("开始时间").
			Optional(),
		field.Time("completed_time").
			Comment("完成时间").
			Optional(),
		field.String("form_key").
			Comment("表单Key").
			Optional(),
		field.JSON("task_variables", jsonvalue.NumberMap{}).
			Comment("参与者可编辑的任务表单变量；回调路由和系统元数据不得存放在此字段").
			Optional(),
		field.String("callback_handler_id").
			Comment("创建任务时从流程定义解析的不可变回调处理器 ID；显式哨兵表示无回调或无法解析").
			Optional(),
		field.String("callback_task_type").
			Comment("创建任务时从流程定义解析的不可变回调任务类型").
			Optional(),
		field.String("callback_action").
			Comment("创建任务时从流程定义解析的不可变回调动作").
			Optional(),
		field.String("callback_config_ref").
			Comment("创建任务时从流程定义解析的可信连接器配置引用，不包含端点或密钥").
			Optional(),
		field.Int("aggregation_version").
			Comment("会签父任务聚合串行化版本；原子递增用于获取父行写锁").
			NonNegative().
			Default(0),
		field.Text("description").
			Comment("任务描述").
			Optional(),
		field.String("correlation_id").
			Comment("跨系统关联 ID（如 KAF session/Langfuse trace），用于委派任务的端到端追踪").
			Optional(),
		field.String("parent_task_id").
			Comment("父任务ID").
			Optional(),
		field.String("root_task_id").
			Comment("根任务ID").
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
	}
}

// Edges of the ProcessTask.
func (ProcessTask) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("process_instance", ProcessInstance.Type).
			Ref("process_tasks").
			Field("process_instance_id").
			Required().
			Unique(),
	}
}

// Indexes of the ProcessTask.
func (ProcessTask) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("task_id").
			Unique(),
		index.Fields("process_instance_id"),
		index.Fields("process_definition_key"),
		index.Fields("task_definition_key"),
		index.Fields("assignee"),
		index.Fields("status"),
		index.Fields("priority"),
		index.Fields("due_date"),
		index.Fields("tenant_id"),
		index.Fields("created_time"),
		index.Fields("parent_task_id"),
		index.Fields("root_task_id"),
	}
}
