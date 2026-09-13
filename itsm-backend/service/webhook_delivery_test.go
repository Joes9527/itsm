package service

import (
	"errors"
	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"testing"
)

func TestWebhookPreflightPreservesInfrastructureFailure(t *testing.T) {
	transient := errors.New("temporary database failure")
	require.ErrorIs(t, webhookPreflightError(transient), transient)
	var blocked *outboxDeliveryBlockedError
	require.False(t, errors.As(webhookPreflightError(transient), &blocked))
	require.True(t, errors.As(webhookPreflightError(executionscope.ErrDenied), &blocked))
}
