package service

import (
	"errors"
	"testing"

	"itsm-backend/service/bpmn"

	"github.com/stretchr/testify/require"
)

func TestCallbackEnqueuePlanBlocksMissingRequiredFieldWithoutPayloadFallback(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{
		PayloadFields:  []string{"title", "description"},
		RequiredFields: []string{"title"},
	}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"description": "must not survive the blocked plan", "password": "secret"},
		false,
		callbackEnqueuePlanRegistry(handler),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
	require.Empty(t, plan.Payload)
	require.NotContains(t, plan.BlockMessage, "secret")
}

func TestCallbackEnqueuePlanBlocksNormalizerFailuresWithoutRawPayloadFallback(t *testing.T) {
	handler := newCallbackEnqueuePlanTestHandler("test_handler", "test_task")
	handler.contracts["apply"] = bpmn.CallbackActionContract{PayloadFields: []string{"title"}}
	normalizer := &normalizingCallbackEnqueuePlanTestHandler{callbackEnqueuePlanTestHandler: handler, normalize: func(string, map[string]interface{}) (map[string]interface{}, error) {
		return nil, errors.New("normalizer rejected confidential input")
	}}

	plan, err := BuildCallbackEnqueuePlan(
		CallbackDescriptor{HandlerID: "test_handler", TaskType: "test_task", Action: "apply"},
		map[string]interface{}{"title": "must not fall back", "authorization": "Bearer secret"},
		false,
		callbackEnqueuePlanRegistry(normalizer),
	)

	require.NoError(t, err)
	require.Equal(t, bpmn.CallbackBlockHandlerContract, plan.BlockCode)
	require.Empty(t, plan.Payload)
	require.NotContains(t, plan.BlockMessage, "secret")
}
