package eventbus

import (
	"fmt"
	"regexp"

	"itsm-backend/handlers/shared"
)

// DurableEventHandler names a logical owner, not a process instance. Replicas
// share its group; different side effects must declare different owner names.
type DurableEventHandler interface {
	shared.EventHandler
	EventConsumerID() string
}

var consumerIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func (eb *WatermillEventBus) consumerIdentity(handler shared.EventHandler) (string, error) {
	if eb.routes == nil {
		return "", fmt.Errorf("event transport execution configuration required")
	}
	_, typed := handler.(ExecutionEnvelopeHandler)
	if typed && eb.authority == nil {
		return "", fmt.Errorf("persistent event authority required before subscription")
	}
	if !eb.routes.candidate && !typed {
		return "", nil
	}
	owner, ok := handler.(DurableEventHandler)
	if !ok {
		return "", fmt.Errorf("persistent event handler must declare a durable consumer identity")
	}
	name := owner.EventConsumerID()
	if !consumerIDPattern.MatchString(name) {
		return "", fmt.Errorf("invalid durable event consumer identity")
	}
	return name, nil
}

// subscriberForLocked allocates client resources only; Subscribe owns network
// operations. The caller holds mu so Close sees every owned subscriber.
func (eb *WatermillEventBus) subscriberForLocked(consumer string) (streamSubscriber, error) {
	if consumer == "" {
		return eb.subscriber, nil
	}
	if eb.newSubscriber == nil {
		return nil, fmt.Errorf("candidate subscriber factory required")
	}
	if existing := eb.ownedSubscribers[consumer]; existing != nil {
		return existing, nil
	}
	subscriber, err := eb.newSubscriber("itsm:" + consumer)
	if err != nil {
		return nil, err
	}
	if subscriber == nil {
		return nil, fmt.Errorf("candidate subscriber factory returned nil")
	}
	if eb.ownedSubscribers == nil {
		eb.ownedSubscribers = map[string]streamSubscriber{}
	}
	eb.ownedSubscribers[consumer] = subscriber
	return subscriber, nil
}
