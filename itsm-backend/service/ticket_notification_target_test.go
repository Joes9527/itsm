package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/common/executionscope"
	"itsm-backend/ent"
)

func TestNotificationTargetRejectsUnknownChannel(t *testing.T) {
	require.ErrorIs(t, validateNotificationConnectorTarget(&ent.TicketNotification{Channel: "unknown"}), executionscope.ErrDenied)
}
