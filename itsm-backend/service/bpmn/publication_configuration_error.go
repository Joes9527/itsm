package bpmn

// PublicationConfigurationError identifies a deterministic, item-local configuration
// failure. It deliberately has no cause: infrastructure failures must never be
// converted into this marker by publication owners.
type PublicationConfigurationError struct{ Message string }

func (e *PublicationConfigurationError) Error() string { return e.Message }
