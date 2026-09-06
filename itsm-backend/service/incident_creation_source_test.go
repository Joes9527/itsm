package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	creation "itsm-backend/handlers/common/workitemcreation"
	"strings"
	"testing"
)

func TestIncidentCreationSourcePolicy(t *testing.T) {
	ref := &creation.SourceReference{Provider: "bpmn", EventID: "verified-execution"}
	for _, tt := range []struct {
		name, channel, provider, source, want string
		ref                                   *creation.SourceReference
		denied                                bool
	}{
		{name: "http omitted", channel: "http", want: "manual"},
		{name: "http manual", channel: "http", source: "manual", want: "manual"},
		{name: "http user", channel: "http", source: " user ", want: "user"},
		{name: "http system", channel: "http", source: "system", denied: true},
		{name: "http monitoring", channel: "http", source: "monitoring", denied: true},
		{name: "http spoofed provider", channel: "http", provider: "bpmn", source: "system", ref: ref, denied: true},
		{name: "http reference", channel: "http", ref: &creation.SourceReference{Provider: "http", EventID: "claimed"}, denied: true},
		{name: "bpmn omitted", channel: "bpmn", provider: "bpmn", ref: ref, want: "system"},
		{name: "bpmn system", channel: "bpmn", provider: "bpmn", ref: ref, source: " system ", want: "system"},
		{name: "bpmn manual", channel: "bpmn", provider: "bpmn", ref: ref, source: "manual", denied: true},
		{name: "bpmn user", channel: "bpmn", provider: "bpmn", ref: ref, source: "user", denied: true},
		{name: "bpmn monitoring", channel: "bpmn", provider: "bpmn", ref: ref, source: "monitoring", denied: true},
		{name: "bpmn missing reference", channel: "bpmn", provider: "bpmn", denied: true},
		{name: "bpmn mismatched reference", channel: "bpmn", provider: "bpmn", ref: &creation.SourceReference{Provider: "monitoring", EventID: "claimed"}, denied: true},
		{name: "bpmn empty event", channel: "bpmn", provider: "bpmn", ref: &creation.SourceReference{Provider: "bpmn"}, denied: true},
		{name: "unknown adapter", channel: "monitoring", provider: "monitoring", source: "monitoring", denied: true},
		{name: "obsolete fixture", channel: "api", denied: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := &creation.IncidentInput{Source: tt.source}
			in := creation.ResolvedIntake{RecordClass: "incident", Identity: creation.Identity{Channel: tt.channel, Provider: tt.provider}, Command: creation.CreateWorkItemCommand{Priority: "high", Incident: input, SourceReference: tt.ref}}
			plan, err := (&IncidentService{}).Prepare(context.Background(), nil, in)
			if tt.denied {
				require.ErrorIs(t, err, creation.ErrPermissionDenied)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, plan.WorkItem.Source)
			}
			require.Equal(t, tt.source, input.Source)
		})
	}
}

func TestIncidentCreationMetadataBoundary(t *testing.T) {
	for _, key := range []string{"password", " Access_Token ", "client-secret", "apiKey", "authorization", "private_key", "refreshToken", "db_password", "cookie"} {
		t.Run(key, func(t *testing.T) {
			in := creation.ResolvedIntake{Identity: creation.Identity{Channel: "http"}, Command: creation.CreateWorkItemCommand{Priority: "high", Incident: &creation.IncidentInput{Metadata: map[string]any{"nested": []any{map[string]any{key: "credential-sentinel"}}}}}}
			_, err := (&IncidentService{}).Prepare(context.Background(), nil, in)
			require.ErrorIs(t, err, creation.ErrInvalidCommand)
			require.NotContains(t, err.Error(), "credential-sentinel")
		})
	}
	for _, metadata := range []map[string]any{
		{"large": strings.Repeat("x", 65537)},
		{"many": make([]any, 1025)},
		{"deep": map[string]any{"a": map[string]any{"b": map[string]any{"c": map[string]any{"d": map[string]any{"e": map[string]any{"f": map[string]any{"g": map[string]any{"h": map[string]any{"i": true}}}}}}}}}},
	} {
		_, err := (&IncidentService{}).Prepare(context.Background(), nil, creation.ResolvedIntake{Identity: creation.Identity{Channel: "http"}, Command: creation.CreateWorkItemCommand{Priority: "high", Incident: &creation.IncidentInput{Metadata: metadata}}})
		require.ErrorIs(t, err, creation.ErrInvalidCommand)
	}
	metadata := map[string]any{"title": "benign", "description": "diagnostic", "requester": "reported by operator", "formValues": map[string]any{"amount": json.Number("9007199254740993.125")}, "tokenCount": 4}
	plan, err := (&IncidentService{}).Prepare(context.Background(), nil, creation.ResolvedIntake{Identity: creation.Identity{Channel: "http"}, Command: creation.CreateWorkItemCommand{Priority: "high", Incident: &creation.IncidentInput{Metadata: metadata}}})
	require.NoError(t, err)
	require.Equal(t, metadata, plan.WorkflowVariables["metadata"])
}

func TestIncidentSourcePolicyRejectsDuplicateBindings(t *testing.T) {
	require.Panics(t, func() {
		newIncidentSourcePolicy(incidentSourceBinding{channel: "http"}, incidentSourceBinding{channel: "http"})
	})
}

func TestIncidentCreationMetadataRejectsCyclesAndUnknownSource(t *testing.T) {
	metadata := map[string]any{}
	metadata["cycle"] = metadata
	_, err := (&IncidentService{}).Prepare(context.Background(), nil, creation.ResolvedIntake{Identity: creation.Identity{Channel: "http"}, Command: creation.CreateWorkItemCommand{Priority: "high", Incident: &creation.IncidentInput{Metadata: metadata}}})
	require.ErrorIs(t, err, creation.ErrInvalidCommand)
	_, err = (&IncidentService{}).ValidateIncidentCreationInput(creation.Identity{Channel: "http"}, nil, &creation.IncidentInput{Source: "unknown-source-sentinel"})
	require.ErrorIs(t, err, creation.ErrInvalidCommand)
	require.NotContains(t, err.Error(), "unknown-source-sentinel")
}

type pointerEncodedIncidentMetadata string

func (*pointerEncodedIncidentMetadata) MarshalJSON() ([]byte, error) {
	return []byte(`{"password":"credential-sentinel"}`), nil
}

func TestIncidentCreationMetadataRejectsAddressablePointerEncoder(t *testing.T) {
	values := []pointerEncodedIncidentMetadata{"benign"}
	input := &creation.IncidentInput{Source: "manual", Metadata: map[string]any{"nested": values}}
	_, err := (&IncidentService{}).ValidateIncidentCreationInput(creation.Identity{Channel: "http"}, nil, input)
	require.ErrorIs(t, err, creation.ErrInvalidCommand)
	require.NotContains(t, err.Error(), "credential-sentinel")
	require.Equal(t, []pointerEncodedIncidentMetadata{"benign"}, values)
	require.Equal(t, "manual", input.Source)
}
