package eventbus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"itsm-backend/common/executionscope"
)

// ExecutionEvent identifies a persisted producer's subject and stable event.
// Implementing this interface is not authorization; Authority checks persistence.
type ExecutionEvent interface {
	ExecutionWorkItemID() int
	PersistentEventID() string
}

type ExecutionIdentity struct {
	DeploymentID string `json:"deploymentId"`
	ScopeID      string `json:"scopeId"`
	WorkItemID   int    `json:"workItemId"`
}

// EventAuthority checks a declared source against current persistent authority.
// Consumers still own authorization and idempotency in their write transaction.
type EventAuthority interface {
	ValidateEvent(context.Context, executionscope.Ref, Envelope) error
}

func (r *streamRoutes) refFor(tenant string) (executionscope.Ref, error) {
	id, err := strconv.Atoi(tenant)
	if err != nil || id <= 0 || strconv.Itoa(id) != tenant {
		return executionscope.Ref{}, executionscope.ErrDenied
	}
	if !r.candidate {
		if err := executionscope.ValidateDeploymentID(r.deploymentID); err != nil {
			return executionscope.Ref{}, err
		}
		return executionscope.Ref{DeploymentID: r.deploymentID, TenantID: id}, nil
	}
	for _, ref := range r.refs {
		if ref.TenantID == id {
			return ref, nil
		}
	}
	return executionscope.Ref{}, executionscope.ErrDenied
}

// DecodeExecutionEnvelope preserves the trusted outer identity. Candidate wire
// messages have a bounded, exact schema; duplicate JSON members are rejected.
func DecodeExecutionEnvelope(raw []byte) (Envelope, error) {
	var env Envelope
	if len(raw) > 1<<20 {
		return env, fmt.Errorf("event envelope exceeds size limit")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := uniqueJSONValue(dec, 0); err != nil {
		return env, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return env, fmt.Errorf("trailing event JSON")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return env, err
	}
	for key := range fields {
		switch key {
		case "eventId", "execution", "eventType", "tenantId", "occurredAt", "payload":
		default:
			return env, fmt.Errorf("unknown event envelope field")
		}
	}
	var executionFields map[string]json.RawMessage
	if err := json.Unmarshal(fields["execution"], &executionFields); err != nil {
		return env, err
	}
	for key := range executionFields {
		switch key {
		case "deploymentId", "scopeId", "workItemId":
		default:
			return env, fmt.Errorf("unknown event execution field")
		}
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return env, err
	}
	if env.Execution == nil || env.Execution.WorkItemID <= 0 || env.EventID == "" || strings.TrimSpace(env.EventID) != env.EventID || len(env.EventID) > 200 || env.OccurredAt.IsZero() {
		return env, fmt.Errorf("persistent event identity required")
	}
	if err := validateStableTopic(env.EventType); err != nil {
		return env, err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(env.Payload, &payload); err != nil || payload == nil {
		return env, fmt.Errorf("event payload must be an object")
	}
	for _, key := range []string{"eventType", "tenantId", "occurredAt", "eventId", "execution"} {
		if _, ok := payload[key]; ok {
			return env, fmt.Errorf("event payload overlaps envelope identity")
		}
	}
	return env, nil
}

func uniqueJSONValue(dec *json.Decoder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("event JSON nesting exceeds limit")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid event JSON member")
			}
			seen[name] = true
			if err := uniqueJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := uniqueJSONValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("invalid event JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

func validateEnvelopeRoute(env Envelope, ref executionscope.Ref, eventType string) error {
	if env.Execution == nil || env.EventType != eventType || env.TenantID != strconv.Itoa(ref.TenantID) || env.Execution.DeploymentID != ref.DeploymentID || env.Execution.ScopeID != ref.ScopeID {
		return fmt.Errorf("event envelope does not match frozen transport route")
	}
	if ref.ScopeID == "" {
		if ref.TenantID <= 0 {
			return executionscope.ErrDenied
		}
		return executionscope.ValidateDeploymentID(ref.DeploymentID)
	}
	return executionscope.ValidateRef(ref)
}
