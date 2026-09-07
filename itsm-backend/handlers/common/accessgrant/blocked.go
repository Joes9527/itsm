package accessgrant

// BlockedError is a current domain non-executability decision, never an
// authentication, tenant authorization, or storage/transport failure.
type BlockedError struct{ Code string }

func (e *BlockedError) Error() string { return "access delegation blocked: " + e.Code }
