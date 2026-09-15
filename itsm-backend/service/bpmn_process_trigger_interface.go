package service

import (
	"context"
	"fmt"
	"sync"

	"itsm-backend/dto"
	"itsm-backend/ent"
)

// TransactionalProcessStart carries the durable process result plus the
// latency-only callback delivery that must run after the caller commits its
// transaction. The callback is guarded so a caller cannot execute it twice;
// durable outbox recovery remains authoritative if the inline attempt fails.
type TransactionalProcessStart struct {
	response     *dto.ProcessTriggerResponse
	businessType dto.BusinessType
	businessID   int
	tenantID     int
	afterCommit  func(context.Context)
	once         sync.Once
}

func newTransactionalProcessStart(response *dto.ProcessTriggerResponse, businessType dto.BusinessType, businessID, tenantID int, afterCommit func(context.Context)) *TransactionalProcessStart {
	return &TransactionalProcessStart{
		response: response, businessType: businessType, businessID: businessID, tenantID: tenantID, afterCommit: afterCommit,
	}
}

func (s *TransactionalProcessStart) validateIdentity(businessType dto.BusinessType, businessID, tenantID int) error {
	if s == nil || s.response == nil || s.response.ProcessInstanceID <= 0 {
		return fmt.Errorf("missing durable process instance")
	}
	if s.businessType != businessType || s.businessID != businessID || s.tenantID != tenantID {
		return fmt.Errorf("workflow identity mismatch")
	}
	expectedBusinessKey := fmt.Sprintf("%s:%d", businessType, businessID)
	if s.response.BusinessKey != expectedBusinessKey {
		return fmt.Errorf("workflow business key mismatch")
	}
	return nil
}

func (s *TransactionalProcessStart) DeliverCommittedCallbacks(ctx context.Context) {
	if s == nil || s.afterCommit == nil {
		return
	}
	s.once.Do(func() { s.afterCommit(ctx) })
}

// TransactionalProcessTrigger starts the bound process on a caller-owned Ent
// transaction. Domains that require record+workflow atomicity must depend on
// this contract instead of starting a second transaction after persistence.
type TransactionalProcessTrigger interface {
	TriggerByBusinessTypeWithClient(ctx context.Context, client *ent.Client, businessType dto.BusinessType, businessID int, variables map[string]interface{}, triggeredBy string, tenantID int) (*TransactionalProcessStart, error)
}

// ProcessTriggerServiceInterface 流程触发服务接口
type ProcessTriggerServiceInterface interface {
	// TriggerProcess 触发流程
	TriggerProcess(ctx context.Context, req *dto.ProcessTriggerRequest) (*dto.ProcessTriggerResponse, error)

	// TriggerByBusinessType 根据业务类型自动匹配流程并触发
	TriggerByBusinessType(ctx context.Context, businessType dto.BusinessType, businessID int, variables map[string]interface{}, triggeredBy string, tenantID int) (*dto.ProcessTriggerResponse, error)

	// CancelProcess 取消流程
	CancelProcess(ctx context.Context, processInstanceID int, reason string) error

	// SuspendProcess 暂停流程
	SuspendProcess(ctx context.Context, processInstanceID int, reason string) error

	// ResumeProcess 恢复流程
	ResumeProcess(ctx context.Context, processInstanceID int) error

	// GetProcessStatus 获取流程状态
	GetProcessStatus(ctx context.Context, processInstanceID int) (*dto.ProcessTriggerResponse, error)
}

// ProcessBindingServiceInterface 流程绑定配置服务接口
type ProcessBindingServiceInterface interface {
	// CreateBinding 创建流程绑定
	CreateBinding(ctx context.Context, binding *dto.ProcessBinding) (*dto.ProcessBinding, error)

	// UpdateBinding 更新流程绑定
	UpdateBinding(ctx context.Context, id int, binding *dto.ProcessBinding) (*dto.ProcessBinding, error)

	// DeleteBinding 删除流程绑定
	DeleteBinding(ctx context.Context, id int, tenantID int) error

	// GetBinding 获取流程绑定
	GetBinding(ctx context.Context, id int, tenantID int) (*dto.ProcessBinding, error)

	// QueryBindings 查询流程绑定列表
	QueryBindings(ctx context.Context, req *dto.ProcessBindingQueryRequest) ([]*dto.ProcessBinding, error)

	// FindBestBinding 根据业务类型查找最佳流程绑定
	FindBestBinding(ctx context.Context, businessType dto.BusinessType, subType string, tenantID int) (*dto.ProcessBinding, error)

	// BatchCreateBindings 批量创建流程绑定
	BatchCreateBindings(ctx context.Context, req *dto.BatchProcessBindingRequest) error
}
