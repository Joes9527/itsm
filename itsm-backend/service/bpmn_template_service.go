package service

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/ent/processdeployment"

	"github.com/pkg/errors"
	"go.uber.org/zap"
)

//go:embed bpmn/*.bpmn
var bpmnTemplates embed.FS

// BPMNTemplateService BPMN模板服务
type BPMNTemplateService struct {
	client *ent.Client
}

// NewBPMNTemplateService 创建BPMN模板服务
func NewBPMNTemplateService(client *ent.Client) *BPMNTemplateService {
	return &BPMNTemplateService{client: client}
}

// TemplateInfo 模板信息
type TemplateInfo struct {
	ID          string
	Name        string
	Category    string
	SubCategory string
	Version     string
	Description string
	Filename    string
}

// LoadAndDeployTemplates 加载并同步所有内置模板：
//   - 未部署 → 部署 v1；
//   - 已部署但最新版本内容与嵌入模板不一致（模板修复随新版本发布）→ 自动发布新版本
//     （事务化降级 is_latest），并停用旧版本、激活新版本；
//   - 内容一致 → 跳过。
//
// 这样内置模板的修复不再要求手工导入或重置数据库——存量租户在下次启动时自动
// 升级到新模板，同时保证同一 key 恰好一行 is_latest=true + is_active=true。
func (s *BPMNTemplateService) LoadAndDeployTemplates(ctx context.Context, tenantID int) ([]*TemplateInfo, error) {
	templates, err := s.listTemplates()
	if err != nil {
		return nil, errors.Wrap(err, "列出模板失败")
	}

	deployed := make([]*TemplateInfo, 0, len(templates))

	for _, tmpl := range templates {
		if err := s.syncTemplate(ctx, tmpl, tenantID); err != nil {
			return nil, fmt.Errorf("同步模板 %s 失败: %w", tmpl.Name, err)
		}
		deployed = append(deployed, tmpl)
	}

	return deployed, nil
}

// syncTemplate 同步单个内置模板（部署/升级/跳过），见 LoadAndDeployTemplates 注释。
func (s *BPMNTemplateService) syncTemplate(ctx context.Context, tmpl *TemplateInfo, tenantID int) error {
	data, err := bpmnTemplates.ReadFile(filepath.Join("bpmn", tmpl.Filename))
	if err != nil {
		return errors.Wrap(err, "读取模板文件失败")
	}

	// 取当前"最新"版本（is_latest 优先、id 兜底，与 StartProcess 的查找口径一致）
	latest, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(tmpl.ID),
			processdefinition.TenantID(tenantID),
		).
		Order(ent.Desc(processdefinition.FieldIsLatest), ent.Desc(processdefinition.FieldID)).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			return errors.Wrap(err, "查询已部署模板失败")
		}
		return s.deployTemplate(ctx, tmpl, tenantID, data)
	}

	if bytes.Equal(latest.BpmnXML, data) {
		// 内容一致，无需动作
		return nil
	}

	// 内容漂移：走版本服务发布新版本（内部事务化：批量降级 is_latest + 建部署 +
	// 建定义）。复用版本服务而不是就地改写旧定义——运行中的实例仍引用旧版本，
	// 就地改写会破坏运行中实例与已部署图的不可变契约。
	versionSvc := NewBPMNVersionService(s.client, zap.NewNop().Sugar())
	if _, err := versionSvc.CreateVersion(ctx, &CreateVersionRequest{
		ProcessDefinitionKey: tmpl.ID,
		TenantID:             tenantID,
		Name:                 tmpl.Name,
		Description:          tmpl.Description,
		BPMNXML:              string(data),
		ChangeLog:            "内置模板升级（随版本自动同步）",
		CreatedBy:            "system",
	}); err != nil {
		return errors.Wrap(err, "发布模板新版本失败")
	}

	// CreateVersion 里新版本是 IsActive=false。停用旧版本后激活最新版本，
	// 使 StartProcess（IsLatest+IsActive）与 trigger 的定义查询（仅 IsActive）
	// 都唯一命中新版本。
	if _, err := s.client.ProcessDefinition.Update().
		Where(
			processdefinition.Key(tmpl.ID),
			processdefinition.TenantID(tenantID),
		).
		SetIsActive(false).
		Save(ctx); err != nil {
		return errors.Wrap(err, "停用旧模板版本失败")
	}
	if _, err := s.client.ProcessDefinition.Update().
		Where(
			processdefinition.Key(tmpl.ID),
			processdefinition.TenantID(tenantID),
			processdefinition.IsLatest(true),
		).
		SetIsActive(true).
		Save(ctx); err != nil {
		return errors.Wrap(err, "激活新模板版本失败")
	}

	return nil
}

// listTemplates 列出所有内置模板
func (s *BPMNTemplateService) listTemplates() ([]*TemplateInfo, error) {
	templates := make([]*TemplateInfo, 0)

	// 读取嵌入的模板文件
	err := fs.WalkDir(bpmnTemplates, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".bpmn") {
			return nil
		}

		// 解析文件名获取模板信息
		filename := filepath.Base(path)
		key := strings.TrimSuffix(filename, ".bpmn")

		info := &TemplateInfo{
			ID:       key,
			Filename: filename,
		}

		// 根据文件名设置默认值
		switch key {
		case "ticket_general_flow":
			info.Name = "通用工单流程"
			info.Category = "ticket"
			info.Description = "通用工单处理流程"
		case "ticket_urgent_flow":
			info.Name = "紧急工单流程"
			info.Category = "ticket"
			info.Description = "高/紧急优先级工单处理流程（结构与通用工单流程等价，暂无独立超时/升级差异）"
		case "ticket_assignment_flow":
			info.Name = "工单分配流程"
			info.Category = "ticket"
			info.SubCategory = "assignment"
			info.Description = "工单自动分配处理流程"
		case "change_normal_flow":
			info.Name = "普通变更流程"
			info.Category = "change"
			info.SubCategory = "normal"
			info.Description = "普通变更管理流程"
		case "change_emergency_flow":
			info.Name = "紧急变更流程"
			info.Category = "change"
			info.SubCategory = "emergency"
			info.Description = "紧急变更快速处理流程"
		case "incident_emergency_flow":
			info.Name = "紧急事件流程"
			info.Category = "incident"
			info.Description = "紧急事件快速响应流程"
		case "service_request_flow":
			info.Name = "服务请求流程"
			info.Category = "service_request"
			info.Description = "标准服务请求处理流程"
		case "service_request_urgent_flow":
			info.Name = "紧急服务请求流程"
			info.Category = "service_request"
			info.Description = "高优先级服务请求处理流程（结构与标准服务请求流程等价，暂无独立超时/升级差异）"
		case "problem_management_flow":
			info.Name = "问题管理流程"
			info.Category = "problem"
			info.Description = "问题管理全流程"
		case "release_approval_flow":
			info.Name = "发布审批流程"
			info.Category = "release"
			info.Description = "软件发布审批管理流程"
		default:
			info.Name = key
			info.Category = "default"
		}

		info.Version = "1.0.0"
		templates = append(templates, info)
		return nil
	})
	if err != nil {
		return nil, errors.Wrap(err, "遍历模板目录失败")
	}

	return templates, nil
}

// deployTemplate 部署单个模板（首次部署 v1）
func (s *BPMNTemplateService) deployTemplate(ctx context.Context, tmpl *TemplateInfo, tenantID int, data []byte) error {
	now := time.Now()
	deploymentID := fmt.Sprintf("%s-v1", tmpl.ID)

	// 幂等：检查部署记录是否已存在
	existing, _ := s.client.ProcessDeployment.Query().
		Where(processdeployment.DeploymentID(deploymentID)).
		First(ctx)

	var deployment *ent.ProcessDeployment
	var err error
	if existing != nil {
		deployment = existing
	} else {
		deployment, err = s.client.ProcessDeployment.Create().
			SetDeploymentID(deploymentID).
			SetDeploymentName(fmt.Sprintf("%s v1", tmpl.Name)).
			SetDeploymentTime(now).
			SetTenantID(tenantID).
			SetDeployedBy("system").
			Save(ctx)
		if err != nil {
			return errors.Wrap(err, "创建部署记录失败")
		}
	}

	// 创建流程定义（关联部署ID）
	_, err = s.client.ProcessDefinition.Create().
		SetKey(tmpl.ID).
		SetName(tmpl.Name).
		SetDescription(tmpl.Description).
		SetVersion(tmpl.Version).
		SetCategory(tmpl.Category).
		SetBpmnXML(data).
		SetIsActive(true).
		SetIsLatest(true).
		SetTenantID(tenantID).
		SetDeploymentID(deployment.ID).
		SetDeployedAt(now).
		Save(ctx)
	if err != nil {
		return errors.Wrap(err, "保存流程定义失败")
	}

	return nil
}

// GetTemplateList 获取模板列表（不部署）
func (s *BPMNTemplateService) GetTemplateList() ([]*TemplateInfo, error) {
	return s.listTemplates()
}

// DeployTemplateByName 根据名称部署单个模板
func (s *BPMNTemplateService) DeployTemplateByName(ctx context.Context, name string, tenantID int) error {
	templates, err := s.listTemplates()
	if err != nil {
		return err
	}

	for _, tmpl := range templates {
		if tmpl.ID == name {
			// 与 LoadAndDeployTemplates 同一语义：未部署则部署 v1，内容漂移则升级
			return s.syncTemplate(ctx, tmpl, tenantID)
		}
	}

	return fmt.Errorf("模板 %s 不存在", name)
}

// GetTemplateContent 获取模板内容
func (s *BPMNTemplateService) GetTemplateContent(name string) ([]byte, error) {
	path := filepath.Join("bpmn", name+".bpmn")
	return bpmnTemplates.ReadFile(path)
}

// ExportTemplateToFile 将已部署的流程导出为BPMN文件
func (s *BPMNTemplateService) ExportTemplateToFile(ctx context.Context, key string, tenantID int, outputPath string) error {
	definition, err := s.client.ProcessDefinition.Query().
		Where(
			processdefinition.Key(key),
			processdefinition.TenantID(tenantID),
		).
		Only(ctx)
	if err != nil {
		return errors.Wrap(err, "查询流程定义失败")
	}

	// 写入文件
	return os.WriteFile(outputPath, definition.BpmnXML, 0o644)
}
