package workitemcreation

import "strings"

// Identity is constructed only by authenticated or verified internal adapters.
// Validation below checks structure/source consistency; authentication, mapping
// versions, tenant membership and permissions remain application prerequisites.
type Identity struct {
	// CatalogOptionKeys is server-only adapter metadata, never a public command field.
	CatalogOptionKeys bool
	TenantID          int
	ActorTenantID     int // server-derived native provenance; never adapter authority
	ActorID           int
	RequesterID       int
	Role              string
	Channel           string
	Provider          string
	TokenID           string
}

func (i Identity) ValidateCommand(command CreateWorkItemCommand) error {
	if i.TenantID <= 0 || i.ActorID <= 0 || i.RequesterID <= 0 || strings.TrimSpace(i.Role) == "" || strings.TrimSpace(i.Channel) == "" {
		return NewAuthenticationRequired("authenticated intake identity is required", nil)
	}
	if command.FeishuTask != nil && (i.Channel != "feishu" || i.Provider != "feishu" || command.RecordClass != RecordClassGeneric || command.SourceReference == nil) {
		return NewPermissionDenied("verified Feishu source is required", nil)
	}
	if command.Email != nil && (i.Channel != "email" || i.Provider != "msgraph_email" || command.RecordClass != RecordClassGeneric || command.SourceReference == nil) {
		return NewPermissionDenied("verified MS Graph email source is required", nil)
	}
	if command.SourceReference == nil {
		return nil
	}

	expectedProvider := strings.TrimSpace(i.Provider)
	if expectedProvider == "" {
		expectedProvider = strings.TrimSpace(i.Channel)
	}
	provided := strings.TrimSpace(command.SourceReference.Provider)
	if expectedProvider == "" || provided != expectedProvider {
		return NewPermissionDenied("source provider does not match the authenticated channel", nil)
	}
	return nil
}

// ActorProvenance is redacted audit evidence, never mutable identity authority.
type ActorProvenance struct {
	ActorUserID     int `json:"actorUserId"`
	ActorTenantID   int `json:"actorTenantId"`
	TargetTenantID  int `json:"targetTenantId"`
	IntakeRequestID int `json:"intakeRequestId"`
	WorkItemID      int `json:"workItemId"`
}
