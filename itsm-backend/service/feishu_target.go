package service

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"

	"itsm-backend/common/executionscope"
	"itsm-backend/connector"
	"itsm-backend/database"
	"itsm-backend/ent"
)

// FeishuTarget is the secret-free identity fixed with an originating command.
// Runtime availability cannot select or replace a durable destination.
type FeishuTarget struct {
	ProtocolVersion   int    `json:"protocolVersion"`
	ConnectorName     string `json:"connectorName"`
	ConnectorProvider string `json:"connectorProvider"`
	DestinationDigest string `json:"destinationDigest"`
}

func (target FeishuTarget) Validate() error {
	if target.ProtocolVersion != 2 || target.ConnectorName != "feishu" || target.ConnectorProvider != "feishu" || len(target.DestinationDigest) != 64 || strings.ToLower(target.DestinationDigest) != target.DestinationDigest {
		return executionscope.ErrDenied
	}
	if _, err := hex.DecodeString(target.DestinationDigest); err != nil {
		return executionscope.ErrDenied
	}
	return nil
}

func describeFeishuTarget(ctx context.Context, tx *ent.Tx, tenantID int, manager *connector.Manager, execution *database.ExecutionPolicy) (*FeishuTarget, error) {
	if tx == nil || manager == nil || execution == nil {
		return nil, executionscope.ErrDenied
	}
	ref, err := execution.EventRef(tenantID)
	if err != nil {
		return nil, err
	}
	var digest string
	if execution.IsCandidate() {
		digest, err = manager.DescribeDeclaredDeliveryTarget(ctx, ref, "outbox", "feishu", "feishu")
	} else {
		digest, err = manager.DescribePersistedDeliveryTarget(ctx, ref, "outbox", tx.Client(), "feishu", "feishu")
	}
	if errors.Is(err, executionscope.ErrTargetNotConfigured) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	target := &FeishuTarget{ProtocolVersion: 2, ConnectorName: "feishu", ConnectorProvider: "feishu", DestinationDigest: digest}
	if err := target.Validate(); err != nil {
		return nil, err
	}
	return target, nil
}
