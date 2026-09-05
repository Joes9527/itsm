package service

import (
	"context"
	"fmt"
	"net/mail"

	"itsm-backend/ent"
	"itsm-backend/ent/user"
)

func executeIncidentRuleAction(ctx context.Context, client *ent.Client, action RuleAction, incident *ent.Incident, tenantID int) error {
	if client == nil {
		return fmt.Errorf("incident rule action database unavailable")
	}
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := action.ExecuteTx(ctx, tx, incident, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

// Configured rule recipients are active users of this tenant. Arbitrary addresses
// remain an alert API concern and cannot be smuggled into creation automation.
func validateIncidentRuleRecipients(ctx context.Context, client *ent.Client, recipients []string, tenantID int) error {
	if len(recipients) == 0 {
		return fmt.Errorf("configured notification recipients are required")
	}
	seen := map[string]bool{}
	for _, address := range recipients {
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address || seen[address] {
			return fmt.Errorf("invalid or duplicate configured notification recipient")
		}
		seen[address] = true
		count, err := client.User.Query().Where(user.TenantID(tenantID), user.Active(true), user.Email(address)).Count(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("notification recipient is not a unique active tenant user")
		}
	}
	return nil
}
func incidentRuleUserRecipients(ctx context.Context, client *ent.Client, ids []int, tenantID int) ([]string, error) {
	var recipients []string
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate notification user")
		}
		seen[id] = true
		recipient, err := client.User.Query().Where(user.ID(id), user.TenantID(tenantID), user.Active(true)).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("notification user not found or inactive: %w", err)
		}
		recipients = append(recipients, recipient.Email)
	}
	if len(recipients) > 0 {
		if err := validateIncidentRuleRecipients(ctx, client, recipients, tenantID); err != nil {
			return nil, err
		}
	}
	return recipients, nil
}
