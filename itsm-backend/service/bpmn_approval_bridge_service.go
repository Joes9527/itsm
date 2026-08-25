package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"itsm-backend/common"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/processinstance"
	"itsm-backend/ent/processtask"
	"itsm-backend/service/bpmn"

	"go.uber.org/zap"
)

// BPMNApprovalBridge 将业务域审批入口（工单/变更 approve|reject）桥接到 BPMN 任务完成，
// 保证审批动作以 BPMN 任务为权威来源，流程实例不因业务侧直批而悬挂（P0-1 双轨审批收敛）。
//
// 约定：ProcessTriggerService 以 businessKey = "{business_type}:{business_id}" 启动流程实例，
// 桥接层按同一约定反查运行中的实例及其待办用户任务。
type BPMNApprovalBridge struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

func NewBPMNApprovalBridge(client *ent.Client, logger *zap.SugaredLogger) *BPMNApprovalBridge {
	return &BPMNApprovalBridge{client: client, logger: logger}
}

// CompleteBusinessApprovalTask 按业务键查找运行中的流程实例，并以 actorUserID 身份
// 完成其当前待办用户任务（写入 approvalAction/approvalResult/approvalComment 决策变量）。
//
// 返回值 handled：
//   - true  已找到并完成对应 BPMN 任务，调用方仍可继续维护业务侧审批记录；
//   - false 业务对象没有关联的运行中流程实例或无待办用户任务，调用方按旧逻辑处理（兼容未绑定流程的历史数据）。
//
// 若存在待办任务但完成失败（如操作人不是任务审批人/候选人），返回错误，调用方必须中止业务侧审批，
// 避免业务状态与流程状态分叉。
func (b *BPMNApprovalBridge) CompleteBusinessApprovalTask(ctx context.Context, tenantID, actorUserID int, businessType string, businessID int, action, comment string) (bool, error) {
	if action != "approve" && action != "reject" {
		return false, nil
	}
	if tenantID <= 0 || businessID <= 0 {
		return false, nil
	}

	_, task, err := b.findPendingApprovalTask(ctx, tenantID, businessType, businessID)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, nil
	}

	approvalResult := "approved"
	if action == "reject" {
		approvalResult = "rejected"
	}
	variables := map[string]interface{}{
		"approvalAction":  action,
		"approvalResult":  approvalResult,
		"approvalComment": strings.TrimSpace(comment),
		"business_type":   strings.ToLower(businessType),
		"business_id":     businessID,
		// release_approval_flow 的 Gateway_ApprovalResult 按 approval_pass 路由到
		// Activity_Schedule；此前无人写这个变量，审批完成后流程停在网关上。
		"approval_pass": action == "approve",
	}
	// change_task 的动作读 change_id：审批桥完成 Activity_CABApproval 时 approveChange/
	// rejectChange 的副作用依赖它，此前缺失导致回调静默失败（引擎只 Warn 不阻断）。
	if strings.EqualFold(businessType, string(dto.BusinessTypeChange)) {
		variables["change_id"] = businessID
	}

	// 注入认证操作人与租户，供引擎的 authorizeTaskActor / recordApprovalDecision 使用
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, actorUserID)

	engine := NewCustomProcessEngine(b.client, b.logger)
	if err := engine.CompleteTask(workflowCtx, task.TaskID, variables); err != nil {
		return false, fmt.Errorf("完成流程审批任务失败: %w", err)
	}

	b.logger.Infow("业务审批已桥接完成BPMN任务",
		"businessType", businessType, "businessId", businessID, "taskId", task.TaskID, "action", action, "actorUserId", actorUserID)
	return true, nil
}

// DelegateBusinessApprovalTask 将业务侧的审批委派同步到 BPMN 待办任务：
// 按业务键反查运行中实例的当前待办用户任务，校验操作人是任务审批人/候选人后，
// 重新指派给 newAssigneeUserID，避免业务侧委派后流程任务仍停留在原审批人（双轨分叉）。
//
// 返回语义与 CompleteBusinessApprovalTask 一致：
//   - (true, nil)  已同步委派对应 BPMN 任务；
//   - (false, nil) 无关联运行中实例或无待办用户任务，调用方按旧逻辑处理；
//   - (false, err) 存在待办任务但同步失败，调用方必须中止业务侧委派。
func (b *BPMNApprovalBridge) DelegateBusinessApprovalTask(ctx context.Context, tenantID, actorUserID int, businessType string, businessID, newAssigneeUserID int) (bool, error) {
	if tenantID <= 0 || businessID <= 0 || newAssigneeUserID <= 0 {
		return false, nil
	}

	_, task, err := b.findPendingApprovalTask(ctx, tenantID, businessType, businessID)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, nil
	}

	// 注入认证操作人与租户，委派前校验操作人必须是当前任务的审批人/候选人，防止越权改派
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, actorUserID)

	engine := NewCustomProcessEngine(b.client, b.logger)
	if customEngine, ok := engine.(*CustomProcessEngine); ok {
		if err := customEngine.authorizeTaskActor(workflowCtx, task); err != nil {
			return false, fmt.Errorf("委派流程任务失败: %w", err)
		}
	}
	if err := engine.TaskService().DelegateTask(workflowCtx, task.TaskID, strconv.Itoa(newAssigneeUserID)); err != nil {
		return false, fmt.Errorf("委派流程任务失败: %w", err)
	}

	b.logger.Infow("业务审批委派已同步BPMN任务",
		"businessType", businessType, "businessId", businessID, "taskId", task.TaskID,
		"actorUserId", actorUserID, "newAssigneeUserId", newAssigneeUserID)
	return true, nil
}

// CompleteBusinessStageTask 把业务域的阶段流转（发布排期/执行/验证、变更排期/实施/验证/关闭）
// 桥接到 BPMN 任务完成：按业务键反查运行中实例，找到与 taskDefinitionKey 匹配的待办
// 用户任务，注入业务身份变量后完成它。
//
// 与 CompleteBusinessApprovalTask 的区别：
//   - 按节点定义键精确匹配（审批桥取"当前任一待办任务"，阶段桥必须落在声明语义的节点上）；
//   - 变量注入 business_id（businessType=change 时同时注入 change_id），满足 handler 侧
//     "调用方显式传业务 ID 才触发副作用"的契约（见 bpmn_process_engine.go
//     dispatchUserTaskCallback 的设计注释，引擎刻意不合并实例变量）。
//
// 返回语义与 CompleteBusinessApprovalTask 一致：无匹配任务返回 (false, nil)，调用方回退
// 旧逻辑；有匹配任务但完成失败返回错误，调用方必须中止业务侧状态流转。
func (b *BPMNApprovalBridge) CompleteBusinessStageTask(ctx context.Context, tenantID, actorUserID int, businessType string, businessID int, taskDefinitionKey string, extraVars map[string]interface{}) (bool, error) {
	if tenantID <= 0 || businessID <= 0 || taskDefinitionKey == "" {
		return false, nil
	}

	_, task, err := b.findPendingBusinessTask(ctx, tenantID, businessType, businessID, taskDefinitionKey)
	if err != nil {
		return false, err
	}
	if task == nil {
		return false, nil
	}

	variables := map[string]interface{}{
		"business_type": strings.ToLower(businessType),
		"business_id":   businessID,
	}
	if strings.EqualFold(businessType, string(dto.BusinessTypeChange)) {
		variables["change_id"] = businessID
	}
	for k, v := range extraVars {
		variables[k] = v
	}

	// 注入认证操作人与租户，供引擎的 authorizeTaskActor / recordApprovalDecision 使用
	workflowCtx := context.WithValue(ctx, bpmn.BPMNTenantIDContextKey, tenantID)
	workflowCtx = context.WithValue(workflowCtx, bpmn.BPMNUserIDContextKey, actorUserID)

	engine := NewCustomProcessEngine(b.client, b.logger)
	if err := engine.CompleteTask(workflowCtx, task.TaskID, variables); err != nil {
		return false, fmt.Errorf("完成流程阶段任务失败: %w", err)
	}

	b.logger.Infow("业务阶段流转已桥接完成BPMN任务",
		"businessType", businessType, "businessId", businessID,
		"taskDefinitionKey", taskDefinitionKey, "taskId", task.TaskID, "actorUserId", actorUserID)
	return true, nil
}

// findPendingApprovalTask 按业务键查找运行中流程实例及其当前待办用户任务。
// 无关联实例或无待办任务时返回 (nil, nil, nil)，调用方回退旧逻辑。
// 待办状态包含 delegated，保证委派后的任务仍可被新审批人通过桥接完成。
func (b *BPMNApprovalBridge) findPendingApprovalTask(ctx context.Context, tenantID int, businessType string, businessID int) (*ent.ProcessInstance, *ent.ProcessTask, error) {
	return b.findPendingBusinessTask(ctx, tenantID, businessType, businessID, "")
}

// findPendingBusinessTask 按业务键（+ 可选节点定义键）查找运行中实例的待办用户任务。
// taskDefinitionKey 为空时取当前任一待办任务（审批桥语义）；非空时精确匹配节点。
func (b *BPMNApprovalBridge) findPendingBusinessTask(ctx context.Context, tenantID int, businessType string, businessID int, taskDefinitionKey string) (*ent.ProcessInstance, *ent.ProcessTask, error) {
	businessKey := fmt.Sprintf("%s:%d", strings.ToLower(businessType), businessID)
	instance, err := b.client.ProcessInstance.Query().
		Where(
			processinstance.BusinessKey(businessKey),
			processinstance.TenantID(tenantID),
			processinstance.Status("running"),
		).
		Order(ent.Desc(processinstance.FieldStartTime)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("查询业务关联流程实例失败: %w", err)
	}

	taskQuery := b.client.ProcessTask.Query().
		Where(
			processtask.ProcessInstanceID(instance.ID),
			processtask.TenantID(tenantID),
			processtask.TaskType("user_task"),
			processtask.StatusIn(
				common.ProcessTaskStatusCreated,
				common.ProcessTaskStatusAssigned,
				common.ProcessTaskStatusStarted,
				common.ProcessTaskStatusDelegated,
			),
		)
	if taskDefinitionKey != "" {
		taskQuery = taskQuery.Where(processtask.TaskDefinitionKey(taskDefinitionKey))
	}
	task, err := taskQuery.
		Order(ent.Asc(processtask.FieldCreatedTime)).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			// 实例在运行但当前无（匹配的）待办用户任务（例如停留在自动节点），不桥接，交由旧逻辑处理
			b.logger.Warnw("业务桥接：流程实例无匹配的待办用户任务",
				"businessKey", businessKey, "taskDefinitionKey", taskDefinitionKey,
				"processInstanceID", instance.ID)
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("查询流程待办任务失败: %w", err)
	}
	return instance, task, nil
}
