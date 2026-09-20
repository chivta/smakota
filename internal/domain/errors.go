package domain

import "fmt"

// AppError carries an HTTP status and a machine-readable code. It never carries
// a human message: those live in the frontend translation map, keyed by Code.
type AppError struct {
	Status int
	Code   string
}

func (e *AppError) Error() string {
	return fmt.Sprintf("%s (%d)", e.Code, e.Status)
}

var (
	ErrNotFound     = &AppError{Status: 404, Code: "NOT_FOUND"}
	ErrInvalidInput = &AppError{Status: 400, Code: "INVALID_INPUT"}
	ErrUnauthorized = &AppError{Status: 401, Code: "UNAUTHORIZED"}
	ErrForbidden    = &AppError{Status: 403, Code: "FORBIDDEN"}
	ErrConflict     = &AppError{Status: 409, Code: "CONFLICT"}
	ErrRateLimited  = &AppError{Status: 429, Code: "RATE_LIMITED"}
	ErrInternal     = &AppError{Status: 500, Code: "INTERNAL"}

	// ErrNotExportable guards Tier 2 rows. Returned when a caller asks for a
	// bulk view of rows whose licence forbids reproduction.
	ErrNotExportable = &AppError{Status: 403, Code: "NOT_EXPORTABLE"}

	// ErrDayLocked is returned for edits to days older than the editable window.
	ErrDayLocked = &AppError{Status: 409, Code: "DAY_LOCKED"}

	// ErrUnresolved means the resolver produced no candidate above the floor.
	ErrUnresolved = &AppError{Status: 422, Code: "UNRESOLVED"}
)
