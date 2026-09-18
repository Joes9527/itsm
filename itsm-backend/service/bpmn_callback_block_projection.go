package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"entgo.io/ent/dialect/sql"
	"itsm-backend/service/bpmn"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processcallbackoutbox"
)

// loadTaskCallbackBlocks reads only required callback outcomes for already
// authorized tasks in the caller's repeatable-read snapshot. Grouping bounds
// each result to one safe code per task even with many callback attempts.
// Never expose callback payloads or infrastructure error text.
func loadTaskCallbackBlocks(ctx context.Context, client *ent.Client, tasks []*ent.ProcessTask) (map[int]*dto.BPMNTaskCallbackBlock, error) {
	if taskReadSnapshot(ctx, client) == nil {
		return nil, fmt.Errorf("task callback projection requires read snapshot")
	}
	result := make(map[int]*dto.BPMNTaskCallbackBlock)
	for start := 0; start < len(tasks); start += bpmnTaskReadBatchSize {
		end := start + bpmnTaskReadBatchSize
		if end > len(tasks) {
			end = len(tasks)
		}
		predicates := make([]predicate.ProcessCallbackOutbox, 0, end-start)
		for _, task := range tasks[start:end] {
			predicates = append(predicates, processcallbackoutbox.And(
				processcallbackoutbox.TenantID(task.TenantID),
				processcallbackoutbox.ProcessInstanceID(task.ProcessInstanceID),
				processcallbackoutbox.ProcessTaskID(task.ID)))
		}
		var rows []struct {
			ProcessTaskID int    `json:"process_task_id"`
			SafeCode      string `json:"safe_code"`
		}
		err := client.ProcessCallbackOutbox.Query().Where(
			processcallbackoutbox.Status("blocked"), processcallbackoutbox.OptionalDeclared(false),
			processcallbackoutbox.Or(predicates...)).
			GroupBy(processcallbackoutbox.FieldProcessTaskID).Aggregate(safeCallbackBlockAggregate).Scan(ctx, &rows)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			code, reason := genericCallbackBlockCode, genericCallbackBlockReason
			safeCode := bpmn.CallbackBlockCode(row.SafeCode)
			if bpmn.IsAllowedCallbackBlockCode(safeCode) {
				code = string(safeCode)
				if fixed, ok := callbackBlockReasons[safeCode]; ok {
					reason = fixed
				}
			}
			result[row.ProcessTaskID] = &dto.BPMNTaskCallbackBlock{Code: code, Reason: reason}
		}
	}
	return result, nil
}

const (
	genericCallbackBlockCode   = "required_callback_blocked"
	genericCallbackBlockReason = "必要流程操作已阻塞，请联系管理员处理。"
)

var callbackBlockReasons = map[bpmn.CallbackBlockCode]string{
	bpmn.CallbackBlockHandlerContract:     "流程操作所需参数缺失或无效，请联系管理员核验任务配置。",
	bpmn.CallbackBlockTargetMissing:       "流程操作的目标记录不可用，请联系管理员核验目标。",
	bpmn.CallbackBlockTargetTypeMismatch:  "流程操作的目标类型不匹配，请联系管理员核验配置。",
	bpmn.CallbackBlockRecipientMissing:    "流程通知缺少接收人，请联系管理员核验配置。",
	bpmn.CallbackBlockRecipientEmpty:      "流程通知没有可用接收人，请联系管理员核验配置。",
	bpmn.CallbackBlockUnsupportedCCType:   "流程通知的抄送类型不受支持，请联系管理员核验配置。",
	bpmn.CallbackBlockUnsupportedTemplate: "流程通知模板包含不受支持的占位符，请联系管理员核验配置。",
	bpmn.CallbackBlockChannelUnavailable:  "流程通知渠道不可用，请联系管理员处理。",
	bpmn.CallbackBlockDeliveryNotCreated:  "流程通知投递记录未创建，请联系管理员处理。",
}

// SQL sanitizes before aggregation: arbitrary stored error classes never cross
// the projection boundary. MIN chooses a stable allowed reason if several
// required callbacks are blocked; unknown classes cannot hide a known reason.
func safeCallbackBlockAggregate(s *sql.Selector) string {
	codes := make([]string, 0, len(callbackBlockReasons))
	for code := range callbackBlockReasons {
		if bpmn.IsAllowedCallbackBlockCode(code) {
			codes = append(codes, "'"+string(code)+"'")
		}
	}
	sort.Strings(codes)
	column := s.C(processcallbackoutbox.FieldLastErrorClass)
	return fmt.Sprintf("COALESCE(MIN(CASE WHEN %s IN (%s) THEN %s ELSE NULL END), '%s') AS safe_code", column, strings.Join(codes, ","), column, genericCallbackBlockCode)
}
