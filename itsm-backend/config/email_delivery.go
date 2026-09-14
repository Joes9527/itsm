package config

import "fmt"

// EmailDeliveryConfig selects the route for new durable email intents. Provider
// availability must never change this choice. Empty preserves the Graph default.
type EmailDeliveryConfig struct {
	Transport string `mapstructure:"transport"`
}

func (c EmailDeliveryConfig) EffectiveTransport() string {
	if c.Transport == "" {
		return "graph"
	}
	return c.Transport
}

func (c EmailDeliveryConfig) Validate() error {
	switch c.EffectiveTransport() {
	case "graph", "smtp":
		return nil
	default:
		return fmt.Errorf("email delivery transport must be graph or smtp")
	}
}
