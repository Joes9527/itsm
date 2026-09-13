package eventbus

import (
	"fmt"
	"regexp"
	"strconv"

	"itsm-backend/common/executionscope"
	"itsm-backend/config"
)

var stableTopicPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

type streamRoute struct {
	topic    string
	tenantID int
}

// streamRoutes freezes transport names from the admitted startup configuration.
// It does not grant permission to publish, consume, or write a business record.
// Database membership and receipt checks remain the owning services' responsibility.
type streamRoutes struct {
	candidate bool
	refs      []executionscope.Ref
}

func newStreamRoutes(cfg config.ExecutionConfig) (*streamRoutes, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	routes := &streamRoutes{candidate: cfg.Mode == "candidate"}
	seen := make(map[string]bool, len(cfg.Scopes))
	for _, scope := range cfg.Scopes {
		if seen[scope.ScopeID] {
			return nil, fmt.Errorf("execution stream scope assigned to multiple tenants")
		}
		seen[scope.ScopeID] = true
		routes.refs = append(routes.refs, executionscope.Ref{DeploymentID: cfg.DeploymentID, ScopeID: scope.ScopeID, TenantID: scope.TenantID})
	}
	return routes, nil
}

func validateStableTopic(topic string) error {
	if len(topic) > 128 || !stableTopicPattern.MatchString(topic) {
		return fmt.Errorf("invalid stable event topic")
	}
	return nil
}

func candidateTopic(ref executionscope.Ref, topic string) string {
	return "candidate:" + ref.DeploymentID + ":" + ref.ScopeID + ":" + topic
}

func (r *streamRoutes) publishTopic(topic, tenant string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("event transport execution configuration required")
	}
	if !r.candidate {
		return topic, nil
	}
	if err := validateStableTopic(topic); err != nil {
		return "", err
	}
	id, err := strconv.Atoi(tenant)
	if err != nil || id <= 0 || strconv.Itoa(id) != tenant {
		return "", fmt.Errorf("canonical event tenant required")
	}
	for _, ref := range r.refs {
		if ref.TenantID == id {
			return candidateTopic(ref, topic), nil
		}
	}
	return "", fmt.Errorf("%w: event tenant outside frozen manifest", executionscope.ErrDenied)
}

func (r *streamRoutes) subscriptionRoutes(topic string) ([]streamRoute, error) {
	if r == nil {
		return nil, fmt.Errorf("event transport execution configuration required")
	}
	if !r.candidate {
		return []streamRoute{{topic: topic}}, nil
	}
	if err := validateStableTopic(topic); err != nil {
		return nil, err
	}
	routes := make([]streamRoute, 0, len(r.refs))
	for _, ref := range r.refs {
		routes = append(routes, streamRoute{topic: candidateTopic(ref, topic), tenantID: ref.TenantID})
	}
	return routes, nil
}
