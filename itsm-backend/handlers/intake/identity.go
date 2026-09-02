package intake

import "strings"

type Identity struct {
	TenantID    int
	ActorID     int
	RequesterID int
	Role        string
	Channel     string
	Provider    string
	TokenID     string
}

func (i Identity) ValidateCommand(command CreateWorkItemCommand) error {
	if i.TenantID <= 0 || i.ActorID <= 0 || i.RequesterID <= 0 || strings.TrimSpace(i.Role) == "" || strings.TrimSpace(i.Channel) == "" {
		return NewAuthenticationRequired("authenticated intake identity is required", nil)
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
