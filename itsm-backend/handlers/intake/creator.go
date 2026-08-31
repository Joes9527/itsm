package intake

import (
	"context"
	"sync"

	"itsm-backend/ent"
)

type ProfessionalCreator interface {
	RecordClass() string
	Prepare(ctx context.Context, tx *ent.Tx, in ResolvedIntake) (*CreationPlan, error)
	CreateExtension(ctx context.Context, tx *ent.Tx, workItem *ent.Ticket, plan *CreationPlan) (*ProfessionalReference, error)
}

type CreatorRegistry struct {
	mu       sync.RWMutex
	creators map[string]ProfessionalCreator
}

func NewCreatorRegistry() *CreatorRegistry {
	return &CreatorRegistry{creators: make(map[string]ProfessionalCreator)}
}

func (r *CreatorRegistry) Register(creator ProfessionalCreator) error {
	if creator == nil || creator.RecordClass() == "" {
		return NewUnsupportedRecordClass("creator record class is required", nil)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.creators[creator.RecordClass()]; exists {
		return NewUnsupportedRecordClass("creator record class is already registered", nil)
	}
	r.creators[creator.RecordClass()] = creator
	return nil
}

func (r *CreatorRegistry) Get(recordClass string) (ProfessionalCreator, error) {
	r.mu.RLock()
	creator := r.creators[recordClass]
	r.mu.RUnlock()
	if creator == nil {
		return nil, NewUnsupportedRecordClass("no creator is registered for record class", nil)
	}
	return creator, nil
}
