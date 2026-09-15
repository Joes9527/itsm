package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
)

func TestWebhookPreflightPreservesInfrastructureFailure(t *testing.T) {
	transient := errors.New("temporary database failure")
	require.ErrorIs(t, webhookPreflightError(transient), transient)
	var blocked *outboxDeliveryBlockedError
	for _, cause := range []error{transient, context.Canceled, context.DeadlineExceeded} {
		require.ErrorIs(t, webhookPreflightError(cause), cause)
		require.False(t, errors.As(webhookPreflightError(cause), &blocked))
	}
	require.True(t, errors.As(webhookPreflightError(executionscope.ErrDenied), &blocked))
}
