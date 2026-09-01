package bpmn

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSynchronousCallbackContractsDeclareExactlyRegisteredActions(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler CallbackContractProvider
		actions []string
	}{
		{"ticket", &TicketServiceTaskHandler{}, []string{"update_status", "notify_requester", "notify_handler", "escalate", "assign"}},
		{"change", &ChangeServiceTaskHandler{}, []string{"create_change", "update_change", "approve_change", "reject_change", "schedule_change", "implement_change", "verify_change", "close_change", "assess_risk", "notify_stakeholders"}},
		{"incident", &IncidentServiceTaskHandler{}, []string{"create_incident", "assign_incident", "escalate_incident", "resolve_incident", "close_incident", "update_incident", "acknowledge_incident", "categorize_incident"}},
		{"service_request", &ServiceRequestServiceTaskHandler{}, []string{"create_request", "update_request", "approve_request", "reject_request", "assign_request", "provision_resource", "complete_request", "cancel_request"}},
		{"generic", &GenericServiceTaskHandler{}, []string{"complete_service", "notify_rejection", "notify"}},
		{"notification", &NotificationHandler{}, []string{"send_in_app", "send_email", "send_sms", "send_webhook"}},
		{"cc", &CCTaskHandler{}, []string{""}},
		{"webhook", &WebhookHandler{}, []string{"call_webhook", "send_notification"}},
		{"release", &ReleaseServiceTaskHandler{}, []string{"tech_review", "approval", "schedule", "execute", "verify"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, action := range test.actions {
				contract, ok := test.handler.CallbackContract(action)
				require.True(t, ok, action)
				for _, required := range contract.RequiredFields {
					require.Contains(t, contract.PayloadFields, required, action)
				}
			}
			_, ok := test.handler.CallbackContract("unknown")
			require.False(t, ok)
		})
	}
}

func TestKAFDelegationIsNotASynchronousCallbackContractProvider(t *testing.T) {
	_, ok := any(&KafDelegateServiceTaskHandler{}).(CallbackContractProvider)
	require.False(t, ok)
}
