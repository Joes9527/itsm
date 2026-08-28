package service

import "itsm-backend/ent"

// ActionActor describes the caller context used to authorize an action.
type ActionActor struct {
	Client   *ent.Client
	TenantID int
	UserID   int
	Role     string
}
