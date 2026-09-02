package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/fieldvalue"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFieldValueService_CreateAndListValues(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_create?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 4, []FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text", SortOrder: 0},
		{Name: "device_count", Label: "设备数量", FieldType: "number", SortOrder: 1},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 4, "ticket", 100, map[string]interface{}{
		"office_location": "北京",
		"device_count":    float64(2),
		"unknown_field":   "should be ignored",
	})
	require.NoError(t, err)

	values, err := valSvc.ListValues(ctx, 1, "ticket", 100)
	require.NoError(t, err)
	require.Len(t, values, 2) // unknown_field 被忽略，不匹配任何定义
	assert.Equal(t, "office_location", values[0].Name)
	assert.Equal(t, "办公地点", values[0].Label)
	assert.Equal(t, "北京", values[0].Value)
	assert.Equal(t, "device_count", values[1].Name)
	assert.Equal(t, float64(2), values[1].Value)
}

func TestFieldValueService_ListValues_EmptyWhenNoValues(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_empty?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	valSvc := NewFieldValueService(client)

	values, err := valSvc.ListValues(ctx, 1, "ticket", 999)
	require.NoError(t, err)
	assert.Empty(t, values)
}

func TestFieldValueService_CreateAdHocValues_NoFieldDefinitionRequired(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_adhoc?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	valSvc := NewFieldValueService(client)

	// 没有任何 field_definitions 行——静态预设场景。
	err := valSvc.CreateAdHocValues(ctx, 1, "ticket", 200, []AdHocFieldValue{
		{Name: "replicas", Label: "副本数", SortOrder: 0, Value: float64(3)},
	})
	require.NoError(t, err)

	values, err := valSvc.ListValues(ctx, 1, "ticket", 200)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "replicas", values[0].Name)
	assert.Equal(t, "副本数", values[0].Label)
	assert.Equal(t, float64(3), values[0].Value)
}

func TestFieldValueService_CreateValues_SurvivesDefinitionDeletion(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_survive?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 4, []FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	})
	require.NoError(t, err)
	require.NoError(t, valSvc.CreateValues(ctx, 1, "ticket_template", 4, "ticket", 100, map[string]interface{}{
		"office_location": "北京",
	}))

	// 模板字段定义被删除/改名后（这里模拟改名：Replace 成一个新 name）
	_, err = defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 4, []FieldDefinitionInput{
		{Name: "office_location_v2", Label: "办公地点(新)", FieldType: "text"},
	})
	require.NoError(t, err)

	// 老工单的历史值展示不受影响
	values, err := valSvc.ListValues(ctx, 1, "ticket", 100)
	require.NoError(t, err)
	require.Len(t, values, 1)
	assert.Equal(t, "office_location", values[0].Name)
	assert.Equal(t, "办公地点", values[0].Label)
	assert.Equal(t, "北京", values[0].Value)
}

func TestFieldValueService_CreateValues_RejectsNonNumericValueForNumberField(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_reject_number?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 40, []FieldDefinitionInput{
		{Name: "device_count", Label: "设备数量", FieldType: "number", SortOrder: 0},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 40, "ticket", 400, map[string]interface{}{
		"device_count": "not-a-number",
	})
	require.Error(t, err)

	count, err := client.FieldValue.Query().Where(fieldvalue.EntityID(400)).Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "校验失败时不应该留下部分写入的值——事务应该整体回滚")
}

func TestFieldValueService_CreateValues_RejectsSelectValueNotInOptions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_reject_select?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 41, []FieldDefinitionInput{
		{
			Name: "priority_level", Label: "优先级", FieldType: "select", SortOrder: 0,
			Options: []interface{}{
				map[string]interface{}{"label": "低", "value": "low"},
				map[string]interface{}{"label": "高", "value": "high"},
			},
		},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 41, "ticket", 401, map[string]interface{}{
		"priority_level": "urgent",
	})
	require.Error(t, err)
}

func TestFieldValueService_CreateValues_AcceptsValidNumberAndSelectValues(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_accept_valid?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	_, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 42, []FieldDefinitionInput{
		{Name: "device_count", Label: "设备数量", FieldType: "number", SortOrder: 0},
		{
			Name: "priority_level", Label: "优先级", FieldType: "select", SortOrder: 1,
			Options: []interface{}{map[string]interface{}{"label": "低", "value": "low"}},
		},
	})
	require.NoError(t, err)

	err = valSvc.CreateValues(ctx, 1, "ticket_template", 42, "ticket", 402, map[string]interface{}{
		"device_count":   3,
		"priority_level": "low",
	})
	require.NoError(t, err)
}

func TestFieldValueService_CreateValues_SkipsDeactivatedFieldDefinition(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:field_value_skip_inactive?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	defSvc := NewFieldDefinitionService(client)
	valSvc := NewFieldValueService(client)

	defs, err := defSvc.ReplaceDefinitions(ctx, 1, "ticket_template", 43, []FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text", SortOrder: 0},
		{Name: "legacy_field", Label: "旧字段", FieldType: "text", SortOrder: 1},
	})
	require.NoError(t, err)
	var legacyID int
	for _, d := range defs {
		if d.Name == "legacy_field" {
			legacyID = d.ID
		}
	}
	require.NotZero(t, legacyID, "fixture must have created the legacy_field definition")
	_, err = client.FieldDefinition.UpdateOneID(legacyID).SetIsActive(false).Save(ctx)
	require.NoError(t, err)

	// legacy_field 是 is_active=false 的旧字段——即便调用方在提交里带了它的值，
	// createFieldValues 也不应该写入，因为它已经不在 (tenantID, entityType, entityID)
	// 的当前活跃字段定义集合里了。这是对 CreateValuesTx 抽出 createFieldValues 时
	// 意外丢失的 IsActive 过滤条件的回归测试。
	err = valSvc.CreateValues(ctx, 1, "ticket_template", 43, "ticket", 403, map[string]interface{}{
		"office_location": "上海",
		"legacy_field":    "should not be written",
	})
	require.NoError(t, err)

	values, err := valSvc.ListValues(ctx, 1, "ticket", 403)
	require.NoError(t, err)
	require.Len(t, values, 1, "deactivated field definition must not receive a written value")
	assert.Equal(t, "office_location", values[0].Name)
	assert.Equal(t, "上海", values[0].Value)
}
