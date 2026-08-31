package intake

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/externalidentity"
	"itsm-backend/ent/user"

	"github.com/gin-gonic/gin"
)

type ExternalIdentityMappingResponse struct {
	ID        int       `json:"id"`
	Provider  string    `json:"provider"`
	Workspace string    `json:"workspace"`
	Subject   string    `json:"subject"`
	UserID    int       `json:"userId"`
	Active    bool      `json:"active"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type CreateExternalIdentityMappingRequest struct {
	Provider  string `json:"provider"`
	Workspace string `json:"workspace"`
	Subject   string `json:"subject"`
	UserID    int    `json:"userId"`
}

type DisableExternalIdentityMappingRequest struct {
	Version int `json:"version"`
}

type IdentityMappingHandler struct{ client *ent.Client }

func NewIdentityMappingHandler(client *ent.Client) *IdentityMappingHandler {
	return &IdentityMappingHandler{client: client}
}

func mappingResponse(mapping *ent.ExternalIdentity) ExternalIdentityMappingResponse {
	return ExternalIdentityMappingResponse{
		ID: mapping.ID, Provider: mapping.Provider, Workspace: mapping.Workspace, Subject: mapping.Subject,
		UserID: mapping.UserID, Active: mapping.Active, Version: mapping.Version,
		CreatedAt: mapping.CreatedAt, UpdatedAt: mapping.UpdatedAt,
	}
}

func mappingFailure(c *gin.Context, status int, code, message string) {
	common.TypedFail(c, status, code, message, false, nil)
}

func mappingIdentity(c *gin.Context) (tenantID, actorID int, ok bool) {
	tenantID, actorID = c.GetInt("tenant_id"), c.GetInt("user_id")
	return tenantID, actorID, tenantID > 0 && actorID > 0
}

func writeMappingAudit(c *gin.Context, tx *ent.Tx, tenantID, actorID, status int, action string, mappingID int) error {
	body, _ := json.Marshal(map[string]int{"mappingId": mappingID})
	_, err := tx.AuditLog.Create().
		SetTenantID(tenantID).SetUserID(actorID).SetRequestID(c.GetString("request_id")).SetIP(c.ClientIP()).
		SetResource("intake_external_identity").SetAction(action).SetPath(c.Request.URL.Path).
		SetMethod(c.Request.Method).SetStatusCode(status).SetRequestBody(string(body)).Save(c.Request.Context())
	return err
}

func validMappingCreate(req CreateExternalIdentityMappingRequest) bool {
	for _, value := range []string{req.Provider, req.Workspace, req.Subject} {
		length := len(strings.TrimSpace(value))
		if length == 0 || length > 512 {
			return false
		}
	}
	return req.UserID > 0
}

func decodeSingleMappingJSON(c *gin.Context, target any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxIdentityAssertionBodyBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON value")
	}
	return nil
}

func (h *IdentityMappingHandler) List(c *gin.Context) {
	tenantID, _, ok := mappingIdentity(c)
	if !ok || h == nil || h.client == nil {
		mappingFailure(c, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authenticated tenant identity is required")
		return
	}
	mappings, err := h.client.ExternalIdentity.Query().
		Where(externalidentity.TenantIDEQ(tenantID)).
		Order(ent.Desc(externalidentity.FieldCreatedAt), ent.Desc(externalidentity.FieldID)).All(c.Request.Context())
	if err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	result := make([]ExternalIdentityMappingResponse, 0, len(mappings))
	for _, mapping := range mappings {
		result = append(result, mappingResponse(mapping))
	}
	common.Success(c, result)
}

func (h *IdentityMappingHandler) Create(c *gin.Context) {
	tenantID, actorID, ok := mappingIdentity(c)
	if !ok || h == nil || h.client == nil {
		mappingFailure(c, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authenticated tenant identity is required")
		return
	}
	var req CreateExternalIdentityMappingRequest
	if err := decodeSingleMappingJSON(c, &req); err != nil || !validMappingCreate(req) {
		mappingFailure(c, http.StatusBadRequest, "INVALID_IDENTITY_MAPPING", "identity mapping is invalid")
		return
	}
	tx, err := h.client.Tx(c.Request.Context())
	if err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	defer tx.Rollback()
	if _, err := tx.User.Query().Where(user.IDEQ(req.UserID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).Only(c.Request.Context()); err != nil {
		mappingFailure(c, http.StatusNotFound, "TARGET_USER_NOT_FOUND", "target user was not found")
		return
	}
	created, err := tx.ExternalIdentity.Create().
		SetTenantID(tenantID).SetProvider(strings.TrimSpace(req.Provider)).SetWorkspace(strings.TrimSpace(req.Workspace)).
		SetSubject(strings.TrimSpace(req.Subject)).SetUserID(req.UserID).Save(c.Request.Context())
	if err != nil {
		if ent.IsConstraintError(err) {
			mappingFailure(c, http.StatusConflict, "IDENTITY_MAPPING_EXISTS", "external identity is already mapped")
			return
		}
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	if err := writeMappingAudit(c, tx, tenantID, actorID, http.StatusCreated, "intake.identity_mapping.created", created.ID); err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE", "identity mapping audit is unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	common.SuccessWithStatus(c, http.StatusCreated, mappingResponse(created))
}

func (h *IdentityMappingHandler) Disable(c *gin.Context) {
	tenantID, actorID, ok := mappingIdentity(c)
	if !ok || h == nil || h.client == nil {
		mappingFailure(c, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "authenticated tenant identity is required")
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		mappingFailure(c, http.StatusBadRequest, "INVALID_IDENTITY_MAPPING", "identity mapping id is invalid")
		return
	}
	var req DisableExternalIdentityMappingRequest
	if err := decodeSingleMappingJSON(c, &req); err != nil || req.Version <= 0 {
		mappingFailure(c, http.StatusBadRequest, "INVALID_IDENTITY_MAPPING_VERSION", "identity mapping version is required")
		return
	}
	tx, err := h.client.Tx(c.Request.Context())
	if err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	defer tx.Rollback()
	current, err := tx.ExternalIdentity.Query().Where(externalidentity.IDEQ(id), externalidentity.TenantIDEQ(tenantID)).Only(c.Request.Context())
	if err != nil {
		mappingFailure(c, http.StatusNotFound, "IDENTITY_MAPPING_NOT_FOUND", "identity mapping was not found")
		return
	}
	if current.Version != req.Version {
		mappingFailure(c, http.StatusConflict, "IDENTITY_MAPPING_VERSION_CONFLICT", "identity mapping version has changed")
		return
	}
	updated, err := tx.ExternalIdentity.UpdateOneID(id).
		Where(externalidentity.TenantIDEQ(tenantID), externalidentity.VersionEQ(req.Version)).
		SetActive(false).SetVersion(req.Version + 1).Save(c.Request.Context())
	if err != nil {
		if ent.IsNotFound(err) {
			mappingFailure(c, http.StatusConflict, "IDENTITY_MAPPING_VERSION_CONFLICT", "identity mapping version has changed")
			return
		}
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	if err := writeMappingAudit(c, tx, tenantID, actorID, http.StatusOK, "intake.identity_mapping.disabled", updated.ID); err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "AUDIT_UNAVAILABLE", "identity mapping audit is unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		mappingFailure(c, http.StatusServiceUnavailable, "IDENTITY_MAPPING_UNAVAILABLE", "identity mappings are temporarily unavailable")
		return
	}
	common.Success(c, mappingResponse(updated))
}
