package service

import (
	"context"
	"errors"

	"itsm-backend/common/executionscope"
)

type graphTargetSender interface {
	GraphMailSender
	Mailbox() string
}

// SendToTarget executes an already recorded identity. It never chooses current
// defaults, reads configuration rows, or falls back to another transport.
// The durable owner remains responsible for source, claim and receipt checks.
func (s *EmailService) SendToTarget(ctx context.Context, tenantID int, capability string, target EmailTarget, msg *EmailMessage) (resultErr error) {
	started := false
	defer func() {
		if resultErr != nil && !started {
			resultErr = newEmailTransportError(target.Transport, "preflight", emailNotAccepted, resultErr)
		}
	}()
	if s == nil || s.targetPolicy == nil || target.Validate() != nil {
		return executionscope.ErrDenied
	}
	ref, err := s.targetPolicy.EventRef(tenantID)
	if err != nil {
		return err
	}
	if err := s.targetPolicy.RequireConnectorDelivery(ctx, ref, capability); err != nil {
		return err
	}
	if target.Transport == "smtp" {
		// Capture the actual route used for this attempt. No policy/default route
		// choice is taken from this config after the intent has been recorded.
		sender := &EmailService{config: s.config, logger: s.logger, smtpSend: s.smtpSend}
		actual, err := sender.describeSMTPTarget()
		if err != nil || actual != target || sender.smtpSend == nil {
			return executionscope.ErrDenied
		}
		if err := s.prepareMessage(msg); err != nil {
			return newEmailTransportError("smtp", "validation", emailNotAccepted, err)
		}
		copy := *msg
		copy.DisableProviderFallback = true
		started = true
		sendErr := sender.sendViaSMTP(ctx, &copy)
		current, err := s.describeSMTPTarget()
		if err != nil || current != target {
			return newEmailTransportError("smtp", "target_changed", emailAcceptanceUnknown, errors.Join(err, executionscope.ErrDenied))
		}
		return sendErr
	}
	if s.targetManager == nil {
		return executionscope.ErrDenied
	}
	manager := s.targetManager
	bound, generation, digest, err := manager.ResolveDeliveryTarget(ctx, ref, capability, target.ConnectorName, target.ConnectorProvider)
	if err != nil {
		return err
	}
	sender, ok := bound.(graphTargetSender)
	if !ok || digest != target.DestinationDigest {
		return executionscope.ErrDenied
	}
	if msg != nil && (len(msg.CC) > 0 || len(msg.Attachments) > 0 || (msg.BodyText == "" && msg.Body != "")) {
		return newEmailTransportError("graph", "unsupported_message", emailNotAccepted, errors.New("bound Graph delivery does not support these message fields"))
	}
	if err := s.prepareMessage(msg); err != nil {
		return newEmailTransportError("graph", "validation", emailNotAccepted, err)
	}
	started = true
	sendErr := s.sendViaGraph(ctx, sender, sender.Mailbox(), msg)
	_, currentGeneration, currentDigest, err := manager.ResolveDeliveryTarget(ctx, ref, capability, target.ConnectorName, target.ConnectorProvider)
	if err != nil || currentGeneration != generation || currentDigest != target.DestinationDigest {
		return newEmailTransportError("graph", "target_changed", emailAcceptanceUnknown, errors.Join(err, executionscope.ErrDenied))
	}
	return sendErr
}
