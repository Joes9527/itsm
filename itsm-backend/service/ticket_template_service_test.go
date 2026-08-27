package service

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTicketTemplateService_CreateTemplate_WritesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)

	fieldSvc := NewFieldDefinitionService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name:     "网络接入申请",
		Category: "网络",
		TenantID: 1,
		Fields: []FieldDefinitionInput{
			{Name: "office_location", Label: "办公地点", FieldType: "text", Required: true, SortOrder: 0},
			{Name: "device_count", Label: "设备数量", FieldType: "number", SortOrder: 1},
		},
	})
	require.NoError(t, err)

	defs, err := fieldSvc.ListDefinitions(ctx, 1, "ticket_template", template.ID)
	require.NoError(t, err)
	require.Len(t, defs, 2)
	assert.Equal(t, "office_location", defs[0].Name)
	assert.Equal(t, "device_count", defs[1].Name)
}

func TestTicketTemplateService_UpdateTemplate_ReplacesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_update_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)
	fieldSvc := NewFieldDefinitionService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name: "模板", Category: "cat", TenantID: 1,
		Fields: []FieldDefinitionInput{{Name: "a", Label: "A", FieldType: "text"}},
	})
	require.NoError(t, err)

	_, err = svc.UpdateTemplate(ctx, template.ID, &UpdateTemplateRequest{
		Fields: []FieldDefinitionInput{{Name: "b", Label: "B", FieldType: "text"}},
	}, 1)
	require.NoError(t, err)

	defs, err := fieldSvc.ListDefinitions(ctx, 1, "ticket_template", template.ID)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, "b", defs[0].Name)
}

func TestTicketTemplateService_UpdateTemplate_NilFieldsPreservesExisting(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_nil_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)
	fieldSvc := NewFieldDefinitionService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name: "模板", Category: "cat", TenantID: 1,
		Fields: []FieldDefinitionInput{{Name: "a", Label: "A", FieldType: "text"}},
	})
	require.NoError(t, err)

	// 只改状态，不碰 Fields——Fields 是 nil，应该被当成"不修改"，不是"清空"。
	isActive := false
	_, err = svc.UpdateTemplate(ctx, template.ID, &UpdateTemplateRequest{
		IsActive: &isActive,
	}, 1)
	require.NoError(t, err)

	defs, err := fieldSvc.ListDefinitions(ctx, 1, "ticket_template", template.ID)
	require.NoError(t, err)
	require.Len(t, defs, 1)
	assert.Equal(t, "a", defs[0].Name)
}

func TestTicketTemplateService_DeleteTemplate_DeletesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_delete_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)
	fieldSvc := NewFieldDefinitionService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name: "模板", Category: "cat", TenantID: 1,
		Fields: []FieldDefinitionInput{{Name: "a", Label: "A", FieldType: "text"}},
	})
	require.NoError(t, err)
	require.NoError(t, svc.DeleteTemplate(ctx, template.ID, 1))

	defs, err := fieldSvc.ListDefinitions(ctx, 1, "ticket_template", template.ID)
	require.NoError(t, err)
	assert.Empty(t, defs)
}

// TestTicketTemplateService_CreateTemplate_PersistsCategoryIDs 是 §5.4 任务包指出的独立
// 发现的真实 bug 的直接回归：CreateTemplateRequest 之前完全没有 CategoryIDs 字段，
// 不管 create API 提交什么，category_ids 这一列永远写不进数据库。这里验证提交非空
// categoryIds 之后，读回（Get）能看到落库的值——不是只检查 Create() 的返回值。
func TestTicketTemplateService_CreateTemplate_PersistsCategoryIDs(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_category_ids_create?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name: "网络接入申请", Category: "网络", TenantID: 1,
		CategoryIDs: []int{10, 20, 30},
	})
	require.NoError(t, err)
	require.ElementsMatch(t, []int{10, 20, 30}, template.CategoryIds, "Create() 返回值应该带上落库的 category_ids")

	fetched, err := svc.GetTemplate(ctx, template.ID, 1)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{10, 20, 30}, fetched.CategoryIds, "重新查询必须能读回 create 时提交的 category_ids")
}

// TestTicketTemplateService_UpdateTemplate_PersistsCategoryIDs 覆盖 update 路径的同一个
// bug：UpdateTemplateRequest 之前也没有 CategoryIDs 字段。
func TestTicketTemplateService_UpdateTemplate_PersistsCategoryIDs(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_category_ids_update?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name: "模板", Category: "cat", TenantID: 1,
	})
	require.NoError(t, err)
	require.Empty(t, template.CategoryIds)

	updated, err := svc.UpdateTemplate(ctx, template.ID, &UpdateTemplateRequest{
		CategoryIDs: []int{7, 8},
	}, 1)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{7, 8}, updated.CategoryIds)

	fetched, err := svc.GetTemplate(ctx, template.ID, 1)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{7, 8}, fetched.CategoryIds, "重新查询必须能读回 update 时提交的 category_ids")
}

// TestTicketTemplateService_UpdateTemplate_NilCategoryIDsPreservesExisting 锁定跟 Fields
// 一样的"nil=不修改"约定（见 TestTicketTemplateService_UpdateTemplate_NilFieldsPreservesExisting）：
// UpdateTemplateRequest.CategoryIDs 为 nil 时不应该清空已有的 category_ids。
func TestTicketTemplateService_UpdateTemplate_NilCategoryIDsPreservesExisting(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:ticket_template_category_ids_nil?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	svc := NewTicketTemplateService(client)

	template, err := svc.CreateTemplate(ctx, &CreateTemplateRequest{
		Name: "模板", Category: "cat", TenantID: 1,
		CategoryIDs: []int{1, 2, 3},
	})
	require.NoError(t, err)

	isActive := false
	_, err = svc.UpdateTemplate(ctx, template.ID, &UpdateTemplateRequest{
		IsActive: &isActive,
	}, 1)
	require.NoError(t, err)

	fetched, err := svc.GetTemplate(ctx, template.ID, 1)
	require.NoError(t, err)
	require.ElementsMatch(t, []int{1, 2, 3}, fetched.CategoryIds, "CategoryIDs 为 nil 的 Update 不应该清空已有的 category_ids")
}

func TestValidateTemplateFields_RejectsUnknownFieldType(t *testing.T) {
	err := validateTemplateFields([]FieldDefinitionInput{
		{Name: "weird_field", Label: "怪字段", FieldType: "banana"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "字段类型")
}

func TestValidateTemplateFields_AcceptsAllDocumentedFieldTypes(t *testing.T) {
	validTypes := []string{"text", "textarea", "number", "date", "select", "multiselect", "boolean", "file"}
	for _, ft := range validTypes {
		err := validateTemplateFields([]FieldDefinitionInput{
			{Name: "f_" + ft, Label: ft, FieldType: ft},
		})
		assert.NoError(t, err, "字段类型 %s 应该是合法的", ft)
	}
}
