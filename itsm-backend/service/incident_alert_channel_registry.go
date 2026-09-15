package service

import (
	"fmt"
	"sort"
)

var incidentAlertChannelRegistry = map[string]struct{}{"email": {}, "in_app": {}}

func RegisteredIncidentAlertChannels() []string {
	channels := make([]string, 0, len(incidentAlertChannelRegistry))
	for channel := range incidentAlertChannelRegistry {
		channels = append(channels, channel)
	}
	sort.Strings(channels)
	return channels
}

func validateIncidentAlertChannels(channels []string) error {
	seen := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		if _, ok := incidentAlertChannelRegistry[channel]; !ok {
			return fmt.Errorf("unsupported alert channel: %s", channel)
		}
		if _, duplicate := seen[channel]; duplicate {
			return fmt.Errorf("duplicate alert channel: %s", channel)
		}
		seen[channel] = struct{}{}
	}
	return nil
}

func channelsFromNotificationConfig(config map[string]interface{}) ([]string, error) {
	channels := make([]string, 0, len(config))
	for channel, raw := range config {
		if channel == "recipients" {
			continue
		}
		if _, ok := incidentAlertChannelRegistry[channel]; !ok {
			return nil, fmt.Errorf("unsupported alert channel: %s", channel)
		}
		enabled, isBool := raw.(bool)
		if !isBool {
			return nil, fmt.Errorf("alert channel %s must be boolean", channel)
		}
		if !enabled {
			continue
		}
		channels = append(channels, channel)
	}
	if err := validateIncidentAlertChannels(channels); err != nil {
		return nil, err
	}
	sort.Strings(channels)
	return channels, nil
}

func recipientsFromNotificationConfig(config map[string]interface{}) ([]string, error) {
	raw, ok := config["recipients"]
	if !ok {
		return nil, nil
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, value := range values {
			recipient, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("alert recipient must be a string")
			}
			result = append(result, recipient)
		}
		return result, nil
	default:
		return nil, fmt.Errorf("alert recipients must be a list")
	}
}

func validateIncidentNotificationConfig(config map[string]interface{}) error {
	channels, err := channelsFromNotificationConfig(config)
	if err != nil {
		return err
	}
	recipients, err := recipientsFromNotificationConfig(config)
	if err != nil {
		return err
	}
	for _, channel := range channels {
		if channel == "email" && len(recipients) == 0 {
			return fmt.Errorf("email alert recipient is required")
		}
	}
	return nil
}
