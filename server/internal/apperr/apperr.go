package apperr

import "fmt"

type Code string

const (
	InvalidRequest         Code = "INVALID_REQUEST"
	UnsupportedFormat      Code = "UNSUPPORTED_FORMAT"
	Unauthenticated        Code = "UNAUTHENTICATED"
	TokenExpired           Code = "TOKEN_EXPIRED"
	DeviceRevoked          Code = "DEVICE_REVOKED"
	RegistrationDisabled   Code = "REGISTRATION_DISABLED"
	NotFound               Code = "NOT_FOUND"
	VaultNotInitialized    Code = "VAULT_NOT_INITIALIZED"
	IdempotencyConflict    Code = "IDEMPOTENCY_CONFLICT"
	SyncResetRequired      Code = "SYNC_RESET_REQUIRED"
	QuotaExceeded          Code = "QUOTA_EXCEEDED"
	ClipPurged             Code = "CLIP_PURGED"
	TrashExpired           Code = "TRASH_EXPIRED"
	CursorExpired          Code = "CURSOR_EXPIRED"
	SnapshotExpired        Code = "SNAPSHOT_EXPIRED"
	VersionConflict        Code = "VERSION_CONFLICT"
	VaultExists            Code = "VAULT_EXISTS"
	PayloadTooLarge        Code = "PAYLOAD_TOO_LARGE"
	PreconditionRequired   Code = "PRECONDITION_REQUIRED"
	RateLimited            Code = "RATE_LIMITED"
	NotReady               Code = "NOT_READY"
	TemporarilyUnavailable Code = "TEMPORARILY_UNAVAILABLE"
)

type Error struct {
	Status  int
	Code    Code
	Message string
	Details map[string]any
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func New(status int, code Code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func WithDetails(status int, code Code, message string, details map[string]any) *Error {
	return &Error{Status: status, Code: code, Message: message, Details: details}
}

func Is(err error, code Code) bool {
	var ae *Error
	if err == nil {
		return false
	}
	if e, ok := err.(*Error); ok {
		return e.Code == code
	}
	_ = ae
	return false
}
