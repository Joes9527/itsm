package service_request

import (
	"context"
	"entgo.io/ent/dialect/sql"
	"fmt"
	"itsm-backend/ent/predicate"
	"itsm-backend/ent/ticket"
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
func (r *EntRepository) toDomain(req *ent.ServiceRequest) (*ServiceRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("service request is missing")
	}
	wi, err := req.Edges.WorkItemOrErr()
	if err != nil {
		return nil, fmt.Errorf("service request %d requires WorkItem: %w", req.ID, err)
	}
	return &ServiceRequest{
		ID:                 req.ID,
		TenantID:           wi.TenantID,
		TicketID:           req.TicketID,
		CatalogID:          req.CatalogID,
		RequesterID:        wi.RequesterID,
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
		Version:            wi.Version,
		ProcessorID:        optionalInt(wi.AssigneeID),
		StartedAt:          itemOrNil(req.StartedAt),
		CompletedAt:        itemOrNil(req.CompletedAt),
		CompletionNote:     req.CompletionNote,
		LastError:          req.LastError,
		CreatedAt:          wi.CreatedAt,
		UpdatedAt:          wi.UpdatedAt,
		TicketTitle:        wi.Title, TicketStatus: wi.Status,
	}, nil
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

func (r *EntRepository) Get(ctx context.Context, id, tenantID int) (*ServiceRequest, error) {
	req, err := r.client.ServiceRequest.Query().
		Where(servicerequest.IDEQ(id), requestScope(tenantID)).
		WithWorkItem().Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(req)
}

func (r *EntRepository) GetByTicketID(ctx context.Context, ticketID, tenantID int) (*ServiceRequest, error) {
	sr, err := r.client.ServiceRequest.Query().
		Where(servicerequest.TicketID(ticketID), requestScope(tenantID)).
		WithWorkItem().Only(ctx)
	if err != nil {
		return nil, err
	}
	return r.toDomain(sr)
}

func (r *EntRepository) List(ctx context.Context, tenantID int, filters ListFilters) ([]*ServiceRequest, int, error) {
	query := r.client.ServiceRequest.Query().
		Where(requestScope(tenantID))

	if filters.UserID > 0 {
		query.Where(servicerequest.HasWorkItemWith(ticket.RequesterID(filters.UserID)))
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
	rows, err := query.WithWorkItem().Order(servicerequest.ByWorkItemField(ticket.FieldCreatedAt, sql.OrderDesc())).All(ctx)
	if err != nil {
		return nil, 0, err
	}

	results := make([]*ServiceRequest, len(rows))
	for i, row := range rows {
		results[i], err = r.toDomain(row)
		if err != nil {
			return nil, 0, err
		}
	}

	return results, total, nil
}

func requestScope(tenantID int) predicate.ServiceRequest {
	return servicerequest.HasWorkItemWith(ticket.TenantID(tenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil())
}

func (r *EntRepository) Update(ctx context.Context, req *ServiceRequest) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := casRequestWorkItem(ctx, tx, req, false); err != nil {
		return err
	}
	update := tx.ServiceRequest.UpdateOneID(req.ID).Where(requestScope(req.TenantID), servicerequest.TicketID(req.TicketID)).
		SetFormData(req.FormData).SetCostCenter(req.CostCenter).SetDataClassification(req.DataClassification).
		SetNeedsPublicIP(req.NeedsPublicIP).SetSourceIPWhitelist(req.SourceIPWhitelist).SetContactName(req.ContactName).SetContactEmail(req.ContactEmail).SetComplianceAck(req.ComplianceAck)
	if req.ExpireAt != nil {
		update.SetExpireAt(*req.ExpireAt)
	}
	if err := update.Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}
func casRequestWorkItem(ctx context.Context, tx *ent.Tx, req *ServiceRequest, deleted bool) error {
	// Verify extension identity before touching its owning WorkItem.
	if _, err := tx.ServiceRequest.Query().Where(servicerequest.ID(req.ID), servicerequest.TicketID(req.TicketID), requestScope(req.TenantID)).Only(ctx); err != nil {
		return err
	}
	update := tx.Ticket.UpdateOneID(req.TicketID).Where(ticket.TenantID(req.TenantID), ticket.RecordClassEQ("service_request_item"), ticket.DeletedAtIsNil(), ticket.VersionEQ(req.Version)).AddVersion(1).SetUpdatedAt(time.Now())
	if deleted {
		update.SetDeletedAt(time.Now())
	}
	return update.Exec(ctx)
}
func (r *EntRepository) Delete(ctx context.Context, req *ServiceRequest) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := casRequestWorkItem(ctx, tx, req, true); err != nil {
		return err
	}
	return tx.Commit()
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
