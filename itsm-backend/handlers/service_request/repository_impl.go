package service_request

import (
	"context"
	"fmt"
	"time"

	"itsm-backend/ent"
	"itsm-backend/ent/servicerequest"
	"itsm-backend/ent/user"
)

type EntRepository struct {
	client *ent.Client
}

func NewEntRepository(client *ent.Client) *EntRepository {
	return &EntRepository{client: client}
}

// toDomain converts Ent model to Domain entity
func (r *EntRepository) toDomain(req *ent.ServiceRequest) *ServiceRequest {
	if req == nil {
		return nil
	}
	return &ServiceRequest{
		ID:                 req.ID,
		TenantID:           req.TenantID,
		TicketID:           req.TicketID,
		CatalogID:          req.CatalogID,
		RequesterID:        req.RequesterID,
		CiID:               req.CiID,
		FormData:           req.FormData,
		CostCenter:         req.CostCenter,
		DataClassification: req.DataClassification,
		NeedsPublicIP:      req.NeedsPublicIP,
		SourceIPWhitelist:  req.SourceIPWhitelist,
		ExpireAt:           itemOrNil(req.ExpireAt),
		ComplianceAck:      req.ComplianceAck,
		ContactName:        req.ContactName,
		ContactEmail:       req.ContactEmail,
		Quantity:           req.Quantity,
		ExpectedAt:         itemOrNil(req.ExpectedAt),
		Version:            req.Version,
		ProcessorID:        optionalInt(req.ProcessorID),
		StartedAt:          itemOrNil(req.StartedAt),
		CompletedAt:        itemOrNil(req.CompletedAt),
		CompletionNote:     req.CompletionNote,
		LastError:          req.LastError,
		CreatedAt:          req.CreatedAt,
		UpdatedAt:          req.UpdatedAt,
	}
}

func itemOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func optionalInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func (r *EntRepository) Create(ctx context.Context, req *ServiceRequest) (*ServiceRequest, error) {
	create := r.client.ServiceRequest.Create().
		SetTenantID(req.TenantID).
		SetTicketID(req.TicketID).
		SetCatalogID(req.CatalogID).
		SetRequesterID(req.RequesterID).
		SetComplianceAck(req.ComplianceAck).
		SetNeedsPublicIP(req.NeedsPublicIP).
		SetDataClassification(req.DataClassification)

	if req.FormData != nil {
		create.SetFormData(req.FormData)
	}
	if req.CostCenter != "" {
		create.SetCostCenter(req.CostCenter)
	}
	if req.SourceIPWhitelist != nil {
		create.SetSourceIPWhitelist(req.SourceIPWhitelist)
	}
	if req.ExpireAt != nil {
		create.SetExpireAt(*req.ExpireAt)
	}
	if req.ContactName != "" {
		create.SetContactName(req.ContactName)
	}
	if req.ContactEmail != "" {
		create.SetContactEmail(req.ContactEmail)
	}
	if req.Quantity > 0 {
		create.SetQuantity(req.Quantity)
	}
	if req.ExpectedAt != nil {
		create.SetExpectedAt(*req.ExpectedAt)
	}
	if req.CiID > 0 {
		create.SetCiID(req.CiID)
	}

	savedReq, err := create.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating service request: %w", err)
	}

	return r.toDomain(savedReq), nil
}

func (r *EntRepository) Get(ctx context.Context, id, tenantID int) (*ServiceRequest, error) {
	req, err := r.client.ServiceRequest.Query().
		Where(servicerequest.IDEQ(id), servicerequest.TenantIDEQ(tenantID), servicerequest.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(req), nil
}

func (r *EntRepository) GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error) {
	sr, err := r.client.ServiceRequest.Query().
		Where(servicerequest.TicketID(ticketID), servicerequest.TenantID(tenantID), servicerequest.DeletedAtIsNil()).
		Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(sr), nil
}

func (r *EntRepository) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceRequest, int, error) {
	query := r.client.ServiceRequest.Query().
		Where(servicerequest.TenantID(tenantID), servicerequest.DeletedAtIsNil())

	if filters.UserID > 0 {
		query.Where(servicerequest.RequesterID(filters.UserID))
	}

	total, err := query.Count(ctx)
	if err != nil {
		return nil, 0, err
	}

	if filters.Page < 1 {
		filters.Page = 1
	}
	if filters.Size < 1 {
		filters.Size = 10
	}
	if filters.Size > 100 {
		filters.Size = 100
	}
	query.Limit(filters.Size).Offset((filters.Page - 1) * filters.Size)

	// Default sort by CreatedAt DESC
	rows, err := query.Order(ent.Desc(servicerequest.FieldCreatedAt)).All(ctx)
	if err != nil {
		return nil, 0, err
	}

	results := make([]*ServiceRequest, len(rows))
	for i, row := range rows {
		results[i] = r.toDomain(row)
	}

	return results, total, nil
}

func (r *EntRepository) Update(ctx context.Context, req *ServiceRequest) error {
	update := r.client.ServiceRequest.UpdateOneID(req.ID).
		Where(
			servicerequest.TenantIDEQ(req.TenantID),
			servicerequest.DeletedAtIsNil(),
			servicerequest.VersionEQ(req.Version),
		).
		SetFormData(req.FormData).
		SetCostCenter(req.CostCenter).
		SetDataClassification(req.DataClassification).
		SetNeedsPublicIP(req.NeedsPublicIP).
		SetSourceIPWhitelist(req.SourceIPWhitelist).
		SetContactName(req.ContactName).
		SetContactEmail(req.ContactEmail).
		AddVersion(1)

	if req.ExpireAt != nil {
		update.SetExpireAt(*req.ExpireAt)
	}

	return update.Exec(ctx)
}

func (r *EntRepository) Delete(ctx context.Context, req *ServiceRequest) error {
	return r.client.ServiceRequest.UpdateOneID(req.ID).
		Where(
			servicerequest.TenantIDEQ(req.TenantID),
			servicerequest.DeletedAtIsNil(),
			servicerequest.VersionEQ(req.Version),
		).
		SetDeletedAt(time.Now()).
		AddVersion(1).
		Exec(ctx)
}

// GetUserContext returns User department (needed for filtering)
// Note: This leaks abstraction slightly by querying User, but practical.
func (r *EntRepository) GetUserContext(ctx context.Context, userID, tenantID int) (string, string, error) {
	u, err := r.client.User.Query().
		Where(user.IDEQ(userID), user.TenantIDEQ(tenantID), user.ActiveEQ(true)).
		Only(ctx)
	if err != nil {
		return "", "", err
	}
	return u.Department, u.Name, nil
}
