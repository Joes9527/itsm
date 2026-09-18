package connector

import (
	"fmt"
	"strings"
)

// DeliveryDestinationDescriber is a pure, concurrency-safe configuration parser.
// It must not retain or mutate Config, initialize a client, access the network,
// or read secret values. Registration captures it before any instance Init.
type DeliveryDestinationDescriber interface {
	DescribeDeliveryDestination(Config) (string, error)
}

// DescribeDeliveryDestination describes routing, not authority. The caller must
// obtain Config from its trusted configuration owner and separately authorize
// the durable business intent and eventual execution. Disabled configurations
// may be described; this never activates an instance or enables a capability.
func (r *Registry) DescribeDeliveryDestination(cfg Config) (string, error) {
	if r == nil {
		return "", fmt.Errorf("connector destination description unavailable")
	}
	r.mu.RLock()
	manifest, registered := r.manifests[cfg.Name]
	description := r.descriptions[cfg.Name]
	r.mu.RUnlock()
	if !registered || description == nil || manifest.Provider != cfg.Provider {
		return "", fmt.Errorf("connector destination description unavailable")
	}
	digest, err := description.DescribeDeliveryDestination(cfg)
	if err != nil || len(digest) != 64 || strings.IndexFunc(digest, func(c rune) bool {
		return !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f')
	}) != -1 {
		return "", fmt.Errorf("connector destination description invalid")
	}
	return digest, nil
}
