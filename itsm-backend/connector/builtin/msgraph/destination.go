package msgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"itsm-backend/connector"
)

// graphDestination is the effective routing identity, never credentials. Keep
// parsing shared with Init so describing a disabled route needs no activation.
type graphDestination struct {
	Protocol      string `json:"protocol"`
	AzureTenantID string `json:"azureTenantId"`
	ClientID      string `json:"clientId"`
	Mailbox       string `json:"mailbox"`
	AADBaseURL    string `json:"aadBaseUrl"`
	GraphBaseURL  string `json:"graphBaseUrl"`
}

func parseGraphDestination(cfg connector.Config) (graphDestination, error) {
	d := graphDestination{Protocol: "msgraph-mail-v1", ClientID: cfg.Credentials["azure_client_id"]}
	values := []struct {
		key      string
		value    *string
		fallback string
	}{
		{"azure_tenant_id", &d.AzureTenantID, ""},
		{"mailbox", &d.Mailbox, ""},
		{"aad_base_url", &d.AADBaseURL, DefaultAADBaseURL},
		{"graph_base_url", &d.GraphBaseURL, DefaultGraphBaseURL},
	}
	for _, entry := range values {
		if raw, ok := cfg.Settings[entry.key]; ok {
			value, ok := raw.(string)
			if !ok {
				return graphDestination{}, fmt.Errorf("msgraph: invalid target field %s", entry.key)
			}
			*entry.value = value
		}
		if *entry.value == "" {
			*entry.value = entry.fallback
		}
		if *entry.value == "" || strings.TrimSpace(*entry.value) != *entry.value {
			return graphDestination{}, fmt.Errorf("msgraph: invalid target field %s", entry.key)
		}
	}
	if d.ClientID == "" || strings.TrimSpace(d.ClientID) != d.ClientID {
		return graphDestination{}, fmt.Errorf("msgraph: client identity is required")
	}
	for _, endpoint := range []string{d.AADBaseURL, d.GraphBaseURL} {
		u, err := url.Parse(endpoint)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			return graphDestination{}, fmt.Errorf("msgraph: invalid target endpoint")
		}
	}
	return d, nil
}

func (d graphDestination) digest() string {
	raw, _ := json.Marshal(d) // only fixed string fields; marshaling cannot fail
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// DescribeDeliveryDestination reads configured identity without Init, token
// acquisition, network access, or reading the client secret.
func (*GraphConnector) DescribeDeliveryDestination(cfg connector.Config) (string, error) {
	destination, err := parseGraphDestination(cfg)
	if err != nil {
		return "", err
	}
	return destination.digest(), nil
}

// DeliveryDestinationIdentity is captured from the same effective identity
// used to construct the client. Later caller-owned map edits cannot alter it.
func (g *GraphConnector) DeliveryDestinationIdentity() string { return g.destination }

var _ connector.DeliveryDestination = (*GraphConnector)(nil)
