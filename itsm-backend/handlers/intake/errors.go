package intake

import (
	"encoding/json"
	"errors"
)

type ErrorCode string

const (
	InvalidCommand            ErrorCode = "InvalidCommand"
	AuthenticationRequired    ErrorCode = "AuthenticationRequired"
	PermissionDenied          ErrorCode = "PermissionDenied"
	ReferenceNotFound         ErrorCode = "ReferenceNotFound"
	IdempotencyConflict       ErrorCode = "IdempotencyConflict"
	DomainValidationFailed    ErrorCode = "DomainValidationFailed"
	InfrastructureUnavailable ErrorCode = "InfrastructureUnavailable"
	InternalFailure           ErrorCode = "InternalFailure"
	UnsupportedRecordClass    ErrorCode = "UnsupportedRecordClass"
	WorkflowBindingRequired   ErrorCode = "WorkflowBindingRequired"
)

var (
	ErrInvalidCommand            = errors.New("invalid intake command")
	ErrAuthenticationRequired    = errors.New("intake authentication required")
	ErrPermissionDenied          = errors.New("intake permission denied")
	ErrReferenceNotFound         = errors.New("intake reference not found")
	ErrIdempotencyConflict       = errors.New("intake idempotency conflict")
	ErrDomainValidationFailed    = errors.New("intake domain validation failed")
	ErrInfrastructureUnavailable = errors.New("intake infrastructure unavailable")
	ErrInternalFailure           = errors.New("intake internal failure")
	ErrUnsupportedRecordClass    = errors.New("intake record class unsupported")
	ErrWorkflowBindingRequired   = errors.New("intake workflow binding required")
)

type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type IntakeError struct {
	Code        ErrorCode    `json:"code"`
	Message     string       `json:"message"`
	Retryable   bool         `json:"retryable"`
	FieldErrors []FieldError `json:"fieldErrors,omitempty"`
	HTTPStatus  int          `json:"-"`
	cause       error
}

func (e *IntakeError) Error() string {
	return e.Message
}

func (e *IntakeError) Unwrap() error {
	return e.cause
}

func (e *IntakeError) Is(target error) bool {
	return target == sentinelForCode(e.Code)
}

func (e *IntakeError) PublicJSON() string {
	payload, _ := json.Marshal(e)
	return string(payload)
}

func NewIntakeError(code ErrorCode, message string, cause error, fields ...FieldError) *IntakeError {
	status, retryable, known := errorPolicy(code)
	if !known {
		code = InternalFailure
		status, retryable, _ = errorPolicy(code)
		message = "intake request failed"
	}
	return &IntakeError{
		Code:        code,
		Message:     message,
		Retryable:   retryable,
		FieldErrors: append([]FieldError(nil), fields...),
		HTTPStatus:  status,
		cause:       cause,
	}
}

func NewInvalidCommand(message string, field FieldError, cause error) *IntakeError {
	fields := []FieldError(nil)
	if field.Field != "" || field.Message != "" {
		fields = append(fields, field)
	}
	return NewIntakeError(InvalidCommand, message, cause, fields...)
}

func NewAuthenticationRequired(message string, cause error) *IntakeError {
	return NewIntakeError(AuthenticationRequired, message, cause)
}

func NewPermissionDenied(message string, cause error) *IntakeError {
	return NewIntakeError(PermissionDenied, message, cause)
}

func NewReferenceNotFound(message string, cause error) *IntakeError {
	return NewIntakeError(ReferenceNotFound, message, cause)
}

func NewIdempotencyConflict(message string, cause error) *IntakeError {
	return NewIntakeError(IdempotencyConflict, message, cause)
}

func NewDomainValidationFailed(message string, cause error, fields ...FieldError) *IntakeError {
	return NewIntakeError(DomainValidationFailed, message, cause, fields...)
}

func NewInfrastructureUnavailable(message string, cause error) *IntakeError {
	return NewIntakeError(InfrastructureUnavailable, message, cause)
}

func NewInternalFailure(message string, cause error) *IntakeError {
	return NewIntakeError(InternalFailure, message, cause)
}

func NewUnsupportedRecordClass(message string, cause error) *IntakeError {
	return NewIntakeError(UnsupportedRecordClass, message, cause)
}

func NewWorkflowBindingRequired(message string, cause error) *IntakeError {
	return NewIntakeError(WorkflowBindingRequired, message, cause)
}

func sentinelForCode(code ErrorCode) error {
	switch code {
	case InvalidCommand:
		return ErrInvalidCommand
	case AuthenticationRequired:
		return ErrAuthenticationRequired
	case PermissionDenied:
		return ErrPermissionDenied
	case ReferenceNotFound:
		return ErrReferenceNotFound
	case IdempotencyConflict:
		return ErrIdempotencyConflict
	case DomainValidationFailed:
		return ErrDomainValidationFailed
	case InfrastructureUnavailable:
		return ErrInfrastructureUnavailable
	case InternalFailure:
		return ErrInternalFailure
	case UnsupportedRecordClass:
		return ErrUnsupportedRecordClass
	case WorkflowBindingRequired:
		return ErrWorkflowBindingRequired
	default:
		return nil
	}
}

func errorPolicy(code ErrorCode) (status int, retryable bool, known bool) {
	switch code {
	case InvalidCommand:
		return 400, false, true
	case AuthenticationRequired:
		return 401, false, true
	case PermissionDenied:
		return 403, false, true
	case ReferenceNotFound:
		return 404, false, true
	case IdempotencyConflict:
		return 409, false, true
	case DomainValidationFailed, UnsupportedRecordClass, WorkflowBindingRequired:
		return 422, false, true
	case InfrastructureUnavailable:
		return 503, true, true
	case InternalFailure:
		return 500, false, true
	default:
		return 0, false, false
	}
}
