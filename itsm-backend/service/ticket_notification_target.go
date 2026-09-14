package service

import (
	"context"
	"encoding/hex"
	"strings"

	"itsm-backend/common/executionscope"
	"itsm-backend/connector"
	"itsm-backend/ent"
)

func notificationConnectorChannel(channel string) bool {
	switch channel {
	case "sms", "webhook", "feishu", "dingtalk", "wecom":
		return true
	}
	return false
}

// Select only while producing a new intent. Delivery and replay never select
// from current configuration again. Multiple enabled providers are ambiguous.
func (s *TicketNotificationService) BindNotificationTargetTx(ctx context.Context, tx *ent.Tx, tenantID int, channel string, create *ent.TicketNotificationCreate) error {
	if s == nil || tx == nil || create == nil {
		return executionscope.ErrDenied
	}
	if channel == "email" {
		if s.emailService == nil {
			return executionscope.ErrDenied
		}
		target, err := s.emailService.DescribeDeliveryTarget(ctx, tx, tenantID, "notification")
		if err != nil {
			return err
		}
		if err := target.Validate(); err != nil {
			return err
		}
		create.SetTargetProtocolVersion(target.ProtocolVersion).SetTargetTransport(target.Transport).SetTargetDestinationDigest(target.DestinationDigest)
		if target.Transport == "graph" {
			create.SetTargetConnectorName(target.ConnectorName).SetTargetConnectorProvider(target.ConnectorProvider)
		}
		return nil
	}
	if !notificationConnectorChannel(channel) {
		if channel == "in_app" || channel == "push" {
			return nil
		}
		return executionscope.ErrDenied
	}
	if s == nil || s.execution == nil || s.connectorManager == nil || create == nil {
		return executionscope.ErrDenied
	}
	if err := s.execution.RequireCapability(ctx, tenantID, "notification"); err != nil {
		return err
	}
	ref, err := s.execution.EventRef(tenantID)
	if err != nil {
		return err
	}
	var selected *connector.Config
	for _, cfg := range s.connectorManager.ListByTenant(tenantID) {
		if !cfg.Enabled || cfg.Name != channel {
			continue
		}
		if selected != nil {
			return executionscope.ErrDenied
		}
		copy := cfg
		selected = &copy
	}
	if selected == nil {
		return executionscope.ErrDenied
	}
	_, _, digest, err := s.connectorManager.ResolveDeliveryTarget(ctx, ref, "notification", selected.Name, selected.Provider)
	if err != nil {
		return err
	}
	if !notificationTargetDigestValid(digest) {
		return executionscope.ErrDenied
	}
	create.SetTargetProtocolVersion(1).SetTargetConnectorName(selected.Name).SetTargetConnectorProvider(selected.Provider).SetTargetDestinationDigest(digest)
	return nil
}

func notificationTargetDigestValid(digest string) bool {
	if len(digest) != 64 || strings.ToLower(digest) != digest {
		return false
	}
	_, err := hex.DecodeString(digest)
	return err == nil
}

func validateNotificationConnectorTarget(row *ent.TicketNotification) error {
	if row == nil {
		return executionscope.ErrDenied
	}
	if row.Channel == "email" {
		_, err := notificationEmailTarget(row)
		return err
	}
	if row.TargetTransport != nil {
		return executionscope.ErrDenied
	}
	if !notificationConnectorChannel(row.Channel) {
		if row.Channel != "in_app" && row.Channel != "email" && row.Channel != "push" {
			return executionscope.ErrDenied
		}
		if row.TargetProtocolVersion != nil || row.TargetConnectorName != nil || row.TargetConnectorProvider != nil || row.TargetDestinationDigest != nil {
			return executionscope.ErrDenied
		}
		return nil
	}
	if row.TargetProtocolVersion == nil || *row.TargetProtocolVersion != 1 || row.TargetConnectorName == nil || *row.TargetConnectorName != row.Channel || row.TargetConnectorProvider == nil || strings.TrimSpace(*row.TargetConnectorProvider) == "" || strings.TrimSpace(*row.TargetConnectorProvider) != *row.TargetConnectorProvider || row.TargetDestinationDigest == nil || !notificationTargetDigestValid(*row.TargetDestinationDigest) {
		return executionscope.ErrDenied
	}
	return nil
}

func (s *TicketNotificationService) resolveNotificationConnectorTarget(ctx context.Context, row *ent.TicketNotification) (connector.Connector, uint64, error) {
	if err := validateNotificationConnectorTarget(row); err != nil {
		return nil, 0, err
	}
	if !notificationConnectorChannel(row.Channel) || s.execution == nil || s.connectorManager == nil {
		return nil, 0, executionscope.ErrDenied
	}
	ref, err := s.execution.EventRef(row.TenantID)
	if err != nil {
		return nil, 0, err
	}
	target, generation, digest, err := s.connectorManager.ResolveDeliveryTarget(ctx, ref, "notification", *row.TargetConnectorName, *row.TargetConnectorProvider)
	if err != nil {
		return nil, 0, err
	}
	if digest != *row.TargetDestinationDigest {
		return nil, 0, executionscope.ErrDenied
	}
	return target, generation, nil
}

// Decode only persisted identity. Legacy empty targets never select current configuration.
func notificationEmailTarget(row *ent.TicketNotification) (EmailTarget, error) {
	if row == nil || row.Channel != "email" || row.TargetProtocolVersion == nil || row.TargetTransport == nil || row.TargetDestinationDigest == nil {
		return EmailTarget{}, executionscope.ErrDenied
	}
	target := EmailTarget{ProtocolVersion: *row.TargetProtocolVersion, Transport: *row.TargetTransport, DestinationDigest: *row.TargetDestinationDigest}
	if row.TargetConnectorName != nil {
		target.ConnectorName = *row.TargetConnectorName
	}
	if row.TargetConnectorProvider != nil {
		target.ConnectorProvider = *row.TargetConnectorProvider
	}
	if target.Transport == "smtp" && (row.TargetConnectorName != nil || row.TargetConnectorProvider != nil) {
		return EmailTarget{}, executionscope.ErrDenied
	}
	if err := target.Validate(); err != nil {
		return EmailTarget{}, err
	}
	return target, nil
}
