package service

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/database"
	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/change"
	"itsm-backend/ent/changepir"

	"go.uber.org/zap"
)

// ChangePIRService 变更实施后审查服务
type ChangePIRService struct {
	client    *ent.Client
	directory database.DirectorySnapshot
	logger    *zap.SugaredLogger
}

// NewChangePIRService 创建PIR服务
func NewChangePIRService(client *ent.Client, logger *zap.SugaredLogger) *ChangePIRService {
	return &ChangePIRService{
		client: client,
		logger: logger,
	}
}

// GetPIR 获取PIR详情
func (s *ChangePIRService) GetPIR(ctx context.Context, id, tenantID int) (*dto.ChangePIRResponse, error) {
	pir, err := s.client.ChangePIR.Query().
		Where(changepir.ID(id), changepir.TenantID(tenantID)).
		WithChange(func(q *ent.ChangeQuery) { q.WithWorkItem() }).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("PIR不存在")
		}
		return nil, fmt.Errorf("获取PIR失败: %w", err)
	}

	changeTitle := ""
	changeID := 0
	if pir.Edges.Change != nil {
		changeTitle = pir.Edges.Change.Edges.WorkItem.Title
		changeID = pir.Edges.Change.ID
	}

	return s.buildPIRResponseFull(ctx, pir, changeTitle, changeID)
}

// GetPIRByChange 获取变更关联的PIR
func (s *ChangePIRService) GetPIRByChange(ctx context.Context, changeID, tenantID int) (*dto.ChangePIRResponse, error) {
	pir, err := s.client.ChangePIR.Query().
		Where(changepir.TenantID(tenantID), changepir.HasChangeWith(change.ID(changeID))).
		WithChange(func(q *ent.ChangeQuery) { q.WithWorkItem() }).
		First(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, fmt.Errorf("该变更无PIR记录")
		}
		return nil, fmt.Errorf("获取PIR失败: %w", err)
	}

	changeTitle := ""
	if pir.Edges.Change != nil {
		changeTitle = pir.Edges.Change.Edges.WorkItem.Title
	}

	return s.buildPIRResponseFull(ctx, pir, changeTitle, changeID)
}

// ListPIRs 获取PIR列表
func (s *ChangePIRService) ListPIRs(ctx context.Context, tenantID int, page, pageSize int, result string) (*dto.ChangePIRListResponse, error) {
	query := s.client.ChangePIR.Query().
		Where(changepir.TenantID(tenantID))

	// 结果筛选
	if result != "" && result != "全部" {
		query = query.Where(changepir.OverallResult(result))
	}

	// 获取总数
	total, err := query.Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取PIR总数失败: %w", err)
	}

	// 分页查询
	pirs, err := query.
		WithChange(func(q *ent.ChangeQuery) { q.WithWorkItem() }).
		Order(ent.Desc(changepir.FieldReviewDate)).
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取PIR列表失败: %w", err)
	}

	// 构建响应列表
	items := make([]*dto.ChangePIRResponse, 0, len(pirs))
	for _, pir := range pirs {
		changeTitle := ""
		changeID := 0
		if pir.Edges.Change != nil {
			changeTitle = pir.Edges.Change.Edges.WorkItem.Title
			changeID = pir.Edges.Change.ID
		}
		response, err := s.buildPIRResponseFull(ctx, pir, changeTitle, changeID)
		if err != nil {
			s.logger.Warnw("Failed to build PIR response", "error", err, "pir_id", pir.ID)
			continue
		}
		items = append(items, response)
	}

	return &dto.ChangePIRListResponse{
		Total: total,
		Items: items,
	}, nil
}

// buildPIRResponseFull 构建完整的PIR响应（包含关联信息）
func (s *ChangePIRService) buildPIRResponseFull(ctx context.Context, pir *ent.ChangePIR, changeTitle string, changeID int) (*dto.ChangePIRResponse, error) {
	// 转换指针字段
	var successSummary, issuesEncountered, lessonsLearned, improvementRecs, rollbackReason *string
	if pir.SuccessSummary != "" {
		successSummary = &pir.SuccessSummary
	}
	if pir.IssuesEncountered != "" {
		issuesEncountered = &pir.IssuesEncountered
	}
	if pir.LessonsLearned != "" {
		lessonsLearned = &pir.LessonsLearned
	}
	if pir.ImprovementRecommendations != "" {
		improvementRecs = &pir.ImprovementRecommendations
	}
	if pir.RollbackReason != "" {
		rollbackReason = &pir.RollbackReason
	}

	var actualStartTime, actualEndTime *time.Time
	if !pir.ActualStartTime.IsZero() {
		actualStartTime = &pir.ActualStartTime
	}
	if !pir.ActualEndTime.IsZero() {
		actualEndTime = &pir.ActualEndTime
	}

	return &dto.ChangePIRResponse{
		ID:                         pir.ID,
		ChangeID:                   changeID,
		ChangeTitle:                changeTitle,
		ReviewerID:                 pir.ReviewerID,
		ReviewerName:               "", // 从reviewer_id字段获取
		OverallResult:              pir.OverallResult,
		ObjectivesAchieved:         pir.ObjectivesAchieved,
		SuccessSummary:             successSummary,
		IssuesEncountered:          issuesEncountered,
		LessonsLearned:             lessonsLearned,
		ImprovementRecommendations: improvementRecs,
		ActualStartTime:            actualStartTime,
		ActualEndTime:              actualEndTime,
		ActualDurationMinutes:      pir.ActualDurationMinutes,
		RollbackPerformed:          pir.RollbackPerformed,
		RollbackReason:             rollbackReason,
		TenantID:                   pir.TenantID,
		ReviewDate:                 pir.ReviewDate,
		CreatedAt:                  pir.CreatedAt,
		UpdatedAt:                  pir.UpdatedAt,
	}, nil
}
