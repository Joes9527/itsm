package service

import (
	"context"
	"fmt"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/processcallbackoutbox"
)

// loadTaskCallbackBlocks reads only required callback outcomes for already
// authorized tasks in the caller's repeatable-read snapshot. DISTINCT bounds
// each result to the task chunk even when a task has many callback attempts.
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
		ids, err := client.ProcessCallbackOutbox.Query().Where(
			processcallbackoutbox.Status("blocked"), processcallbackoutbox.OptionalDeclared(false),
			processcallbackoutbox.Or(predicates...)).Unique(true).Select(processcallbackoutbox.FieldProcessTaskID).Ints(ctx)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			result[id] = &dto.BPMNTaskCallbackBlock{Code: "required_callback_blocked", Reason: "必要流程操作已阻塞，请联系管理员处理。"}
		}
	}
	return result, nil
}
