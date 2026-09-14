package workitemassignment

import (
	"context"
	"itsm-backend/authorization"
	"itsm-backend/ent"
)

// Owner applies manual ownership through the professional application service.
// The verified session owns the transaction; implementations never commit it.
type Owner interface {
	RecordClass() string
	AssignWorkItem(context.Context, *authorization.SessionSnapshot, *ent.Ticket, int, string) (*ent.Ticket, error)
}
