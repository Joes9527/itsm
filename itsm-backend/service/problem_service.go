package service

import (
	"context"
	"fmt"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/problem"
	"itsm-backend/ent/user"

	"go.uber.org/zap"
)

// ProblemService 问题管理服务。
//
// 统一 WorkItem 领域模型迁移（Wave 2 · Problem 域）核实后收窄：这个类型历史上还有一整套
// CreateProblem/GetProblem/ListProblems/UpdateProblem/DeleteProblem/GetProblemStats/
// triggerWorkflowForProblem/GetWorkflowStatus/mapProcessStatus 方法群及配套的
// SetProcessTriggerService，但核实后确认它们是完全的死代码：
//   - internal/bootstrap/app.go 从未实例化过 ProblemService（全仓库搜索
//     "ProblemService{" 和 "service.NewProblemService(" 只有一处非测试调用，就是本文件
//     下面保留的 CreateKnownErrorFromProblem 这条路径），router.go 也没有任何路由指向
//     它们——真正处理 Problem CRUD 的是 handlers/problem.Service（router.go 里
//     config.ProblemHandler 挂的就是这个包）。
//   - SetProcessTriggerService 在全仓库范围内零调用点（搜索
//     "\.SetProcessTriggerService\(" 命中的全部是 release/ticket/incident/change 各自
//     的 Service，没有一处是 ProblemService 的实例），所以依赖它的
//     triggerWorkflowForProblem/GetWorkflowStatus/mapProcessStatus 连间接可达都做不到。
//
// 按 AGENTS.md §4"新旧实现共存时优先重构删除，不保留死代码"，这些方法已删除。
//
// 保留这个类型和这个文件本身，只因为 CreateKnownErrorFromProblem 这一个方法真的在跑：
// handlers/known_error/handler.go 的 CreateFromProblem（POST /problems/:id/known-error）
// 每次请求都会临时 new 一个 ProblemService 只用这一个方法（"从问题发布已知错误"）。是否把
// 它挪到 handlers/known_error 或 handlers/problem 包里、拆成一个更小的类型，是一个更大的
// 后续重构（会改变 known_error.Handler 的调用方式，以及 handlers/problem/known_error_test.go
// 里已经锁定的调用形状），超出本次 Problem WorkItem 迁移任务的范围——这次只做"删除确认
// 死掉的部分"，不做"重新安置还活着的部分"。
type ProblemService struct {
	knownErrorService *KnownErrorService
	client            *ent.Client
	logger            *zap.SugaredLogger
}

// NewProblemService 创建问题管理服务
func NewProblemService(client *ent.Client, logger *zap.SugaredLogger) *ProblemService {
	return &ProblemService{
		client: client,
		logger: logger,
	}
}

// SetKnownErrorService 注入已知错误服务，CreateKnownErrorFromProblem 依赖它完成实际创建。
func (s *ProblemService) SetKnownErrorService(kes *KnownErrorService) {
	s.knownErrorService = kes
}

// CreateKnownErrorFromProblem 从问题创建已知错误
func (s *ProblemService) CreateKnownErrorFromProblem(ctx context.Context, problemID int, createdBy int, req *dto.KEDBCreateRequest) (*dto.KEDBResponse, error) {
	s.logger.Infow("Creating known error from problem", "problemID", problemID, "createdBy", createdBy)
	if s.knownErrorService == nil {
		return nil, fmt.Errorf("known error service is not configured")
	}
	creator, err := s.client.User.Query().
		Where(user.IDEQ(createdBy), user.ActiveEQ(true)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("creator not found")
		}
		return nil, fmt.Errorf("failed to validate creator: %w", err)
	}

	// 获取问题
	problemEntity, err := s.client.Problem.Query().
		Where(problem.IDEQ(problemID), problem.TenantIDEQ(creator.TenantID), problem.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("problem not found")
		}
		return nil, fmt.Errorf("failed to get problem: %w", err)
	}

	// 构建创建已知错误的请求
	createReq := dto.KEDBCreateRequest{
		Title:            problemEntity.Title,
		Description:      problemEntity.Description,
		RootCause:        problemEntity.RootCause,
		Category:         problemEntity.Category,
		Severity:         mapPriorityToSeverity(problemEntity.Priority),
		AffectedProducts: []string{},
		AffectedCIs:      []string{},
		Keywords:         []string{problemEntity.Title},
		ProblemID:        &problemID,
	}

	// 如果用户传入了自定义请求，覆盖默认值
	if req != nil {
		if req.Title != "" {
			createReq.Title = req.Title
		}
		if req.Description != "" {
			createReq.Description = req.Description
		}
		if req.Symptoms != "" {
			createReq.Symptoms = req.Symptoms
		}
		if req.RootCause != "" {
			createReq.RootCause = req.RootCause
		}
		if req.Workaround != "" {
			createReq.Workaround = req.Workaround
		}
		if req.Resolution != "" {
			createReq.Resolution = req.Resolution
		}
		if req.Category != "" {
			createReq.Category = req.Category
		}
		if req.Severity != "" {
			createReq.Severity = req.Severity
		}
		if len(req.AffectedProducts) > 0 {
			createReq.AffectedProducts = req.AffectedProducts
		}
		if len(req.AffectedCIs) > 0 {
			createReq.AffectedCIs = req.AffectedCIs
		}
		if len(req.Keywords) > 0 {
			createReq.Keywords = req.Keywords
		}
	}

	// 验证必填字段
	if createReq.Title == "" {
		return nil, fmt.Errorf("title is required")
	}

	// 创建已知错误 - 需要先转换为CreateKnownErrorRequest
	// 注意：这里KnownErrorService需要的是CreateKnownErrorRequest，我先手动映射
	knownError, err := s.knownErrorService.CreateKnownError(ctx, dto.CreateKnownErrorRequest{
		Title:            createReq.Title,
		Description:      createReq.Description,
		Symptoms:         createReq.Symptoms,
		RootCause:        createReq.RootCause,
		Workaround:       createReq.Workaround,
		Resolution:       createReq.Resolution,
		Status:           dto.KnownErrorStatusDraft, // 默认是草稿状态，需要审批
		Category:         createReq.Category,
		Severity:         createReq.Severity,
		AffectedProducts: createReq.AffectedProducts,
		AffectedCIs:      createReq.AffectedCIs,
		Keywords:         createReq.Keywords,
		CreatedBy:        createdBy,
		TenantID:         problemEntity.TenantID,
	})
	if err != nil {
		s.logger.Errorw("Failed to create known error from problem", "error", err)
		return nil, fmt.Errorf("failed to create known error: %w", err)
	}

	// 关联已知错误到问题
	err = s.knownErrorService.LinkKnownErrorToProblem(ctx, knownError.ID, problemID, problemEntity.TenantID)
	if err != nil {
		if deleteErr := s.client.KnownError.DeleteOneID(knownError.ID).Exec(ctx); deleteErr != nil {
			s.logger.Errorw("Failed to compensate orphan known error", "knownErrorID", knownError.ID, "error", deleteErr)
		}
		return nil, fmt.Errorf("failed to link known error to problem: %w", err)
	}

	// 转换为响应
	return toKEDBResponse(knownError), nil
}

// mapPriorityToSeverity 将问题优先级映射为已知错误严重程度
func mapPriorityToSeverity(priority string) string {
	switch priority {
	case "critical":
		return dto.KnownErrorSeverityCritical
	case "high":
		return dto.KnownErrorSeverityHigh
	case "medium":
		return dto.KnownErrorSeverityMedium
	case "low":
		return dto.KnownErrorSeverityLow
	default:
		return dto.KnownErrorSeverityMedium
	}
}

// toKEDBResponse 将ent.KnownError转换为dto.KEDBResponse
func toKEDBResponse(ke *ent.KnownError) *dto.KEDBResponse {
	return &dto.KEDBResponse{
		ID:               ke.ID,
		Title:            ke.Title,
		Description:      ke.Description,
		Symptoms:         ke.Symptoms,
		RootCause:        ke.RootCause,
		Workaround:       ke.Workaround,
		Resolution:       ke.Resolution,
		Status:           ke.Status,
		Category:         ke.Category,
		Severity:         ke.Severity,
		AffectedProducts: ke.AffectedProducts,
		AffectedCIs:      ke.AffectedCis,
		Keywords:         ke.Keywords,
		OccurrenceCount:  ke.OccurrenceCount,
		CreatedBy:        ke.CreatedBy,
		TenantID:         ke.TenantID,
		CreatedAt:        ke.CreatedAt,
		UpdatedAt:        ke.UpdatedAt,
	}
}
