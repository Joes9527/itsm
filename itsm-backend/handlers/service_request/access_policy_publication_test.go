package service_request_test

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"itsm-backend/dto"
	"itsm-backend/ent/processdefinition"
	"itsm-backend/handlers/common/accessgrant"
	"itsm-backend/handlers/service_catalog"
	"itsm-backend/service"
	"strings"
	"testing"
)

func TestAccessPolicyPublicationRequiresExactDeclaredCapability(t *testing.T) {
	for _, kind := range []string{"valid", "wrong_policy", "missing_policy", "unknown_action", "unbounded", "foreign_policy", "other_catalog", "unverifiable_worker"} {
		t.Run(kind, func(t *testing.T) {
			fx := newSSLVPNDelegationFixture(t)
			deploySSLVPNDefinition(t, fx, "access", fmt.Sprintf(sslvpnApprovalNodes, fx.approver.ID, fx.approver.ID), sslvpnApprovalFlows)
			owner := service_catalog.NewService(service_catalog.NewEntRepository(fx.client), fx.client, zap.NewNop().Sugar(), nil)
			configureCatalogPublicationForTest(fx.ctx, fx.client, fx.tenant.ID, owner)
			policy := &accessgrant.Policy{Provider: accessgrant.Graph, ExternalSystem: "directory", GroupID: "group", DurationField: "duration", DurationOptions: []accessgrant.DurationOption{{Key: "month", Label: "一个月", Seconds: 2592000}}}
			input := dto.CreateServiceCatalogRequest{Name: "Access", Category: "IT", TargetClass: "service_request_item", ProcessDefinitionKey: "access", RequiresApproval: true, AccessPolicy: policy, Fields: []map[string]any{{"name": "duration", "label": "申请有效期", "type": "select", "required": true, "options": []any{map[string]any{"label": "一个月", "value": "month"}}}}}
			if kind == "missing_policy" {
				input.AccessPolicy = nil
			}
			draft, err := owner.Create(fx.ctx, fx.tenant.ID, input)
			require.NoError(t, err)
			ref := "999999"
			if draft.AccessPolicy != nil {
				ref = fmt.Sprint(draft.AccessPolicy.ID)
			}
			if kind == "wrong_policy" {
				ref = "999999"
			}
			if kind == "foreign_policy" || kind == "other_catalog" {
				tenantID := fx.tenant.ID
				if kind == "foreign_policy" {
					tenantID = fx.client.Tenant.Create().SetName("Foreign").SetCode("foreign").SaveX(fx.ctx).ID
				}
				other := fx.client.ServiceCatalog.Create().SetTenantID(tenantID).SetName("Other catalog").SaveX(fx.ctx)
				otherPolicy := fx.client.CatalogAccessPolicy.Create().SetCatalogID(other.ID).SetProvider("graph").SetExternalSystem("directory").SetGroupID("other-group").SetDurationField("duration").SetDurationOptions(policy.DurationOptions).SaveX(fx.ctx)
				ref = fmt.Sprint(otherPolicy.ID)
			}
			if kind == "unverifiable_worker" {
				owner.SetPublicationEngine(service.NewCustomProcessEngine(fx.client, zap.NewNop().Sugar()).(*service.CustomProcessEngine))
			}
			action := accessgrant.Capability
			if kind == "unknown_action" {
				action = "unknown_grant"
			}
			def := fx.client.ProcessDefinition.Query().Where(processdefinition.TenantIDEQ(fx.tenant.ID), processdefinition.KeyEQ("access")).OnlyX(fx.ctx)
			xml := strings.Replace(string(def.BpmnXML), `<bpmn:metaData name="allowed_actions">`, fmt.Sprintf(`<bpmn:metaData name="action">%s</bpmn:metaData><bpmn:metaData name="callback_config_ref">%s</bpmn:metaData><bpmn:metaData name="allowed_actions">`, action, ref), 1)
			fx.client.ProcessDefinition.UpdateOne(def).SetBpmnXML([]byte(xml)).SaveX(fx.ctx)
			// Direct fixture mutation has changed the public process revision; reload before publish.
			draft, err = owner.Get(fx.ctx, fx.tenant.ID, draft.ID)
			require.NoError(t, err)
			enabled := "enabled"
			patch := dto.UpdateServiceCatalogRequest{ExpectedCatalogVersion: draft.CatalogVersion, Status: &enabled}
			if kind == "unbounded" {
				policy.DurationOptions[0].Seconds = 0
				patch.AccessPolicy = policy
			}
			_, err = owner.Update(fx.ctx, fx.tenant.ID, draft.ID, patch)
			if kind == "valid" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
