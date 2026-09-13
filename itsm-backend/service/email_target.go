package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"itsm-backend/common/executionscope"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/ent"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"unicode"
)

// EmailTarget is the versioned, secret-free identity stored with a durable
// email intent. SMTP has no connector identity; Graph names an exact provider.
type EmailTarget struct {
	ProtocolVersion   int    `json:"protocolVersion"`
	Transport         string `json:"transport"`
	ConnectorName     string `json:"connectorName,omitempty"`
	ConnectorProvider string `json:"connectorProvider,omitempty"`
	DestinationDigest string `json:"destinationDigest"`
}

// Validate rejects legacy, incomplete and cross-transport target identities.
func (target EmailTarget) Validate() error {
	if target.ProtocolVersion != 2 || len(target.DestinationDigest) != 64 || strings.ToLower(target.DestinationDigest) != target.DestinationDigest {
		return executionscope.ErrDenied
	}
	if _, err := hex.DecodeString(target.DestinationDigest); err != nil {
		return executionscope.ErrDenied
	}
	switch target.Transport {
	case "graph":
		if target.ConnectorName == "msgraph-email" && target.ConnectorProvider == "microsoft" {
			return nil
		}
	case "smtp":
		if target.ConnectorName == "" && target.ConnectorProvider == "" {
			return nil
		}
	}
	return executionscope.ErrDenied
}

// SetDeliveryTargetDependencies is trusted construction-time wiring, before
// serving concurrent requests. It does not activate any provider.
func (s *EmailService) SetDeliveryTargetDependencies(manager *connector.Manager, policy *database.ExecutionPolicy) {
	s.targetManager, s.targetPolicy = manager, policy
}

// DescribeDeliveryTarget is used only when producing a new durable intent.
// Standard Graph configuration reads use the caller's original transaction;
// candidate Graph identity comes exclusively from the frozen declaration.
func (s *EmailService) DescribeDeliveryTarget(ctx context.Context, tx *ent.Tx, tenantID int, capability string) (EmailTarget, error) {
	if s == nil || s.targetPolicy == nil || tx == nil {
		return EmailTarget{}, executionscope.ErrDenied
	}
	ref, err := s.targetPolicy.EventRef(tenantID)
	if err != nil {
		return EmailTarget{}, err
	}
	if err := s.targetPolicy.RequireDeliveryIdentity(ctx, ref, capability); err != nil {
		return EmailTarget{}, err
	}
	switch s.config.DeliveryTransport {
	case "smtp":
		return s.describeSMTPTarget()
	case "graph", "":
		// The default remains Graph, independent of live provider availability.
	default:
		return EmailTarget{}, executionscope.ErrDenied
	}
	if s.targetManager == nil {
		return EmailTarget{}, executionscope.ErrDenied
	}
	var digest string
	if s.targetPolicy.IsCandidate() {
		digest, err = s.targetManager.DescribeDeclaredDeliveryTarget(ctx, ref, capability, "msgraph-email", "microsoft")
	} else {
		digest, err = s.targetManager.DescribePersistedDeliveryTarget(ctx, ref, capability, tx.Client(), "msgraph-email", "microsoft")
	}
	if err != nil {
		return EmailTarget{}, err
	}
	return EmailTarget{ProtocolVersion: 2, Transport: "graph", ConnectorName: "msgraph-email", ConnectorProvider: "microsoft", DestinationDigest: digest}, nil
}

func (s *EmailService) describeSMTPTarget() (EmailTarget, error) {
	for _, value := range []string{s.config.Host, s.config.Username, s.config.From} {
		if value == "" || strings.TrimSpace(value) != value || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return EmailTarget{}, executionscope.ErrDenied
		}
	}
	if s.config.Port < 1 || s.config.Port > 65535 || strings.ContainsAny(s.config.Host, "/?#@") {
		return EmailTarget{}, executionscope.ErrDenied
	}
	// Match the current sender's host:port construction. Do not claim a target
	// for an address the existing SMTP implementation cannot parse.
	if _, _, err := net.SplitHostPort(s.config.Host + ":" + strconv.Itoa(s.config.Port)); err != nil {
		return EmailTarget{}, executionscope.ErrDenied
	}
	address, err := mail.ParseAddress(s.config.From)
	if err != nil || address.Address != s.config.From {
		return EmailTarget{}, executionscope.ErrDenied
	}
	identity := struct {
		Protocol, Host                          string
		Port                                    int
		Username, From, TransportMode, AuthMode string
	}{"smtp-mail-v1", s.config.Host, s.config.Port, s.config.Username, s.config.From, "tcp-starttls-if-advertised-tls12-verify-peer", "plain-auth"}
	raw, _ := json.Marshal(identity)
	digest := sha256.Sum256(raw)
	return EmailTarget{ProtocolVersion: 2, Transport: "smtp", DestinationDigest: hex.EncodeToString(digest[:])}, nil
}
