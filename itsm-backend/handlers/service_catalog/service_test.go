package service_catalog

import (
	"context"
	"testing"

	"itsm-backend/ent/enttest"
	"itsm-backend/service"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestService_Create_SavesFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_fields?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	fields := []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text", Required: true, SortOrder: 0},
		{Name: "device_count", Label: "设备数量", FieldType: "number", Required: false, SortOrder: 1},
	}

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, fields)
	require.NoError(t, err)
	require.NotNil(t, created)

	// 返回对象应该携带刚保存的字段定义
	require.Len(t, created.Fields, 2)
	assert.Equal(t, "office_location", created.Fields[0].Name)
	assert.Equal(t, "device_count", created.Fields[1].Name)

	// 底层 field_definitions 表应该真正落库，entity_type 为 "service_catalog"
	listed, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, 1, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "office_location", listed[0].Name)
	assert.Equal(t, "办公地点", listed[0].Label)
	assert.Equal(t, "device_count", listed[1].Name)
}

func TestService_Create_NoClient_SkipsFieldDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_create_noclient?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	// client == nil：不接入字段定义（沿用现有可选 client 的兼容路径）
	svc := NewService(repo, nil, zaptest.NewLogger(t).Sugar())

	fields := []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	}

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, fields)
	require.NoError(t, err)
	require.NotNil(t, created)
	// Create 仍然把入参 fields 回填到返回对象上
	require.Len(t, created.Fields, 1)
}

func TestService_Update_NilFields_LeavesExistingDefinitionsUntouched(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_nil?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	})
	require.NoError(t, err)

	// Update 时不传 fields（nil）：既有字段定义应保持不变
	updated, err := svc.Update(ctx, 1, created.ID, "VM Service Updated", "", "", 0, "", 0, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, updated)

	listed, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, 1, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "office_location", listed[0].Name)
}

func TestService_Update_NonNilFields_ReplacesDefinitions(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:sc_service_update_replace?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	repo := NewEntRepository(client)
	svc := NewService(repo, client, zaptest.NewLogger(t).Sugar())

	created, err := svc.Create(ctx, "VM Service", "it_service", "virtual machine", 1, 1, "enabled", 0, 0, []service.FieldDefinitionInput{
		{Name: "office_location", Label: "办公地点", FieldType: "text"},
	})
	require.NoError(t, err)

	newFields := []service.FieldDefinitionInput{
		{Name: "device_count", Label: "设备数量", FieldType: "number"},
	}
	updated, err := svc.Update(ctx, 1, created.ID, "", "", "", 0, "", 0, 0, newFields)
	require.NoError(t, err)
	require.Len(t, updated.Fields, 1)
	assert.Equal(t, "device_count", updated.Fields[0].Name)

	listed, err := service.NewFieldDefinitionService(client).ListDefinitions(ctx, 1, "service_catalog", created.ID)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, "device_count", listed[0].Name)
}
