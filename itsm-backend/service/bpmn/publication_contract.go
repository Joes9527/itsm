package bpmn

import (
	"context"
	"encoding/json"
	"itsm-backend/ent"
)

// PublicationConfigurationProvider lets the registered capability owner expose
// only its public policy and validate it without executing external side effects.
// Preview must support incomplete drafts; Validate is called only for publication.
// C1 grant handlers register this interface on the same dispatch registry.
type PublicationConfigurationProvider interface {
	PublicationConfiguration(context.Context, *ent.Client, int, string, string) (json.RawMessage, error)
	ValidatePublicationConfiguration(context.Context, *ent.Client, int, string, string) error
}
