package service

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processversionchangelog"

	// "itsm-backend/ent/processdeployment" // 暂时不使用，因为ProcessDeployment没有ProcessDefinitionID字段
	"go.uber.org/zap"
)

// BPMNVersionService BPMN流程版本管理服务
type BPMNVersionService struct {
	client *ent.Client
	logger *zap.SugaredLogger
}

// NewBPMNVersionService 创建BPMN版本管理服务实例
func NewBPMNVersionService(client *ent.Client, logger *zap.SugaredLogger) *BPMNVersionService {
	return &BPMNVersionService{client: client, logger: logger}
}

// ProcessVersion 流程版本信息
type ProcessVersion struct {
	ID                   string    `json:"id"`
	ProcessDefinitionKey string    `json:"processDefinitionKey"`
	Version              string    `json:"version"`
	Name                 string    `json:"name"`
	Description          string    `json:"description"`
	BPMNXML              string    `json:"bpmnXml"`
	DeploymentID         string    `json:"deploymentId"`
	IsActive             bool      `json:"isActive"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
	CreatedBy            string    `json:"createdBy"`
	TenantID             int       `json:"tenantId"`
	ChangeLog            string    `json:"changeLog"`
	CompatibilityNotes   string    `json:"compatibilityNotes"`
}

// CreateVersionRequest 创建版本请求
type CreateVersionRequest struct {
	ProcessDefinitionKey string `json:"processDefinitionKey" binding:"required"`
	Name                 string `json:"name" binding:"required"`
	Description          string `json:"description"`
	BPMNXML              string `json:"bpmnXml" binding:"required"`
	ChangeLog            string `json:"changeLog"`
	CompatibilityNotes   string `json:"compatibilityNotes"`
	TenantID             int    `json:"-" form:"-"`
	CreatedBy            string `json:"-" form:"-"`
}

// UpdateVersionRequest 更新版本请求
type UpdateVersionRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	BPMNXML            string `json:"bpmnXml"`
	ChangeLog          string `json:"changeLog"`
	CompatibilityNotes string `json:"compatibilityNotes"`
}

// VersionComparison 版本比较结果
type VersionComparison struct {
	BaseVersion     *ProcessVersion `json:"baseVersion"`
	TargetVersion   *ProcessVersion `json:"targetVersion"`
	Changes         []ChangeDetail  `json:"changes"`
	BreakingChanges []string        `json:"breakingChanges"`
	Compatibility   string          `json:"compatibility"`
}

// ChangeDetail 变更详情
type ChangeDetail struct {
	Type        string `json:"type"`        // "added", "removed", "modified"
	ChangeType  string `json:"changeType"`  // 变更类型
	ElementType string `json:"elementType"` // "task", "gateway", "event", "flow"
	ElementID   string `json:"elementId"`
	ElementName string `json:"elementName"`
	Description string `json:"description"`
	Impact      string `json:"impact"`             // "low", "medium", "high", "critical"
	OldValue    string `json:"oldValue,omitempty"` // 旧值
	NewValue    string `json:"newValue,omitempty"` // 新值
}

func bpmnVersionTenant(ctx context.Context, requestedTenantID int) (int, error) {
	tenantID, err := bpmnAuthorizedTenantFromContext(ctx)
	if err != nil {
		return 0, err
	}
	if requestedTenantID > 0 && requestedTenantID != tenantID {
		return 0, common.NewForbiddenError("无权操作其他租户的 BPMN 版本")
	}
	return tenantID, nil
}

func bpmnVersionCreatedBy(ctx context.Context, trustedFallback string) string {
	if scope, err := BPMNAccessScopeFromContext(ctx); err == nil {
		return strconv.Itoa(scope.UserID)
	}
	return trustedFallback
}

// CreateVersion 创建新版本
func (s *BPMNVersionService) CreateVersion(ctx context.Context, req *CreateVersionRequest) (*ProcessVersion, error) {
	if req == nil {
		return nil, common.NewValidationError("版本请求不能为空", nil)
	}
	tenantID, err := bpmnVersionTenant(ctx, req.TenantID)
	if err != nil {
		return nil, err
	}
	createdBy := bpmnVersionCreatedBy(ctx, req.CreatedBy)
	// 获取当前最高版本号（语义化版本）
	currentVersion, err := s.getCurrentVersion(ctx, req.ProcessDefinitionKey, tenantID)
	if err != nil {
		return nil, fmt.Errorf("获取当前版本失败: %w", err)
	}

	newVersion := incrementSemver(currentVersion)

	// 降级旧版本 + 建部署记录 + 建流程定义必须在同一个事务里，三者要么一起成功、要么一起回滚。
	//
	// 非事务地先降级再创建有一个比原 bug 更糟的失败模式：任何一个 Create 失败时，
	// 旧行已经被改成 is_latest=false，新行又没建出来，该 key 就变成 0 行 is_latest=true；
	// GetLatestProcessDefinition 只按 IsLatest(true) 过滤，会直接查不到，
	// 该 key 的 StartProcess 全线挂掉，直到有人手工再建一个版本。
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("开启事务失败: %w", err)
	}
	txClient := tx.Client()

	// 把当前 is_latest=true 的旧版本全部降级——不这样做的话，每次 CreateVersion 都会
	// 让同一个 key 同时存在多行 is_latest=true（新行靠 schema 默认值天生是 true，
	// 旧行从来没人主动改成 false），GetLatestProcessDefinition/StartProcess 的
	// .First() 会取到不确定的一行。跟 bpmnProcessDefinitionService.CreateProcessDefinition
	// （service/bpmn_process_engine.go）已经写对的降级逻辑保持一致。
	if err := s.demoteCurrentLatest(ctx, txClient, req.ProcessDefinitionKey, tenantID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	// 先创建部署记录（因为ProcessDefinition需要deployment_id）
	deployment, err := txClient.ProcessDeployment.Create().
		SetDeploymentID(fmt.Sprintf("tenant-%d-%s-v%s", tenantID, req.ProcessDefinitionKey, newVersion)).
		SetDeploymentName(fmt.Sprintf("%s v%s", req.Name, newVersion)).
		SetDeploymentTime(time.Now()).
		SetTenantID(tenantID).
		SetDeployedBy(createdBy).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("创建部署记录失败: %w", err)
	}

	// 使用部署ID创建流程定义
	processDef, err := txClient.ProcessDefinition.Create().
		SetKey(req.ProcessDefinitionKey).
		SetName(req.Name).
		SetDescription(req.Description).
		SetBpmnXML([]byte(req.BPMNXML)).
		SetVersion(newVersion).
		SetTenantID(tenantID).
		SetIsActive(false).
		SetIsLatest(true).
		SetDeploymentID(deployment.ID).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("创建流程定义失败: %w", err)
	}
	changeLog := strings.TrimSpace(req.ChangeLog)
	if changeLog == "" {
		changeLog = fmt.Sprintf("创建流程版本 %s", newVersion)
	}
	if err := s.recordVersionChangeLog(ctx, txClient, processDef.ID, newVersion, changeLog, createdBy, tenantID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交事务失败: %w", err)
	}

	return &ProcessVersion{
		ID:                   fmt.Sprintf("%d", processDef.ID), // ID是int类型，转换为string
		ProcessDefinitionKey: processDef.Key,
		Version:              newVersion, // 语义化版本字符串
		Name:                 processDef.Name,
		Description:          processDef.Description,
		BPMNXML:              string(processDef.BpmnXML), // BPMNXML是[]byte类型，转换为string
		DeploymentID:         deployment.DeploymentID,    // 使用DeploymentID字段
		IsActive:             processDef.IsActive,
		CreatedAt:            processDef.CreatedAt,
		UpdatedAt:            processDef.UpdatedAt,
		CreatedBy:            createdBy,
		TenantID:             processDef.TenantID,
		ChangeLog:            req.ChangeLog,
		CompatibilityNotes:   req.CompatibilityNotes,
	}, nil
}

// demoteCurrentLatest 把某个 (tenant, key) 下所有 is_latest=true 的行一次性改成 false。
//
// 必须是批量 Update 而不是 Query().First() + UpdateOne：这个 bug 的前提就是生产数据里
// 同一个 key 可能已经并列存在多行 is_latest=true（历史上从没人降级过旧行）。只降级一行的话，
// CreateVersion 跑完还剩 N-1 行旧的 + 1 行新的，根本收敛不到"任何时刻恰好 1 行最新"。
//
// client 由调用方传入（CreateVersion 传的是事务里的 tx.Client()），保证降级与后续的
// 创建动作处在同一个事务边界内。没有旧版本时批量 Update 影响 0 行，天然幂等。
func (s *BPMNVersionService) demoteCurrentLatest(ctx context.Context, client *ent.Client, key string, tenantID int) error {
	demoted, err := client.ProcessDefinition.Update().
		Where(
			processdefinition.Key(key),
			processdefinition.TenantID(tenantID),
			processdefinition.IsLatest(true),
		).
		SetIsLatest(false).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("降级旧版本失败: %w", err)
	}
	if demoted > 1 {
		s.logger.Warnw("同一流程 key 存在多行 is_latest=true 的历史脏数据，已一并降级",
			"key", key, "tenantId", tenantID, "demoted", demoted)
	}
	return nil
}

// GetVersion 获取指定版本
func (s *BPMNVersionService) GetVersion(ctx context.Context, processKey string, version string, tenantID int) (*ProcessVersion, error) {
	tenantID, err := bpmnVersionTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	processDef, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(processKey),
			processdefinition.Version(version),
			processdefinition.TenantID(tenantID),
		).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义失败: %w", err)
	}

	return &ProcessVersion{
		ID:                   fmt.Sprintf("%d", processDef.ID), // ID是int类型，转换为string
		ProcessDefinitionKey: processDef.Key,
		Version:              processDef.Version,
		Name:                 processDef.Name,
		Description:          processDef.Description,
		BPMNXML:              string(processDef.BpmnXML), // BPMNXML是[]byte类型，转换为string
		DeploymentID:         "",                         // 暂时为空，因为无法查询部署信息
		IsActive:             processDef.IsActive,
		CreatedAt:            processDef.CreatedAt,
		UpdatedAt:            processDef.UpdatedAt,
		TenantID:             processDef.TenantID,
	}, nil
}

// ListVersions 获取流程的所有版本
func (s *BPMNVersionService) ListVersions(ctx context.Context, processKey string, tenantID int) ([]*ProcessVersion, error) {
	tenantID, err := bpmnVersionTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	processDefs, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(processKey),
			processdefinition.TenantID(tenantID),
		).
		Order(ent.Desc(processdefinition.FieldVersion)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取流程定义列表失败: %w", err)
	}

	var versions []*ProcessVersion
	for _, processDef := range processDefs {
		// ProcessDeployment没有ProcessDefinitionID字段，暂时跳过部署信息查询
		// 获取部署信息
		// deployment, err := s.client.ProcessDeployment.Query().
		// 	Where(processdeployment.ProcessDefinitionID(processDef.ID)).
		// 	First(ctx)
		// if err != nil {
		// 	// 跳过没有部署信息的版本
		// 	continue
		// }

		version := &ProcessVersion{
			ID:                   fmt.Sprintf("%d", processDef.ID), // ID是int类型，转换为string
			ProcessDefinitionKey: processDef.Key,
			Version:              processDef.Version,
			Name:                 processDef.Name,
			Description:          processDef.Description,
			BPMNXML:              string(processDef.BpmnXML), // BPMNXML是[]byte类型，转换为string
			DeploymentID:         "",                         // 暂时为空，因为无法查询部署信息
			IsActive:             processDef.IsActive,
			CreatedAt:            processDef.CreatedAt,
			UpdatedAt:            processDef.UpdatedAt,
			TenantID:             processDef.TenantID,
		}
		versions = append(versions, version)
	}

	return versions, nil
}

// ActivateVersion 激活指定版本
func (s *BPMNVersionService) ActivateVersion(ctx context.Context, processKey string, version string, tenantID int) (err error) {
	tenantID, err = bpmnVersionTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	// 开始事务
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("开始事务失败: %w", err)
	}
	defer func() {
		if v := recover(); v != nil {
			_ = tx.Rollback()
			err = fmt.Errorf("激活版本失败")
			s.logger.Errorw("Panic recovered in ActivateVersion", "error_class", "panic")
		}
	}()
	target, err := tx.ProcessDefinition.Query().Where(
		processdefinition.Key(processKey),
		processdefinition.Version(version),
		processdefinition.TenantID(tenantID),
	).Only(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("目标版本不存在")
	}

	// 停用所有版本
	_, err = tx.ProcessDefinition.Update().
		Where(
			processdefinition.Key(processKey),
			processdefinition.TenantID(tenantID),
		).
		SetIsActive(false).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("停用其他版本失败: %w", err)
	}

	// 激活指定版本 - Version是string类型
	_, err = tx.ProcessDefinition.Update().
		Where(
			processdefinition.Key(processKey),
			processdefinition.Version(version),
			processdefinition.TenantID(tenantID),
		).
		SetIsActive(true).
		Save(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("激活指定版本失败: %w", err)
	}
	if err := s.recordVersionChangeLog(
		ctx,
		tx.Client(),
		target.ID,
		version,
		fmt.Sprintf("激活流程版本 %s", version),
		bpmnVersionCreatedBy(ctx, ""),
		tenantID,
	); err != nil {
		_ = tx.Rollback()
		return err
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}

	return nil
}

// RollbackToVersion 回滚到指定版本
func (s *BPMNVersionService) RollbackToVersion(ctx context.Context, processKey string, targetVersion string, tenantID int, reason string) error {
	tenantID, err := bpmnVersionTenant(ctx, tenantID)
	if err != nil {
		return err
	}
	// 检查目标版本是否存在 - Version是string类型
	targetProcessDef, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(processKey),
			processdefinition.Version(targetVersion),
			processdefinition.TenantID(tenantID),
		).
		First(ctx)
	if err != nil {
		return fmt.Errorf("目标版本不存在: %w", err)
	}

	// 创建回滚版本 - BPMNXML是[]byte类型，转换为string
	rollbackReq := &CreateVersionRequest{
		ProcessDefinitionKey: processKey,
		Name:                 fmt.Sprintf("%s (回滚到 v%s)", targetProcessDef.Name, targetVersion),
		Description:          fmt.Sprintf("回滚到版本 %s，原因: %s", targetVersion, reason),
		BPMNXML:              string(targetProcessDef.BpmnXML), // 转换为string
		ChangeLog:            fmt.Sprintf("回滚到版本 %s，原因: %s", targetVersion, reason),
		CompatibilityNotes:   "回滚版本，可能存在兼容性问题",
		TenantID:             tenantID,
		CreatedBy:            "系统回滚",
	}

	rollbackVersion, err := s.CreateVersion(ctx, rollbackReq)
	if err != nil {
		return fmt.Errorf("创建回滚版本失败: %w", err)
	}

	// 激活回滚版本
	if err := s.ActivateVersion(ctx, processKey, rollbackVersion.Version, tenantID); err != nil {
		return fmt.Errorf("激活回滚版本失败: %w", err)
	}

	return nil
}

// CompareVersions 比较两个版本
func (s *BPMNVersionService) CompareVersions(ctx context.Context, processKey string, baseVersion, targetVersion string, tenantID int) (*VersionComparison, error) {
	tenantID, err := bpmnVersionTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	// 获取基础版本
	baseProcessDef, err := s.GetVersion(ctx, processKey, baseVersion, tenantID)
	if err != nil {
		return nil, fmt.Errorf("获取基础版本失败: %w", err)
	}

	// 获取目标版本
	targetProcessDef, err := s.GetVersion(ctx, processKey, targetVersion, tenantID)
	if err != nil {
		return nil, fmt.Errorf("获取目标版本失败: %w", err)
	}

	// 比较BPMN XML内容
	changes, breakingChanges := s.compareBPMNXML(baseProcessDef.BPMNXML, targetProcessDef.BPMNXML)

	// 评估兼容性
	compatibility := s.assessCompatibility(changes, breakingChanges)

	return &VersionComparison{
		BaseVersion:     baseProcessDef,
		TargetVersion:   targetProcessDef,
		Changes:         changes,
		BreakingChanges: breakingChanges,
		Compatibility:   compatibility,
	}, nil
}

// getCurrentVersion 获取当前最高版本号
func (s *BPMNVersionService) getCurrentVersion(ctx context.Context, processKey string, tenantID int) (string, error) {
	processDef, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(processKey),
			processdefinition.TenantID(tenantID),
		).
		Order(ent.Desc(processdefinition.FieldCreatedAt)). // 按创建时间降序，避免字符串排序问题
		First(ctx)
	if err != nil {
		return "1.0.0", nil // 无已有定义时返回默认版本
	}
	if processDef.Version == "" {
		return "1.0.0", nil
	}
	return processDef.Version, nil
}

// parseSemver 解析 major.minor.patch，返回各部分
func parseSemver(v string) (major, minor, patch int, ok bool) {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err1, err2, err3 error
	major, err1 = strconv.Atoi(parts[0])
	minor, err2 = strconv.Atoi(parts[1])
	patch, err3 = strconv.Atoi(parts[2])
	return major, minor, patch, err1 == nil && err2 == nil && err3 == nil
}

// incrementSemver 递增 minor 版本号，重置 patch
func incrementSemver(current string) string {
	major, minor, _, ok := parseSemver(current)
	if !ok {
		return "1.0.0"
	}
	return fmt.Sprintf("%d.%d.0", major, minor+1)
}

// recordVersionChangeLog 记录版本变更日志。client 必须与业务变更使用同一事务。
func (s *BPMNVersionService) recordVersionChangeLog(ctx context.Context, client *ent.Client, processDefID int, version string, changeLog, createdBy string, tenantID int) error {
	// 解析 createdBy 为 int (用户 ID)
	createdByInt, err := strconv.Atoi(createdBy)
	if err != nil {
		createdByInt = 0
	}

	create := client.ProcessVersionChangelog.Create().
		SetProcessDefinitionID(processDefID).
		SetVersion(version).
		SetChangeLog(changeLog).
		SetChangeType("update").
		SetTenantID(tenantID)
	if createdByInt > 0 {
		create.SetCreatedBy(createdByInt)
	}
	_, err = create.Save(ctx)
	if err != nil {
		s.logger.Errorw("Failed to create version changelog", "process_def_id", processDefID, "tenant_id", tenantID, "error_class", "audit_write")
		return fmt.Errorf("创建版本变更日志失败：%w", err)
	}

	s.logger.Infow("Version change logged successfully", "process_def_id", processDefID, "tenant_id", tenantID)
	return nil
}

// compareBPMNXML 比较BPMN XML内容
func (s *BPMNVersionService) compareBPMNXML(baseXML, targetXML string) ([]ChangeDetail, []string) {
	// 简单比较：检查XML长度是否变化
	baseLen := len(baseXML)
	targetLen := len(targetXML)

	if baseLen == targetLen && baseXML == targetXML {
		// 完全相同
		return []ChangeDetail{}, []string{}
	}

	var changes []ChangeDetail

	// 解析基础XML
	type Process struct {
		ID    string `xml:"id,attr"`
		Name  string `xml:"name,attr"`
		Tasks []struct {
			ID   string `xml:"id,attr"`
			Name string `xml:"name,attr"`
		} `xml:"process>userTask"`
	}
	type Definitions struct {
		Process Process `xml:"process"`
	}

	var baseDef Definitions
	if err := xml.Unmarshal([]byte(baseXML), &baseDef); err == nil {
		var targetDef Definitions
		if err := xml.Unmarshal([]byte(targetXML), &targetDef); err == nil {
			// 比较Task数量
			if len(baseDef.Process.Tasks) != len(targetDef.Process.Tasks) {
				changes = append(changes, ChangeDetail{
					Type:        "modified",
					ChangeType:  "structure",
					ElementType: "process",
					ElementID:   baseDef.Process.ID,
					ElementName: baseDef.Process.Name,
					Description: fmt.Sprintf("Task count changed from %d to %d", len(baseDef.Process.Tasks), len(targetDef.Process.Tasks)),
					Impact:      "high",
				})
			}
		}
	}

	// 如果无法详细解析或没有发现特定差异但内容不同，添加通用变更记录
	if len(changes) == 0 {
		changeDetail := ChangeDetail{
			ElementID:   "root",
			ElementType: "process",
			ChangeType:  "modified",
			OldValue:    fmt.Sprintf("length:%d", baseLen),
			NewValue:    fmt.Sprintf("length:%d", targetLen),
			Description: "BPMN XML content has changed",
			Impact:      "medium",
		}
		changes = append(changes, changeDetail)
	}

	return changes, []string{}
}

// assessCompatibility 评估兼容性
func (s *BPMNVersionService) assessCompatibility(changes []ChangeDetail, breakingChanges []string) string {
	if len(breakingChanges) > 0 {
		return "incompatible"
	}

	if len(changes) == 0 {
		return "identical"
	}

	// 检查是否有高风险变更
	for _, change := range changes {
		if change.Impact == "critical" || change.Impact == "high" {
			return "risky"
		}
	}

	return "compatible"
}

// GetChangeLogsByProcessKey 根据流程定义key获取版本变更日志列表
func (s *BPMNVersionService) GetChangeLogsByProcessKey(ctx context.Context, processKey string, tenantID int) ([]*ent.ProcessVersionChangelog, error) {
	tenantID, err := bpmnVersionTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	// 先通过 processdefinition 找到对应的 ID
	pd, err := s.client.ProcessDefinition.Query().
		Where(processdefinition.Key(processKey), processdefinition.TenantID(tenantID)).
		First(ctx)
	if err != nil {
		return nil, fmt.Errorf("流程定义不存在: %w", err)
	}

	// 获取 changelogs
	changelogs, err := s.client.ProcessVersionChangelog.Query().
		Where(processversionchangelog.ProcessDefinitionIDEQ(pd.ID), processversionchangelog.TenantIDEQ(tenantID)).
		Order(ent.Desc(processversionchangelog.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取变更日志失败: %w", err)
	}

	return changelogs, nil
}

// GetChangeLogsByProcessDefinitionID 根据流程定义ID获取版本变更日志
func (s *BPMNVersionService) GetChangeLogsByProcessDefinitionID(ctx context.Context, processDefID int) ([]*ent.ProcessVersionChangelog, error) {
	tenantID, err := bpmnVersionTenant(ctx, 0)
	if err != nil {
		return nil, err
	}
	if _, err := s.client.ProcessDefinition.Query().Where(
		processdefinition.ID(processDefID), processdefinition.TenantID(tenantID),
	).Only(ctx); err != nil {
		return nil, fmt.Errorf("流程定义不存在")
	}
	changelogs, err := s.client.ProcessVersionChangelog.Query().
		Where(processversionchangelog.ProcessDefinitionIDEQ(processDefID), processversionchangelog.TenantIDEQ(tenantID)).
		Order(ent.Desc(processversionchangelog.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取变更日志失败: %w", err)
	}

	return changelogs, nil
}
