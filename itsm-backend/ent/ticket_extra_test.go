package ent_test

import (
	"context"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"

	"itsm-backend/ent/enttest"
	"itsm-backend/ent/ticket"
)

func TestTicket_ExternalMessageID_Dedup(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", "file:ticket_extmsg_test?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	tenant, err := client.Tenant.Create().
		SetName("Test Tenant").SetCode("test").SetDomain("test.com").SetStatus("active").
		Save(ctx)
	require.NoError(t, err)

	user, err := client.User.Create().
		SetUsername("alice").SetEmail("alice@test.com").SetName("Alice").
		SetPasswordHash("hash").SetRole("end_user").SetActive(true).
		SetTenantID(tenant.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Ticket.Create().
		SetTitle("From email").
		SetDescription("body").
		SetRecordClass("incident").
		SetPriority("medium").
		SetTicketNumber("TCK-EXT-0001").
		SetRequesterID(user.ID).
		SetTenantID(tenant.ID).
		SetStatus("new").
		SetCreatorEmail("alice@test.com").
		SetExternalMessageID("<msg-1@contoso.com>").
		Save(ctx)
	require.NoError(t, err)

	exists, err := client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenant.ID), ticket.ExternalMessageIDEQ("<msg-1@contoso.com>")).
		Exist(ctx)
	require.NoError(t, err)
	require.True(t, exists, "expected ticket to be found by external_message_id")

	notExists, err := client.Ticket.Query().
		Where(ticket.TenantIDEQ(tenant.ID), ticket.ExternalMessageIDEQ("<msg-2@contoso.com>")).
		Exist(ctx)
	require.NoError(t, err)
	require.False(t, notExists, "unrelated message id must not match")
}
