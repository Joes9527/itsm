package intake

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"io"
	"itsm-backend/authentication"
	"itsm-backend/common"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"itsm-backend/middleware"
	"net/http"
	"reflect"
	"strings"
)

type Handler struct {
	exchange    *IdentityExchangeService
	application creation.Application
	readers     *ReadService
	mappings    *IdentityMappingService
}

func NewHandler(exchange *IdentityExchangeService, application creation.Application) *Handler {
	return &Handler{exchange: exchange, application: application}
}
func (h *Handler) SetReaders(readers *ReadService)              { h.readers = readers }
func (h *Handler) SetMappings(mappings *IdentityMappingService) { h.mappings = mappings }
func (h *Handler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST("/intake/identity-exchange", h.exchangeHandler("create"))
	group.POST("/intake/identity-exchange/read", h.exchangeHandler("read"))
	secret := ""
	if h.exchange != nil {
		secret = h.exchange.jwtSecret
	}
	group.POST("/intake/work-items", middleware.IntakeAuthMiddleware(secret, "intake:create", h.exchange.ValidateCredential), h.CreateWorkItem)
	group.GET("/intake/catalog-items", middleware.IntakeAuthMiddleware(secret, "intake:catalog:read", h.exchange.ValidateCredential), h.CatalogPage)
	group.GET("/intake/catalog-items/:id", middleware.IntakeAuthMiddleware(secret, "intake:catalog:read", h.exchange.ValidateCredential), h.CatalogDetail)
	group.GET("/intake/work-items/:id", middleware.IntakeAuthMiddleware(secret, "intake:workitem:read", h.exchange.ValidateCredential), h.WorkItem)
}
func decodeIdentityBody(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32*1024)
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return creation.NewInvalidCommand("invalid identity body", creation.FieldError{}, nil)
	}
	object, objectErr := common.DecodeJSONObject(raw)
	if objectErr != nil {
		return creation.NewInvalidCommand("invalid identity object", creation.FieldError{}, nil)
	}
	fields := map[string]bool{}
	t := reflect.TypeOf(target).Elem()
	for index := 0; index < t.NumField(); index++ {
		name := strings.Split(t.Field(index).Tag.Get("json"), ",")[0]
		fields[name] = true
	}
	for name := range object {
		if !fields[name] {
			return creation.NewInvalidCommand("unknown identity field", creation.FieldError{}, nil)
		}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(target); err != nil {
		return creation.NewInvalidCommand("invalid identity fields", creation.FieldError{}, nil)
	}
	return nil
}
func (h *Handler) exchangeHandler(purpose string) gin.HandlerFunc {
	return func(c *gin.Context) {
		var a IdentityAssertion
		if err := decodeIdentityBody(c, &a); err != nil {
			intakehttp.Fail(c, err)
			return
		}
		result, err := h.exchange.Exchange(c.Request.Context(), a, purpose)
		if err != nil {
			intakehttp.Fail(c, err)
			return
		}
		common.Success(c, result)
	}
}
func (h *Handler) CreateWorkItem(c *gin.Context) {
	identity := c.MustGet("intake_identity").(creation.Identity)
	identity.CatalogOptionKeys = true
	claims := c.MustGet("intake_claims").(*authentication.IntakeClaims)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	command, err := creation.DecodeCreateWorkItemCommand(c.Request.Body)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	if command.SourceReference != nil && (command.SourceReference.Provider != claims.Provider || command.SourceReference.EventID != claims.EventID || command.SourceReference.ConversationID != "") {
		intakehttp.Fail(c, creation.NewPermissionDenied("source reference differs from verified identity", nil))
		return
	}
	command.SourceReference = &creation.SourceReference{Provider: claims.Provider, EventID: claims.EventID}
	if h.application == nil {
		intakehttp.Fail(c, creation.NewInfrastructureUnavailable("intake application unavailable", nil))
		return
	}
	result, err := h.application.Create(WithRequestID(c.Request.Context(), c.GetString("request_id")), identity, command)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	c.JSON(status, gin.H{"code": 0, "message": "success", "data": result})
}
