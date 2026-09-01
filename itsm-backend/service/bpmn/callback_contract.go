package bpmn

// CallbackActionContract declares the durable payload boundary for one
// synchronous callback action.
type CallbackActionContract struct {
	PayloadFields     []string
	RequiredFields    []string
	ConfigRefRequired bool
}

// CallbackContractProvider exposes the declared actions supported by a
// synchronous callback handler.
type CallbackContractProvider interface {
	CallbackContract(action string) (CallbackActionContract, bool)
}
