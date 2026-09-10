package service

import (
	"context"
	"encoding/json"
	"fmt"

	"itsm-backend/ent"
)

// Durable relation delivery event types. These are the only relation effects the
// shared outbox worker consumes.
const (
	RelationCreatedEventType = "work_item.relation_created"
	RelationRemovedEventType = "work_item.relation_removed"
)

// RequiresProblemVerification reports whether a Change professional outcome
// constitutes a completed repair that a resolved_by_change source Problem must
// verify. Only an explicit successful outcome qualifies: failed, rolled_back and
// terminal outcomes never request verification, and an empty or unknown outcome
// must not be interpreted as a repair.
func RequiresProblemVerification(outcome string) bool {
	return outcome == "successful"
}

// IsDirectedRelation reports whether a relation type carries direction and a
// counterpart action. Only directed relations emit a delivery event: symmetric or
// structural relations (related_to, parent_child, duplicate_of, ...) have no
// counterpart to act on, so emitting an event for them would create a dispatch
// surface with nothing legitimate to do. The type list is the relation registry,
// not a second classifier.
func IsDirectedRelation(relationType string) bool {
	switch relationType {
	case "investigated_by", "resolved_by_change":
		return true
	default:
		return false
	}
}

func relationEventID(relationID, version int, removed bool) string {
	outcome := "created"
	if removed {
		outcome = "removed"
	}
	return fmt.Sprintf("work-item-relation:%d:%d:%s", relationID, version, outcome)
}

// emitRelationEventTx persists the relation delivery event inside the caller's
// transaction, so the relation row, the source version bump, the immutable receipt
// and the delivery event commit or roll back together. Facts are handed over
// unchanged from the reviewed RelationFacts contract.
func emitRelationEventTx(ctx context.Context, tx *ent.Tx, facts RelationFacts) error {
	if !IsDirectedRelation(facts.Type) {
		return nil
	}
	eventType := RelationCreatedEventType
	if facts.Removed {
		eventType = RelationRemovedEventType
	}
	payload, err := json.Marshal(facts)
	if err != nil {
		return fmt.Errorf("marshal relation delivery facts: %w", err)
	}
	if _, err := tx.OutboxEvent.Create().
		SetEventID(relationEventID(facts.RelationID, facts.Version, facts.Removed)).
		SetEventType(eventType).
		SetTenantID(facts.TenantID).
		SetAggregateType("work_item").
		SetAggregateID(fmt.Sprintf("%d", facts.MutationWorkItemID)).
		SetPayload(payload).
		Save(ctx); err != nil {
		return fmt.Errorf("persist relation delivery event: %w", err)
	}
	return nil
}
