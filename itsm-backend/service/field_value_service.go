package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"itsm-backend/ent"
	"itsm-backend/ent/fielddefinition"
	"itsm-backend/ent/fieldvalue"
)

// FieldValueService 动态字段值的共享服务，工单先接入，服务请求以后接入。
type FieldValueService struct {
	client *ent.Client
}

func NewFieldValueService(client *ent.Client) *FieldValueService {
	return &FieldValueService{client: client}
}

// ValidateFieldValue 按字段定义的 field_type 做最基本的格式/成员校验。只处理有明确
// 判定标准的类型（number 是不是数字、select/multiselect 的值在不在 options 里）；
// text/textarea/date/boolean/file 目前没有额外格式约束，跳过。
func ValidateFieldValue(def *ent.FieldDefinition, raw interface{}) error {
	switch def.FieldType {
	case "number":
		switch raw.(type) {
		case float64, int, int64, json.Number:
			return nil
		case string:
			if _, err := strconv.ParseFloat(raw.(string), 64); err == nil {
				return nil
			}
		}
		return fmt.Errorf("字段 %q 需要数字类型的值", def.Label)
	case "select":
		if len(def.Options) == 0 {
			return nil
		}
		valueStr := fmt.Sprintf("%v", raw)
		for _, opt := range def.Options {
			if optMap, ok := opt.(map[string]interface{}); ok {
				if fmt.Sprintf("%v", optMap["value"]) == valueStr {
					return nil
				}
			}
		}
		return fmt.Errorf("字段 %q 的值不在允许的选项范围内", def.Label)
	case "multiselect":
		if len(def.Options) == 0 {
			return nil
		}
		values, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("字段 %q 需要数组类型的值", def.Label)
		}
		allowed := make(map[string]struct{}, len(def.Options))
		for _, opt := range def.Options {
			if optMap, ok := opt.(map[string]interface{}); ok {
				allowed[fmt.Sprintf("%v", optMap["value"])] = struct{}{}
			}
		}
		for _, v := range values {
			if _, ok := allowed[fmt.Sprintf("%v", v)]; !ok {
				return fmt.Errorf("字段 %q 包含不在允许范围内的值: %v", def.Label, v)
			}
		}
		return nil
	default:
		return nil
	}
}

func validateFieldValue(def *ent.FieldDefinition, raw interface{}) error {
	return ValidateFieldValue(def, raw)
}

// CreateValues 把提交的 values（fieldName -> 原始值）跟 (defEntityType, defEntityID) 下的
// 字段定义匹配，快照 name/label/顺序后写入 field_values，挂在 (valueEntityType, valueEntityID) 上。
// values 里不匹配任何字段定义的 key 会被忽略（例如 presetTypeId 这类路由元数据）。
// 多条 insert 包在一个事务里：中途某一条失败（比如瞬时 DB 错误）不应该留下"插了一半"的
// 半成品提交记录——field_values 代表的是一次完整的表单提交，要么整体成功要么整体不落库。
func (s *FieldValueService) CreateValues(ctx context.Context, tenantID int, defEntityType string, defEntityID int, valueEntityType string, valueEntityID int, values map[string]interface{}) error {
	return s.CreateValuesTx(ctx, nil, tenantID, defEntityType, defEntityID, valueEntityType, valueEntityID, values)
}

// CreateValuesTx writes dynamic field values through the caller's transaction
// when supplied. A nil transaction preserves the standalone CreateValues
// contract by opening and owning one transaction for the complete submission.
func (s *FieldValueService) CreateValuesTx(
	ctx context.Context,
	tx *ent.Tx,
	tenantID int,
	defEntityType string,
	defEntityID int,
	valueEntityType string,
	valueEntityID int,
	values map[string]any,
) error {
	if len(values) == 0 {
		return nil
	}
	if tx != nil {
		return createFieldValues(ctx, tx.Client(), tenantID, defEntityType, defEntityID, valueEntityType, valueEntityID, values)
	}

	ownedTx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := createFieldValues(ctx, ownedTx.Client(), tenantID, defEntityType, defEntityID, valueEntityType, valueEntityID, values); err != nil {
		return rollback(ownedTx, err)
	}
	return ownedTx.Commit()
}

func createFieldValues(
	ctx context.Context,
	client *ent.Client,
	tenantID int,
	defEntityType string,
	defEntityID int,
	valueEntityType string,
	valueEntityID int,
	values map[string]any,
) error {
	defs, err := client.FieldDefinition.Query().
		Where(
			fielddefinition.TenantID(tenantID),
			fielddefinition.EntityType(defEntityType),
			fielddefinition.EntityID(defEntityID),
			fielddefinition.IsActive(true),
		).
		Order(ent.Asc(fielddefinition.FieldSortOrder)).
		All(ctx)
	if err != nil {
		return err
	}

	for _, def := range defs {
		raw, ok := values[def.Name]
		if !ok {
			continue
		}
		if err := validateFieldValue(def, raw); err != nil {
			return err
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		defID := def.ID
		_, err = client.FieldValue.Create().
			SetTenantID(tenantID).
			SetEntityType(valueEntityType).
			SetEntityID(valueEntityID).
			SetFieldDefinitionID(defID).
			SetFieldName(def.Name).
			SetFieldLabel(def.Label).
			SetSortOrder(def.SortOrder).
			SetValue(encoded).
			Save(ctx)
		if err != nil {
			return err
		}
	}
	return nil
}

// AdHocFieldValue 是没有对应 field_definitions 行的自描述字段值——
// 用于前端静态预设（代码里写死、不对应数据库模板）提交自定义字段的场景。
type AdHocFieldValue struct {
	Name      string
	Label     string
	SortOrder int
	Value     interface{}
}

// CreateAdHocValues 直接按调用方提供的 name/label 写入 field_values，跳过
// CreateValues 那种"先查 field_definitions 再匹配"的步骤——静态预设没有
// field_definitions 行可以匹配。
func (s *FieldValueService) CreateAdHocValues(ctx context.Context, tenantID int, valueEntityType string, valueEntityID int, fields []AdHocFieldValue) error {
	if len(fields) == 0 {
		return nil
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	for _, f := range fields {
		encoded, err := json.Marshal(f.Value)
		if err != nil {
			return rollback(tx, err)
		}
		_, err = tx.FieldValue.Create().
			SetTenantID(tenantID).
			SetEntityType(valueEntityType).
			SetEntityID(valueEntityID).
			SetFieldName(f.Name).
			SetFieldLabel(f.Label).
			SetSortOrder(f.SortOrder).
			SetValue(encoded).
			Save(ctx)
		if err != nil {
			return rollback(tx, err)
		}
	}
	return tx.Commit()
}

// FieldValueDTO 展示用的已解析字段值。
type FieldValueDTO struct {
	Name  string      `json:"name"`
	Label string      `json:"label"`
	Value interface{} `json:"value"`
}

// ListValues 按提交时快照的顺序返回 (tenantID, entityType, entityID) 的字段值。
func (s *FieldValueService) ListValues(ctx context.Context, tenantID int, entityType string, entityID int) ([]FieldValueDTO, error) {
	rows, err := s.client.FieldValue.Query().
		Where(
			fieldvalue.TenantID(tenantID),
			fieldvalue.EntityType(entityType),
			fieldvalue.EntityID(entityID),
		).
		Order(ent.Asc(fieldvalue.FieldSortOrder)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]FieldValueDTO, 0, len(rows))
	for _, row := range rows {
		var value interface{}
		if len(row.Value) > 0 {
			if err := json.Unmarshal(row.Value, &value); err != nil {
				continue // 损坏的值跳过展示，不阻塞整个响应
			}
		}
		result = append(result, FieldValueDTO{
			Name:  row.FieldName,
			Label: row.FieldLabel,
			Value: value,
		})
	}
	return result, nil
}
