package intake

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDecodeCreateWorkItemCommandRejectsIdentityFields(t *testing.T) {
	for _, field := range []string{"tenantId", "actorId", "requesterId", "role"} {
		t.Run(field, func(t *testing.T) {
			payload := `{"idempotencyKey":"key-1","intakeKind":"incident","title":"test","` + field + `":1}`
			_, err := DecodeCreateWorkItemCommand(strings.NewReader(payload))
			require.ErrorIs(t, err, ErrInvalidCommand)
		})
	}
}

func TestDecodeCreateWorkItemCommandRejectsTrailingJSON(t *testing.T) {
	_, err := DecodeCreateWorkItemCommand(strings.NewReader(
		`{"idempotencyKey":"key-1","intakeKind":"incident","title":"test"} {}`,
	))
	require.ErrorIs(t, err, ErrInvalidCommand)
}

func TestIdentityRejectsSourceProviderMismatch(t *testing.T) {
	identity := Identity{TenantID: 1, ActorID: 2, RequesterID: 2, Role: "requester", Channel: "kaf_web", Provider: "teams", TokenID: "jti-1"}
	command := validIncidentCommand("key-1", nil)
	command.SourceReference = &SourceReference{Provider: "wecom", EventID: "event-1"}

	err := identity.ValidateCommand(command)
	require.ErrorIs(t, err, ErrPermissionDenied)
}

func TestIdentityAcceptsMatchingProviderAndRejectsMissingAuthenticatedIDs(t *testing.T) {
	identity := Identity{TenantID: 1, ActorID: 2, RequesterID: 2, Role: "requester", Channel: "kaf_web", Provider: "teams", TokenID: "jti-1"}
	command := validIncidentCommand("key-1", nil)
	command.SourceReference = &SourceReference{Provider: " teams ", EventID: "event-1"}
	require.NoError(t, identity.ValidateCommand(command))

	identity.ActorID = 0
	require.ErrorIs(t, identity.ValidateCommand(command), ErrAuthenticationRequired)
}

func TestIntakeErrorUnwrapsCauseAndSerializesOnlySafeDetails(t *testing.T) {
	cause := errors.New("database password leaked through driver error")
	err := NewIntakeError(InfrastructureUnavailable, "intake temporarily unavailable", cause)

	require.ErrorIs(t, err, ErrInfrastructureUnavailable)
	require.ErrorIs(t, err, cause)
	require.Equal(t, 503, err.HTTPStatus)
	require.True(t, err.Retryable)
	require.NotContains(t, err.PublicJSON(), "password")
	require.Contains(t, err.PublicJSON(), `"code":"InfrastructureUnavailable"`)
}

func TestIntakeErrorPoliciesMapToStableHTTPStatuses(t *testing.T) {
	tests := []struct {
		code      ErrorCode
		status    int
		retryable bool
		sentinel  error
	}{
		{InvalidCommand, 400, false, ErrInvalidCommand},
		{AuthenticationRequired, 401, false, ErrAuthenticationRequired},
		{PermissionDenied, 403, false, ErrPermissionDenied},
		{ReferenceNotFound, 404, false, ErrReferenceNotFound},
		{IdempotencyConflict, 409, false, ErrIdempotencyConflict},
		{DomainValidationFailed, 422, false, ErrDomainValidationFailed},
		{UnsupportedRecordClass, 422, false, ErrUnsupportedRecordClass},
		{WorkflowBindingRequired, 422, false, ErrWorkflowBindingRequired},
		{InfrastructureUnavailable, 503, true, ErrInfrastructureUnavailable},
		{InternalFailure, 500, false, ErrInternalFailure},
	}
	for _, test := range tests {
		t.Run(string(test.code), func(t *testing.T) {
			err := NewIntakeError(test.code, "safe", nil)
			require.Equal(t, test.status, err.HTTPStatus)
			require.Equal(t, test.retryable, err.Retryable)
			require.ErrorIs(t, err, test.sentinel)
		})
	}
}
