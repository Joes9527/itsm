package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"itsm-backend/dto"
	creation "itsm-backend/handlers/common/workitemcreation"
)

func TestTicketCreationCTIContract(t *testing.T) {
	var req dto.CreateTicketRequest
	require.NoError(t, json.Unmarshal([]byte(`{"title":"Ticket","description":"Description","priority":"medium","cti":{"categoryId":11,"typeId":22,"itemId":33}}`), &req))
	command, err := ticketCreationCommand(req)
	require.NoError(t, err)
	require.Equal(t, 11, *command.CTI.CategoryID)
	require.Equal(t, 22, *command.CTI.TypeID)
	require.Equal(t, 33, *command.CTI.ItemID)
	legacy := 33
	req.CategoryID = &legacy
	_, err = ticketCreationCommand(req)
	require.NoError(t, err)
	legacy = 44
	_, err = ticketCreationCommand(req)
	require.Error(t, err)
	req.CTI = nil
	command, err = ticketCreationCommand(req)
	require.NoError(t, err)
	require.Equal(t, &creation.CTIInput{CategoryID: &legacy}, command.CTI)
}
