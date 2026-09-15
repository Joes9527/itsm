package service

import (
	"context"
	"strings"
	"testing"

	"itsm-backend/ent"
	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestBuildCallbackEnqueuePlanFiltersDeclaredPayload(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:     []string{"title", "priority"},
		RequiredFields:    []string{"title"},
		ConfigRefRequired: true,
	}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply", ConfigRef: "callback-config"},
		map[string]interface{}{
			"title":       "Approved change",
			"priority":    "high",
			"password":    "must-not-persist",
			"tenant_id":   999,
			"business_id": 777,
		},
		true,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Empty(t, plan.BlockCode)
	require.Equal(t, "callback-config", plan.ConfigRef)
	require.Equal(t, map[string]interface{}{"title": "Approved change", "priority": "high"}, plan.Payload)
	require.True(t, plan.OptionalDeclared)
}

func TestBuildCallbackEnqueuePlanBlocksInvalidRequiredConfigRefWithoutRetainingIt(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:     []string{"title"},
		ConfigRefRequired: true,
	}

	for _, testCase := range []struct {
		name      string
		configRef string
	}{
		{name: "missing", configRef: ""},
		{name: "url containing secret", configRef: "https://api.example.test/?token=secret"},
		{name: "secret-like delimiter", configRef: "connector:secret"},
		{name: "surrounding whitespace", configRef: " config-with-whitespace "},
		{name: "over length limit", configRef: strings.Repeat("a", maxBPMNCallbackConfigRefLength+1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := BuildCallbackEnqueuePlan(
				CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply", ConfigRef: testCase.configRef},
				map[string]interface{}{"title": "must not persist with invalid config"},
				false,
				callbackEnqueuePlanRegistry(handler),
			)

			require.NoError(t, err)
			require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
			require.Empty(t, plan.ConfigRef)
			require.Empty(t, plan.Payload)
			require.NotContains(t, plan.BlockMessage, "secret")
		})
	}
}

func TestBuildCallbackEnqueuePlanBlocksMalformedOptionalConfigRefWithoutRetainingIt(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{PayloadFields: []string{"title"}}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply", ConfigRef: "connector/secret"},
		map[string]interface{}{"title": "must not persist with malformed config"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
	require.Empty(t, plan.ConfigRef)
	require.Empty(t, plan.Payload)
}

type callbackEnqueuePlanTestHandler struct {
	handlerID string
	taskType  string
	contracts map[string]bpmn.CallbackActionContract
}

func newCallbackEnqueuePlanTestHandler(handlerID, taskType string) *callbackEnqueuePlanTestHandler {
	return &callbackEnqueuePlanTestHandler{
		handlerID: handlerID,
		taskType:  taskType,
		contracts: map[string]bpmn.CallbackActionContract{},
	}
}

func (h *callbackEnqueuePlanTestHandler) GetHandlerID() string {
	return h.handlerID
}

func (h *callbackEnqueuePlanTestHandler) GetTaskType() string {
	return h.taskType
}

func (h *callbackEnqueuePlanTestHandler) Execute(context.Context, *ent.ProcessTask, map[string]interface{}) (*bpmn.CallbackEffect, error) {
	return bpmn.AppliedEffect("", nil), nil
}

func (h *callbackEnqueuePlanTestHandler) CallbackContract(action string) (bpmn.CallbackActionContract, bool) {
	contract, ok := h.contracts[action]
	return contract, ok
}

type normalizingCallbackEnqueuePlanTestHandler struct {
	*callbackEnqueuePlanTestHandler
	normalize func(string, map[string]interface{}) (map[string]interface{}, error)
}

func (h *normalizingCallbackEnqueuePlanTestHandler) NormalizeCallbackPayload(action string, variables map[string]interface{}) (map[string]interface{}, error) {
	return h.normalize(action, variables)
}

func callbackEnqueuePlanRegistry(handlers ...bpmn.ServiceTaskHandlerInterface) *bpmn.CallbackRegistry {
	registry := bpmn.NewCallbackRegistry(nil, zap.NewNop().Sugar())
	for _, handler := range handlers {
		registry.RegisterHandler(handler)
	}
	return registry
}

func TestBuildCallbackEnqueuePlanCopiesOnlyParsedOptionalDeclaration(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{PayloadFields: []string{"title"}}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"title": "kept", "callback_optional": false},
		true,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.True(t, plan.OptionalDeclared)
	require.NotContains(t, plan.Payload, "callback_optional")
}

func TestBuildCallbackEnqueuePlanAllowsExplicitEmptyPayloadContract(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"password": "must-not-persist"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Empty(t, plan.BlockCode)
	require.Empty(t, plan.Payload)
}
