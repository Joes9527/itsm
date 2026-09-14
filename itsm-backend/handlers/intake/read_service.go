package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"itsm-backend/authorization"
	"itsm-backend/common"
	"itsm-backend/ent"
	"itsm-backend/ent/ticket"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/handlers/common/intakehttp"
	creation "itsm-backend/handlers/common/workitemcreation"
	"net/url"

	"strconv"
	"strings"
)

type CatalogReader interface {
	ListAvailableForIntake(context.Context, *authorization.SessionSnapshot, int, string, int) ([]*creation.CatalogReadDefinition, error)
	ReadAvailableForIntake(context.Context, *authorization.SessionSnapshot, int) (*creation.CatalogReadDefinition, error)
}
type FulfillmentReader interface {
	ReadFulfillment(context.Context, *ent.Client, *ent.Ticket) (accessgrant.Fulfillment, error)
}
type ReferenceReadOptions struct {
	FrontendURL string
	PageSize    int
	Lifecycle   authorization.WorkItemLifecycleReader
}

type ReadService struct {
	references   ReferenceReadOptions
	fulfillment  FulfillmentReader
	sessions     *authorization.SessionReader
	catalog      CatalogReader
	cursorSecret string
}

func (s *ReadService) SetFulfillmentReader(owner FulfillmentReader) { s.fulfillment = owner }

func NewReadService(sessions *authorization.SessionReader, c CatalogReader, secret string, references ReferenceReadOptions) *ReadService {
	return &ReadService{sessions: sessions, catalog: c, cursorSecret: secret, references: references}
}

type CatalogSummary struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TargetClass string `json:"targetClass"`
}
type CatalogPage struct {
	Items      []CatalogSummary `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}
type CatalogOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}
type CatalogField struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Type     string          `json:"type"`
	Required bool            `json:"required"`
	ReadOnly bool            `json:"readOnly"`
	Options  []CatalogOption `json:"options"`
}
type CatalogContract struct {
	CatalogSummary
	CatalogVersion    string         `json:"catalogVersion"`
	FormSchemaVersion string         `json:"formSchemaVersion"`
	Fields            []CatalogField `json:"fields"`
}
type WorkItemView struct {
	WorkItemID       int               `json:"workItemId"`
	Number           string            `json:"number"`
	RecordClass      string            `json:"recordClass"`
	Status           string            `json:"status"`
	Version          int               `json:"version"`
	FulfillmentState string            `json:"fulfillmentState"`
	AccessResult     *accessgrant.View `json:"accessResult"`
}
type catalogCursor struct {
	TenantID int    `json:"tenantId"`
	ActorID  int    `json:"actorId"`
	Query    string `json:"query"`
	After    int    `json:"after"`
}

func (s *ReadService) encodeCursor(value catalogCursor) string {
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, []byte(s.cursorSecret))
	mac.Write(append([]byte("intake-catalog-cursor-v1\x00"), raw...))
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *ReadService) decodeCursor(cursor string, i creation.Identity, q string) (int, error) {
	invalid := creation.NewInvalidCommand("invalid catalog cursor", creation.FieldError{Field: "cursor", Message: "cursor is invalid for this query"}, nil)
	if len(cursor) > 4096 {
		return 0, invalid
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 {
		return 0, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, invalid
	}
	var value catalogCursor
	if json.Unmarshal(raw, &value) != nil || value.After < 1 || value.TenantID != i.TenantID || value.ActorID != i.ActorID || value.Query != q {
		return 0, invalid
	}
	if !hmac.Equal([]byte(cursor), []byte(s.encodeCursor(value))) {
		return 0, invalid
	}
	return value.After, nil
}
func (s *ReadService) List(ctx context.Context, i creation.Identity, q, cursor string) (*CatalogPage, error) {
	if s == nil || s.sessions == nil || s.catalog == nil || s.cursorSecret == "" {
		return nil, creation.NewInfrastructureUnavailable("catalog reader unavailable", nil)
	}
	if len(q) > 256 {
		return nil, creation.NewInvalidCommand("catalog query too long", creation.FieldError{}, nil)
	}
	after := 0
	var err error
	if cursor != "" {
		after, err = s.decodeCursor(cursor, i, q)
		if err != nil {
			return nil, err
		}
	}
	result := &CatalogPage{Items: []CatalogSummary{}}
	err = s.sessions.Read(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		rows, err := s.catalog.ListAvailableForIntake(ctx, snapshot, after, q, 51)
		if err != nil {
			return err
		}
		if len(rows) > 50 {
			value := s.encodeCursor(catalogCursor{TenantID: i.TenantID, ActorID: i.ActorID, Query: q, After: rows[49].ID})
			result.NextCursor = &value
			rows = rows[:50]
		}
		for _, row := range rows {
			result.Items = append(result.Items, CatalogSummary{ID: row.ID, Name: row.Name, Description: row.Description, TargetClass: row.TargetClass})
		}
		return nil
	})
	return result, err
}
func (s *ReadService) Detail(ctx context.Context, i creation.Identity, id int) (*CatalogContract, error) {
	if s == nil || s.sessions == nil || s.catalog == nil {
		return nil, creation.NewInfrastructureUnavailable("catalog reader unavailable", nil)
	}
	var result *CatalogContract
	err := s.sessions.Read(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		row, err := s.catalog.ReadAvailableForIntake(ctx, snapshot, id)
		if err != nil {
			return err
		}
		result = &CatalogContract{CatalogSummary: CatalogSummary{ID: row.ID, Name: row.Name, Description: row.Description, TargetClass: row.TargetClass}, CatalogVersion: row.CatalogVersion, FormSchemaVersion: row.FormSchemaVersion, Fields: []CatalogField{}}
		for _, f := range row.Fields {
			if !containsIdentityValue([]string{"text", "textarea", "number", "date", "select", "multiselect", "boolean", "file"}, f.FieldType) {
				return creation.NewDomainValidationFailed("unsupported catalog field type", nil)
			}
			field := CatalogField{Key: f.Name, Label: f.Label, Type: f.FieldType, Required: f.Required, ReadOnly: false, Options: []CatalogOption{}}
			for _, option := range f.Options {
				field.Options = append(field.Options, CatalogOption{Key: option.Key, Label: option.Label})
			}
			result.Fields = append(result.Fields, field)
		}
		return nil
	})
	return result, err
}
func (s *ReadService) WorkItem(ctx context.Context, i creation.Identity, id int) (*WorkItemView, error) {
	if s == nil || s.sessions == nil {
		return nil, creation.NewInfrastructureUnavailable("work item reader unavailable", nil)
	}
	var result *WorkItemView
	err := s.sessions.Read(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		item, policy, err := authorization.ResolveWorkItemIdentity(ctx, snapshot.Tx.Client(), id, i.TenantID)
		if err != nil {
			appErr, ok := common.AsAppError(err)
			if ok && appErr.Code == common.ErrCodeNotFound {
				return creation.NewReferenceNotFound("work item unavailable", nil)
			}
			if ok && appErr.Code == common.ErrCodeForbidden {
				return creation.NewPermissionDenied("work item class unavailable", nil)
			}
			return creation.NewInfrastructureUnavailable("work item lookup unavailable", err)
		}
		if err = authorization.RequireCurrentPermission(ctx, snapshot.Tx, i, policy.Resource, policy.ResolveAction("read")); err != nil {
			return err
		}
		// This requester-facing projection is intentionally limited to the current
		// requester's own work. Operator breadth belongs to the professional APIs.
		if item.RequesterID != i.ActorID {
			return creation.NewReferenceNotFound("work item unavailable", nil)
		}
		result = &WorkItemView{WorkItemID: item.ID, Number: item.TicketNumber, RecordClass: item.RecordClass, Status: item.Status, Version: item.Version, FulfillmentState: "unknown", AccessResult: nil}
		if item.RecordClass == creation.RecordClassServiceRequestItem {
			if s.fulfillment == nil {
				return creation.NewInfrastructureUnavailable("requested item fulfillment owner unavailable", nil)
			}
			projection, err := s.fulfillment.ReadFulfillment(ctx, snapshot.Tx.Client(), item)
			if err != nil {
				return creation.NewInfrastructureUnavailable("requested item fulfillment unavailable", err)
			}
			result.FulfillmentState = projection.State
			result.AccessResult = projection.AccessResult
		}
		return nil
	})
	return result, err
}
func (h *Handler) CatalogPage(c *gin.Context) {
	for k, v := range c.Request.URL.Query() {
		if (k != "q" && k != "cursor") || len(v) != 1 {
			intakehttp.Fail(c, creation.NewInvalidCommand("invalid query parameter", creation.FieldError{}, nil))
			return
		}
	}
	result, err := h.readers.List(c.Request.Context(), c.MustGet("intake_identity").(creation.Identity), c.Query("q"), c.Query("cursor"))
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	common.Success(c, result)
}
func (h *Handler) CatalogDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		intakehttp.Fail(c, creation.NewInvalidCommand("invalid catalog id", creation.FieldError{}, nil))
		return
	}
	result, err := h.readers.Detail(c.Request.Context(), c.MustGet("intake_identity").(creation.Identity), id)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	common.Success(c, result)
}
func (h *Handler) WorkItem(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		intakehttp.Fail(c, creation.NewInvalidCommand("invalid work item id", creation.FieldError{}, nil))
		return
	}
	result, err := h.readers.WorkItem(c.Request.Context(), c.MustGet("intake_identity").(creation.Identity), id)
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	common.Success(c, result)
}

// Reference queries use the same current session snapshot for scope, ACL and
// lifecycle projections. A reference never grants write authority.
func (s *ReadService) validateReferences() error {
	if s == nil || s.sessions == nil || s.cursorSecret == "" || s.references.PageSize < 1 || missingDependency(s.references.Lifecycle) {
		return creation.NewInfrastructureUnavailable("work item reference reader unavailable", nil)
	}
	if _, err := ticketReferenceURL(s.references.FrontendURL, 1); err != nil {
		return creation.NewInfrastructureUnavailable("work item reference URL unavailable", err)
	}
	return nil
}

type referenceCursor struct {
	TenantID int    `json:"tenantId"`
	ActorID  int    `json:"actorId"`
	Mode     string `json:"mode"`
	After    int    `json:"after"`
}

func (s *ReadService) encodeReferenceCursor(value referenceCursor) string {
	raw, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, []byte(s.cursorSecret))
	mac.Write(append([]byte("intake-work-item-reference-cursor-v1\x00"), raw...))
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *ReadService) decodeReferenceCursor(cursor string, i creation.Identity) (int, error) {
	invalid := creation.NewInvalidCommand("invalid work item reference cursor", creation.FieldError{Field: "cursor", Message: "cursor is invalid for this query"}, nil)
	if len(cursor) > 4096 {
		return 0, invalid
	}
	parts := strings.Split(cursor, ".")
	if len(parts) != 2 {
		return 0, invalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, invalid
	}
	var value referenceCursor
	if json.Unmarshal(raw, &value) != nil || value.After < 1 || value.TenantID != i.TenantID || value.ActorID != i.ActorID || value.Mode != "unfinished" {
		return 0, invalid
	}
	if !hmac.Equal([]byte(cursor), []byte(s.encodeReferenceCursor(value))) {
		return 0, invalid
	}
	return value.After, nil
}
func referenceReadable(snapshot *authorization.SessionSnapshot, item *ent.Ticket) (bool, error) {
	policy, err := authorization.ResolveWorkItemPolicy(item.RecordClass)
	if err != nil {
		return false, creation.NewInfrastructureUnavailable("work item policy unavailable", err)
	}
	return authorization.CheckPermissionMatch(snapshot.Permissions, policy.Resource, policy.ResolveAction("read")), nil
}
func (s *ReadService) ReferenceByNumber(ctx context.Context, i creation.Identity, number string) (*WorkItemReference, error) {
	if err := s.validateReferences(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(number) == "" {
		return nil, creation.NewInvalidCommand("number is required", creation.FieldError{Field: "number", Message: "number must not be blank"}, nil)
	}
	var result *WorkItemReference
	err := s.sessions.Read(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		item, err := snapshot.Tx.Client().Ticket.Query().Where(ticket.TenantIDEQ(i.TenantID), ticket.RequesterIDEQ(i.ActorID), ticket.DeletedAtIsNil(), ticket.TicketNumberEQ(number)).Only(ctx)
		if ent.IsNotFound(err) {
			return creation.NewReferenceNotFound("work item unavailable", nil)
		}
		if err != nil {
			return creation.NewInfrastructureUnavailable("work item lookup unavailable", err)
		}
		readable, err := referenceReadable(snapshot, item)
		if err != nil {
			return err
		}
		if !readable {
			return creation.NewReferenceNotFound("work item unavailable", nil)
		}
		reference, err := projectWorkItemReference(s.references.FrontendURL, item)
		if err != nil {
			return creation.NewInfrastructureUnavailable("work item reference unavailable", err)
		}
		result = &reference
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (s *ReadService) UnfinishedReferences(ctx context.Context, i creation.Identity, cursor string) (*WorkItemReferencePage, error) {
	if err := s.validateReferences(); err != nil {
		return nil, err
	}
	after := 0
	if cursor != "" {
		var err error
		after, err = s.decodeReferenceCursor(cursor, i)
		if err != nil {
			return nil, err
		}
	}
	result := &WorkItemReferencePage{Items: []WorkItemReference{}}
	err := s.sessions.Read(ctx, i, func(snapshot *authorization.SessionSnapshot) error {
		client := snapshot.Tx.Client()
		for {
			rows, err := client.Ticket.Query().Where(ticket.TenantIDEQ(i.TenantID), ticket.RequesterIDEQ(i.ActorID), ticket.DeletedAtIsNil(), ticket.IDGT(after)).Order(ent.Asc(ticket.FieldID)).Limit(s.references.PageSize).All(ctx)
			if err != nil {
				return creation.NewInfrastructureUnavailable("work item list unavailable", err)
			}
			for _, item := range rows {
				readable, err := referenceReadable(snapshot, item)
				if err != nil {
					return err
				}
				if readable {
					unfinished, err := s.references.Lifecycle.IsUnfinished(ctx, client, item)
					if err != nil {
						return creation.NewInfrastructureUnavailable("work item lifecycle unavailable", err)
					}
					if unfinished {
						// Look ahead until another visible unfinished record exists. Keep the
						// cursor before that record so the next page cannot lose it.
						if len(result.Items) == s.references.PageSize {
							value := s.encodeReferenceCursor(referenceCursor{TenantID: i.TenantID, ActorID: i.ActorID, Mode: "unfinished", After: after})
							result.NextCursor = &value
							return nil
						}
						reference, err := projectWorkItemReference(s.references.FrontendURL, item)
						if err != nil {
							return creation.NewInfrastructureUnavailable("work item reference unavailable", err)
						}
						result.Items = append(result.Items, reference)
					}
				}
				after = item.ID
			}
			if len(rows) < s.references.PageSize {
				return nil
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (h *Handler) WorkItemReferences(c *gin.Context) {
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		intakehttp.Fail(c, creation.NewInvalidCommand("invalid query parameters", creation.FieldError{}, nil))
		return
	}
	for key, values := range query {
		if (key != "number" && key != "cursor") || len(values) != 1 {
			intakehttp.Fail(c, creation.NewInvalidCommand("invalid query parameter", creation.FieldError{}, nil))
			return
		}
	}
	_, hasNumber := query["number"]
	_, hasCursor := query["cursor"]
	if hasNumber && hasCursor {
		intakehttp.Fail(c, creation.NewInvalidCommand("number and cursor cannot be combined", creation.FieldError{}, nil))
		return
	}
	identity := c.MustGet("intake_identity").(creation.Identity)
	var result any
	if hasNumber {
		result, err = h.readers.ReferenceByNumber(c.Request.Context(), identity, query.Get("number"))
	} else {
		result, err = h.readers.UnfinishedReferences(c.Request.Context(), identity, query.Get("cursor"))
	}
	if err != nil {
		intakehttp.Fail(c, err)
		return
	}
	common.Success(c, result)
}
