package controller

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/service"
)

func serializationConflictAutoAssignHandler(client *ent.Client, logger *zap.SugaredLogger, sessions *authorization.SessionReader) gin.HandlerFunc {
	smart := service.NewTicketAssignmentSmartService(client, logger, nil, nil)
	smart.SetSessionReader(sessions)
	return NewTicketAssignmentSmartController(smart, nil, logger).AutoAssign
}
