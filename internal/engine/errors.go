package engine

import "fmt"

const (
	ErrCodeProbeFailed     = "PROBE_FAILED"
	ErrCodeRangeRejected   = "RANGE_REJECTED"
	ErrCodeAuthRequired    = "AUTH_REQUIRED"
	ErrCodeForbidden       = "FORBIDDEN"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeDiskFull        = "DISK_FULL"
	ErrCodeChecksum        = "CHECKSUM_MISMATCH"
	ErrCodeCanceled        = "CANCELED"
	ErrCodeNetwork         = "NETWORK"
	ErrCodeValidatorChange = "VALIDATOR_CHANGED"
	ErrCodeInternal        = "INTERNAL"
)

func NewTaskError(code, message, hint string) *TaskError {
	return &TaskError{Code: code, Message: message, Hint: hint}
}

func (e *TaskError) Error() string {
	if e == nil {
		return ""
	}
	if e.Hint != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Hint)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}
