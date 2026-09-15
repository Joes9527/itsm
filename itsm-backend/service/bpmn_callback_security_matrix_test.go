package service

import (
	"testing"

	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
)

func TestCallbackEnqueuePlanFailsClosedForInvalidCallbackContracts(t *testing.T) {
	validHandler := newCallbackEnqueuePlanTestHandler("valid_handler", "valid_task")
	validHandler.contracts["apply"] = bpmn.CallbackActionContract{PayloadFields: []string{"title"}, ConfigRefRequired: true}
	normalizerLeakHandler := newCallbackEnqueuePlanTestHandler("normalizer_handler", "normalizer_task")
	normalizerLeakHandler.contracts["apply"] = bpmn.CallbackActionContract{PayloadFields: []string{"title"}}
	normalizer := &normalizingCallbackEnqueuePlanTestHandler{callbackEnqueuePlanTestHandler: normalizerLeakHandler, normalize: func(string, map[string]interface{}) (map[string]interface{}, error) {
		return map[string]interface{}{"title": "declared", "token": "must-not-persist"}, nil
	}}
	malformedContractHandler := newCallbackEnqueuePlanTestHandler("malformed_handler", "malformed_task")
	malformedContractHandler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:  []string{"title"},
		RequiredFields: []string{"undeclared_required"},
	}

	registry := callbackEnqueuePlanRegistry(validHandler, normalizer, malformedContractHandler)
	for _, tt := range []struct {
		name       string
		descriptor CallbackDescriptor
		registry   *bpmn.CallbackRegistry
		wantError  bool
	}{
		{
			name:       "unknown handler",
			descriptor: CallbackDescriptor{HandlerID: "unknown_handler", TaskType: "unknown_task", Action: "apply"},
			registry:   registry,
		},
		{
			name:       "task type mismatch",
			descriptor: CallbackDescriptor{HandlerID: "valid_handler", TaskType: "wrong_task", Action: "apply"},
			registry:   registry,
		},
		{
			name:       "unknown action",
			descriptor: CallbackDescriptor{HandlerID: "valid_handler", TaskType: "valid_task", Action: "unknown"},
			registry:   registry,
		},
		{
			name:       "missing required config ref",
			descriptor: CallbackDescriptor{HandlerID: "valid_handler", TaskType: "valid_task", Action: "apply"},
			registry:   registry,
		},
		{
			name:       "malformed config ref",
			descriptor: CallbackDescriptor{HandlerID: "valid_handler", TaskType: "valid_task", Action: "apply", ConfigRef: "https://token:secret@example.test"},
			registry:   registry,
		},
		{
			name:       "normalizer adds undeclared value",
			descriptor: CallbackDescriptor{HandlerID: "normalizer_handler", TaskType: "normalizer_task", Action: "apply", ConfigRef: "normalizer-config"},
			registry:   registry,
		},
		{
			name:       "malformed required field contract",
			descriptor: CallbackDescriptor{HandlerID: "malformed_handler", TaskType: "malformed_task", Action: "apply"},
			registry:   registry,
		},
		{
			name:       "missing registry is an infrastructure error",
			descriptor: CallbackDescriptor{HandlerID: "valid_handler", TaskType: "valid_task", Action: "apply"},
			wantError:  true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := BuildCallbackEnqueuePlan(tt.descriptor, map[string]interface{}{"title": "safe", "password": "secret"}, false, tt.registry)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
			require.Empty(t, plan.ConfigRef)
			require.Empty(t, plan.Payload)
			require.NotContains(t, plan.BlockMessage, "secret")
		})
	}
}
