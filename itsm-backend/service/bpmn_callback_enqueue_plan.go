package service

import (
	"fmt"
	"strings"

	"itsm-backend/service/bpmn"
)

// CallbackDescriptor identifies the handler-owned callback contract selected
// from the BPMN definition. It deliberately contains no task, tenant, or work
// item identity: those values are loaded authoritatively at execution time.
type CallbackDescriptor struct {
	HandlerID string
	TaskType  string
	Action    string
	ConfigRef string
}

// CallbackEnqueuePlan is the validated, durable boundary between a BPMN
// definition callback and later callback execution.
type CallbackEnqueuePlan struct {
	HandlerID        string
	TaskType         string
	Action           string
	ConfigRef        string
	Payload          map[string]interface{}
	OptionalDeclared bool
	BlockCode        bpmn.CallbackBlockCode
	BlockMessage     string
}

// BuildCallbackEnqueuePlan validates a callback against the selected handler's
// declared action contract before it can be persisted for later execution.
// Definition defects are represented as an intentionally empty blocked plan;
// registry availability remains an infrastructure error that prevents enqueue.
func BuildCallbackEnqueuePlan(
	descriptor CallbackDescriptor,
	rawPayload map[string]interface{},
	optionalDeclared bool,
	registry *bpmn.CallbackRegistry,
) (CallbackEnqueuePlan, error) {
	plan := CallbackEnqueuePlan{
		HandlerID:        strings.TrimSpace(descriptor.HandlerID),
		TaskType:         strings.TrimSpace(descriptor.TaskType),
		Action:           strings.TrimSpace(descriptor.Action),
		OptionalDeclared: optionalDeclared,
	}
	if registry == nil {
		return CallbackEnqueuePlan{}, fmt.Errorf("callback registry is required")
	}
	if plan.HandlerID == "" || plan.TaskType == "" {
		return blockedCallbackEnqueuePlan(plan), nil
	}

	handler := registry.GetHandler(plan.HandlerID)
	if handler == nil || handler.GetHandlerID() != plan.HandlerID || handler.GetTaskType() != plan.TaskType {
		return blockedCallbackEnqueuePlan(plan), nil
	}
	contractProvider, ok := handler.(bpmn.CallbackContractProvider)
	if !ok {
		return blockedCallbackEnqueuePlan(plan), nil
	}
	contract, ok := contractProvider.CallbackContract(plan.Action)
	if !ok {
		return blockedCallbackEnqueuePlan(plan), nil
	}
	if err := validateBPMNCallbackActionContract(contract); err != nil {
		return blockedCallbackEnqueuePlan(plan), nil
	}
	configRef, err := normalizeBPMNCallbackContractConfigRef(contract, descriptor.ConfigRef)
	if err != nil {
		return blockedCallbackEnqueuePlan(plan), nil
	}
	plan.ConfigRef = configRef

	payloadSource := rawPayload
	if normalizer, ok := handler.(bpmn.CallbackPayloadNormalizer); ok {
		normalized, err := normalizer.NormalizeCallbackPayload(plan.Action, rawPayload)
		if err != nil {
			return blockedCallbackEnqueuePlan(plan), nil
		}
		if err := validateBPMNCallbackNormalizerContractOutput(contract, normalized); err != nil {
			return blockedCallbackEnqueuePlan(plan), nil
		}
		payloadSource = normalized
	}
	payload, err := normalizeBPMNCallbackContractPayload(contract, payloadSource)
	if err != nil {
		return blockedCallbackEnqueuePlan(plan), nil
	}
	plan.Payload = payload
	return plan, nil
}

func blockedCallbackEnqueuePlan(plan CallbackEnqueuePlan) CallbackEnqueuePlan {
	plan.ConfigRef = ""
	plan.Payload = map[string]interface{}{}
	plan.BlockCode = bpmn.CallbackBlockHandlerContract
	plan.BlockMessage = "callback handler contract is invalid"
	return plan
}
