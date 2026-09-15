package controller

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"itsm-backend/authorization"
	"itsm-backend/ent"
	"itsm-backend/service"
)

func serializationConflictAcceptTicketHandler(client *ent.Client, logger *zap.SugaredLogger, sessions *authorization.SessionReader) gin.HandlerFunc {
	workflow := service.NewTicketWorkflowService(client, logger)
	workflow.SetSessionReader(sessions)
	return NewTicketWorkflowController(workflow, nil, logger).AcceptTicket
}
