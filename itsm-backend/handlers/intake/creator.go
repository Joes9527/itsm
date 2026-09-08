package intake

import (
	"reflect"
	"sync"

	creation "itsm-backend/handlers/common/workitemcreation"
)

type CreatorRegistry struct {
	mu       sync.RWMutex
	creators map[string]creation.ProfessionalCreator
}

func NewCreatorRegistry() *CreatorRegistry {
	return &CreatorRegistry{creators: make(map[string]creation.ProfessionalCreator)}
}

func (r *CreatorRegistry) Register(creator creation.ProfessionalCreator) error {
	if creator == nil || isNilCreator(creator) {
		return creation.NewUnsupportedRecordClass("creator record class is required", nil)
	}
	class := creator.RecordClass()
	if !creation.IsSupportedRecordClass(class) {
		return creation.NewUnsupportedRecordClass("unsupported creator record class", nil)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.creators == nil {
		r.creators = make(map[string]creation.ProfessionalCreator)
	}
	if _, exists := r.creators[class]; exists {
		return creation.NewUnsupportedRecordClass("creator record class is already registered", nil)
	}
	r.creators[class] = creator
	return nil
}

func (r *CreatorRegistry) Get(recordClass string) (creation.ProfessionalCreator, error) {
	r.mu.RLock()
	creator := r.creators[recordClass]
	r.mu.RUnlock()
	if creator == nil {
		return nil, creation.NewUnsupportedRecordClass("no creator is registered for record class", nil)
	}
	return creator, nil
}

func isNilCreator(creator creation.ProfessionalCreator) bool {
	v := reflect.ValueOf(creator)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
