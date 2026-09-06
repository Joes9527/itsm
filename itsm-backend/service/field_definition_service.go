package service

import (
	"context"
	"fmt"
	"strings"

	"itsm-backend/ent"
	"itsm-backend/ent/fielddefinition"
)

// FieldDefinitionService 动态字段定义的共享服务，工单模板、服务目录项共用。
type FieldDefinitionService struct {
	client *ent.Client
}

func NewFieldDefinitionService(client *ent.Client) *FieldDefinitionService {
	return &FieldDefinitionService{client: client}
}

// FieldDefinitionInput 创建/替换字段定义时的输入。
type FieldDefinitionInput struct {
	Name      string
	Label     string
	FieldType string
	Required  bool
	Options   []interface{}
	SortOrder int
}

// ReplaceDefinitions preserves field identities by name and commits the complete form atomically.
func (s *FieldDefinitionService) ReplaceDefinitions(ctx context.Context, tenantID int, entityType string, entityID int, defs []FieldDefinitionInput) ([]*ent.FieldDefinition, error) {
	if entityType == "service_catalog" {
		return nil, fmt.Errorf("catalog definitions require the versioned catalog mutation")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}

	result, err := s.ReplaceDefinitionsTx(ctx, tx, tenantID, entityType, entityID, defs)
	if err != nil {
		return nil, rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// ReplaceDefinitionsTx joins the owning entity's atomic definition mutation.
func (s *FieldDefinitionService) ReplaceDefinitionsTx(ctx context.Context, tx *ent.Tx, tenantID int, entityType string, entityID int, defs []FieldDefinitionInput) ([]*ent.FieldDefinition, error) {
	existing, err := tx.FieldDefinition.Query().Where(fielddefinition.TenantIDEQ(tenantID), fielddefinition.EntityTypeEQ(entityType), fielddefinition.EntityIDEQ(entityID)).All(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]*ent.FieldDefinition{}
	for _, row := range existing {
		byName[row.Name] = row
	}
	names := []string{}
	seen := map[string]bool{}
	result := make([]*ent.FieldDefinition, 0, len(defs))
	for _, d := range defs {
		if strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Label) == "" || seen[d.Name] {
			return nil, fmt.Errorf("field name and label must be nonempty and names unique")
		}
		seen[d.Name] = true
		names = append(names, d.Name)
		switch d.FieldType {
		case "text", "textarea", "number", "date", "select", "multiselect", "boolean", "file":
		default:
			return nil, fmt.Errorf("unsupported field type %q", d.FieldType)
		}
		order := d.SortOrder
		options := d.Options
		if options == nil {
			options = []interface{}{}
		}
		var row *ent.FieldDefinition
		if old := byName[d.Name]; old != nil {
			row, err = tx.FieldDefinition.UpdateOneID(old.ID).SetLabel(d.Label).SetFieldType(d.FieldType).SetRequired(d.Required).SetOptions(options).SetSortOrder(order).SetIsActive(true).Save(ctx)
		} else {
			row, err = tx.FieldDefinition.Create().SetTenantID(tenantID).SetEntityType(entityType).SetEntityID(entityID).SetName(d.Name).SetLabel(d.Label).SetFieldType(d.FieldType).SetRequired(d.Required).SetOptions(options).SetSortOrder(order).Save(ctx)
		}
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	deletion := tx.FieldDefinition.Delete().Where(fielddefinition.TenantIDEQ(tenantID), fielddefinition.EntityTypeEQ(entityType), fielddefinition.EntityIDEQ(entityID))
	if len(names) > 0 {
		deletion = deletion.Where(fielddefinition.NameNotIn(names...))
	}
	if _, err := deletion.Exec(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

// ListDefinitions 按 sort_order 返回 (tenantID, entityType, entityID) 下的字段定义。
func (s *FieldDefinitionService) ListDefinitions(ctx context.Context, tenantID int, entityType string, entityID int) ([]*ent.FieldDefinition, error) {
	return s.client.FieldDefinition.Query().
		Where(
			fielddefinition.TenantID(tenantID),
			fielddefinition.EntityType(entityType),
			fielddefinition.EntityID(entityID),
			fielddefinition.IsActive(true),
		).
		Order(ent.Asc(fielddefinition.FieldSortOrder)).
		All(ctx)
}

// ListDefinitionsForEntities 批量按 entityID 分组返回字段定义，避免调用方对每个实体单独查一次
// （列表接口场景，同 ListDefinitions 的单实体版本相比省掉 N-1 次查询）。
func (s *FieldDefinitionService) ListDefinitionsForEntities(ctx context.Context, tenantID int, entityType string, entityIDs []int) (map[int][]*ent.FieldDefinition, error) {
	result := make(map[int][]*ent.FieldDefinition, len(entityIDs))
	if len(entityIDs) == 0 {
		return result, nil
	}

	defs, err := s.client.FieldDefinition.Query().
		Where(
			fielddefinition.TenantID(tenantID),
			fielddefinition.EntityType(entityType),
			fielddefinition.EntityIDIn(entityIDs...),
			fielddefinition.IsActive(true),
		).
		Order(ent.Asc(fielddefinition.FieldSortOrder)).
		All(ctx)
	if err != nil {
		return nil, err
	}

	for _, d := range defs {
		result[d.EntityID] = append(result[d.EntityID], d)
	}
	return result, nil
}

// DeleteDefinitions 删除 (tenantID, entityType, entityID) 下所有字段定义。
// 只在宿主实体本身也是硬删除时使用；如果宿主实体是软删除/可恢复的（比如状态置为
// disabled 但记录还在），应该用 DisableDefinitions，否则宿主"恢复"之后字段定义
// 已经永久丢了，恢复不回来。
func (s *FieldDefinitionService) DeleteDefinitions(ctx context.Context, tenantID int, entityType string, entityID int) error {
	if entityType == "service_catalog" {
		return fmt.Errorf("catalog definitions require the versioned catalog mutation")
	}
	_, err := s.client.FieldDefinition.Delete().
		Where(
			fielddefinition.TenantID(tenantID),
			fielddefinition.EntityType(entityType),
			fielddefinition.EntityID(entityID),
		).
		Exec(ctx)
	return err
}

// DisableDefinitions 把 (tenantID, entityType, entityID) 下所有字段定义标记为不活跃
// （is_active=false），不物理删除行。用于宿主实体是软删除的场景：ListDefinitions/
// ListDefinitionsForEntities 都过滤 is_active=true，效果上等同于"字段定义也消失了"，
// 但宿主实体恢复后，数据仍在，不会永久丢失管理员配置过的自定义字段。
func (s *FieldDefinitionService) DisableDefinitions(ctx context.Context, tenantID int, entityType string, entityID int) error {
	if entityType == "service_catalog" {
		return fmt.Errorf("catalog definitions require the versioned catalog mutation")
	}
	_, err := s.client.FieldDefinition.Update().
		Where(
			fielddefinition.TenantID(tenantID),
			fielddefinition.EntityType(entityType),
			fielddefinition.EntityID(entityID),
		).
		SetIsActive(false).
		Save(ctx)
	return err
}

// rollback aborts tx and wraps the rollback error (if any) around the original cause.
func rollback(tx *ent.Tx, cause error) error {
	if rbErr := tx.Rollback(); rbErr != nil {
		return fmt.Errorf("%w (rollback also failed: %v)", cause, rbErr)
	}
	return cause
}
