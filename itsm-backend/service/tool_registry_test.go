package service

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"itsm-backend/dto"
	"itsm-backend/ent"
	"itsm-backend/ent/enttest"
	creation "itsm-backend/handlers/common/workitemcreation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// TestToolRegistry_NewToolRegistryAcceptsConfigurationItemService is a
// regression guard for the refactor that replaced CMDBService with
// ConfigurationItemService. It verifies the type of the third constructor
// parameter at compile time, ensuring no caller accidentally re-introduces
// CMDBService.
func TestToolRegistry_NewToolRegistryAcceptsConfigurationItemService(t *testing.T) {
	// Build a TypeOf for the constructor's third parameter at runtime so
	// the test fails loudly if a future change reverts to CMDBService.
	fnType := reflect.TypeOf(NewToolRegistry)
	require.Equal(t, 4, fnType.NumIn(), "NewToolRegistry must keep its 4-parameter signature")
	third := fnType.In(2)
	want := reflect.TypeOf((*ConfigurationItemService)(nil))
	assert.Equal(t, want.String(), third.String(),
		"NewToolRegistry's third argument must be *ConfigurationItemService (was %s)", third.String())

	// And the field type on the struct must match as well.
	// ToolRegistry fields: 0=rag, 1=incident, 2=cmdb, 3=client
	tr := &ToolRegistry{}
	fieldType := reflect.TypeOf(tr).Elem().Field(2).Type
	assert.Equal(t, want.String(), fieldType.String(),
		"ToolRegistry.cmdb field must be *ConfigurationItemService (was %s)", fieldType.String())
}

// TestToolRegistry_ListCIsUsesConfigurationItemService verifies that the
// `list_cis` tool routes through ConfigurationItemService.ListCIs and that the
// limit/offset arguments are translated into page = offset/limit + 1.
func TestToolRegistry_ListCIsUsesConfigurationItemService(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:tool_registry?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx := context.Background()
	ciType, err := client.CIType.Create().SetName("server").SetTenantID(1).Save(ctx)
	require.NoError(t, err)

	// Seed 25 CIs so we can verify the offset/page translation: offset=10,
	// limit=5 must map to page=3 (offset/limit + 1) and size=5.
	for i := 0; i < 25; i++ {
		_, err := client.ConfigurationItem.Create().
			SetName("ci-" + string(rune('a'+i%26))).
			SetCiType("server").
			SetCiTypeID(ciType.ID).
			SetStatus("active").
			SetTenantID(1).
			Save(ctx)
		require.NoError(t, err)
	}

	logger := zaptest.NewLogger(t).Sugar()
	historySvc := NewCIHistoryService(client, logger)
	tagSvc := NewCITagService(client, logger)
	ciSvc := NewConfigurationItemService(client, logger, historySvc, tagSvc)

	// RAG and Incident services are unused by list_cis; pass nil so that
	// any accidental invocation would NPE immediately.
	registry := NewToolRegistry(nil, nil, ciSvc, client)

	t.Run("translates limit and offset into page", func(t *testing.T) {
		result, err := registry.Execute(context.Background(), 1, "list_cis", map[string]interface{}{
			"limit":  float64(5),
			"offset": float64(10),
		})
		require.NoError(t, err)
		items, ok := result.([]*dto.CIResponse)
		require.True(t, ok, "list_cis result must be a []*dto.CIResponse, got %T", result)
		assert.Len(t, items, 5, "page 3 size 5 should return 5 CIs")
	})

	t.Run("defaults to limit=10 offset=0 when args missing", func(t *testing.T) {
		result, err := registry.Execute(context.Background(), 1, "list_cis", map[string]interface{}{})
		require.NoError(t, err)
		items, ok := result.([]*dto.CIResponse)
		require.True(t, ok)
		assert.Len(t, items, 10, "default limit=10 should return 10 CIs")
	})

	t.Run("returns empty list for tenant with no CIs", func(t *testing.T) {
		result, err := registry.Execute(context.Background(), 42, "list_cis", map[string]interface{}{
			"limit":  float64(5),
			"offset": float64(0),
		})
		require.NoError(t, err)
		items, ok := result.([]*dto.CIResponse)
		require.True(t, ok)
		assert.Empty(t, items, "unknown tenant should yield zero CIs")
	})

	t.Run("clamps offset past total to empty page", func(t *testing.T) {
		result, err := registry.Execute(context.Background(), 1, "list_cis", map[string]interface{}{
			"limit":  float64(10),
			"offset": float64(1000),
		})
		require.NoError(t, err)
		items, ok := result.([]*dto.CIResponse)
		require.True(t, ok)
		assert.Empty(t, items, "offset past end of result set must return no CIs")
	})
}

// TestToolRegistry_ListToolsCatalog sanity-checks the registered list. It is
// here so future regressions (missing tool name, wrong action) surface in the
// test suite rather than at runtime when the LLM gateway tries to look up a
// tool by name.
func TestToolRegistry_ListToolsCatalog(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:tool_registry_catalog?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	historySvc := NewCIHistoryService(client, logger)
	tagSvc := NewCITagService(client, logger)
	ciSvc := NewConfigurationItemService(client, logger, historySvc, tagSvc)
	registry := NewToolRegistry(nil, nil, ciSvc, client)

	tools := registry.ListTools()
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	assert.True(t, names["list_cis"], "list_cis tool must remain registered")
	assert.True(t, names["get_incident_stats"], "get_incident_stats tool must remain registered")
	assert.True(t, names["list_kb"], "list_kb tool must remain registered")
	assert.True(t, names["create_ticket"], "create_ticket tool must remain registered")
	assert.True(t, names["update_ticket"], "update_ticket tool must remain registered")
}

func TestCreateTicketToolPublishesItsExactExecutableContract(t *testing.T) {
	tool := NewToolRegistry(nil, nil, nil, nil).GetTool("create_ticket")
	require.NotNil(t, tool)
	require.Equal(t, map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"description":          "Canonical create-ticket arguments. Intake trims title, description and priority before validation; omitted or blank priority defaults to medium.",
		"properties": map[string]interface{}{
			"title":        map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 500, "pattern": `\S`, "description": "Canonical title after Intake trimming."},
			"description":  map[string]interface{}{"type": "string", "maxLength": 20000, "description": "Canonical description after Intake trimming."},
			"priority":     map[string]interface{}{"type": "string", "enum": []string{"low", "medium", "high", "critical", "urgent"}, "description": "Canonical priority after Intake trimming; omit for medium. Intake also accepts blank priority as the medium default."},
			"requester_id": map[string]interface{}{"type": "integer", "minimum": 1},
		},
		"required": []string{"title"},
	}, tool.ArgsSchema)
	require.Equal(t, map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]interface{}{
			"workItemId": map[string]interface{}{"type": "integer", "minimum": 1},
			"number":     map[string]interface{}{"type": "string"},
			"recordClass": map[string]interface{}{
				"type": "string", "enum": []string{creation.RecordClassGeneric},
			},
			"professionalReference": map[string]interface{}{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]interface{}{
					"type": map[string]interface{}{"type": "string", "enum": []string{""}},
					"id":   map[string]interface{}{"type": "integer", "enum": []int{0}},
				},
				"required": []string{"type", "id"},
			},
			"workflowStartStatus": map[string]interface{}{
				"type": "string", "enum": []string{"not_required", "pending", "active", "manual_intervention_required"},
			},
			"replayed": map[string]interface{}{"type": "boolean"},
		},
		"required": []string{"workItemId", "number", "recordClass", "professionalReference", "workflowStartStatus", "replayed"},
	}, tool.ResultSchema)

	update := NewToolRegistry(nil, nil, nil, nil).GetTool("update_ticket")
	require.NotNil(t, update)
	require.Nil(t, update.ArgsSchema)
	require.Nil(t, update.ResultSchema)
}

func TestCreateTicketToolArgumentsMatchPublishedContract(t *testing.T) {
	for _, priority := range []string{"low", "medium", "high", "critical", "urgent"} {
		t.Run("accepts_"+priority, func(t *testing.T) {
			command, requester, err := toolCreationCommand(`{"title":"Approved request","description":"details","priority":"`+priority+`","requester_id":23}`, 4, 17)
			require.NoError(t, err)
			require.Equal(t, priority, command.Priority)
			require.Equal(t, 23, requester)
		})
	}
	for name, raw := range map[string]string{
		"zero requester":     `{"title":"request","requester_id":0}`,
		"fraction requester": `{"title":"request","requester_id":1.5}`,
		"extra field":        `{"title":"request","tenant_id":9}`,
	} {
		t.Run("rejects_"+name, func(t *testing.T) {
			_, _, err := toolCreationCommand(raw, 4, 17)
			require.Error(t, err)
		})
	}
}

func TestCreateTicketToolResultSchemaMatchesSerializedSharedReceipt(t *testing.T) {
	receipt := creation.CreateWorkItemResult{
		WorkItemID: 31, Number: "TKT-202609-000031", RecordClass: creation.RecordClassGeneric,
		ProfessionalReference: creation.ProfessionalReference{}, WorkflowStartStatus: "not_required", Replayed: true,
	}
	encoded, err := json.Marshal(receipt)
	require.NoError(t, err)
	var serialized map[string]interface{}
	require.NoError(t, json.Unmarshal(encoded, &serialized))
	require.Equal(t, map[string]interface{}{
		"workItemId": float64(31), "number": "TKT-202609-000031", "recordClass": "generic",
		"professionalReference": map[string]interface{}{"type": "", "id": float64(0)},
		"workflowStartStatus":   "not_required", "replayed": true,
	}, serialized)
}

// TestToolRegistry_UnknownTool confirms unknown tools are rejected with an
// error rather than silently returning nil.
func TestToolRegistry_UnknownTool(t *testing.T) {
	client := enttest.Open(t, "sqlite3", "file:tool_registry_unknown?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	logger := zaptest.NewLogger(t).Sugar()
	historySvc := NewCIHistoryService(client, logger)
	tagSvc := NewCITagService(client, logger)
	ciSvc := NewConfigurationItemService(client, logger, historySvc, tagSvc)
	registry := NewToolRegistry(nil, nil, ciSvc, client)

	_, err := registry.Execute(context.Background(), 1, "does_not_exist", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown tool")
}

// Compile-time guard that the test file references ent (otherwise the unused
// import would fail after refactors).
var _ = (*ent.Client)(nil)
