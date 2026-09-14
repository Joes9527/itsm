package feishu

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"itsm-backend/connector"
)

// This is routing identity only. Parsing is shared by description and Init,
// so producing an intent does not acquire a token or require an app secret.
type feishuDestination struct {
	Protocol           string `json:"protocol"`
	AppID              string `json:"appId"`
	BaseURL            string `json:"baseUrl"`
	CallbackInstanceID string `json:"callbackInstanceId"`
}

func parseFeishuDestination(cfg connector.Config) (feishuDestination, error) {
	target := feishuDestination{Protocol: "feishu-task-v2", AppID: cfg.Credentials["app_id"]}
	if target.AppID == "" || strings.TrimSpace(target.AppID) != target.AppID {
		return feishuDestination{}, fmt.Errorf("feishu: app identity is required")
	}
	for _, field := range []struct {
		key  string
		dest *string
	}{
		{"base_url", &target.BaseURL}, {"callbackInstanceId", &target.CallbackInstanceID},
	} {
		if raw, exists := cfg.Settings[field.key]; exists {
			value, ok := raw.(string)
			if !ok || strings.TrimSpace(value) != value {
				return feishuDestination{}, fmt.Errorf("feishu: invalid target field %s", field.key)
			}
			*field.dest = value
		}
	}
	if target.BaseURL == "" {
		target.BaseURL = BaseURLCN
		if raw, exists := cfg.Settings["region"]; exists {
			region, ok := raw.(string)
			if !ok {
				return feishuDestination{}, fmt.Errorf("feishu: invalid target region")
			}
			if region == "intl" {
				target.BaseURL = BaseURLIntl
			}
		}
	}
	endpoint, err := url.Parse(target.BaseURL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" {
		return feishuDestination{}, fmt.Errorf("feishu: invalid target endpoint")
	}
	return target, nil
}

func (target feishuDestination) digest() string {
	raw, _ := json.Marshal(target)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func (*Feishu) DescribeDeliveryDestination(cfg connector.Config) (string, error) {
	target, err := parseFeishuDestination(cfg)
	if err != nil {
		return "", err
	}
	return target.digest(), nil
}

func (f *Feishu) DeliveryDestinationIdentity() string { return f.destination }
