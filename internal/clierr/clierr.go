// Package clierr defines the stable error taxonomy for twt2. Every
// user-facing failure carries a Code so agents can branch on the JSON
// error output and on the process exit code.
package clierr

import (
	"errors"
	"fmt"
)

type Code string

const (
	NotFound           Code = "not_found"
	AlreadyExists      Code = "already_exists"
	PreconditionFailed Code = "precondition_failed"
	Locked             Code = "locked"
	UnsafeState        Code = "unsafe_state"
	InvalidUsage       Code = "invalid_usage"
	Internal           Code = "internal"
)

// Codes lists every code in sorted order for schema output.
func Codes() []string {
	return []string{
		string(AlreadyExists),
		string(Internal),
		string(InvalidUsage),
		string(Locked),
		string(NotFound),
		string(PreconditionFailed),
		string(UnsafeState),
	}
}

type Error struct {
	Code    Code
	Message string
	Hint    string
	Wrapped error
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Wrapped }

func New(code Code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Wrap classifies an existing error and keeps it available to errors.Is.
func Wrap(code Code, err error) *Error {
	return &Error{Code: code, Message: err.Error(), Wrapped: err}
}

// WithHint adds a full-sentence recovery hint to err and returns err.
func WithHint(err *Error, format string, a ...any) *Error {
	err.Hint = fmt.Sprintf(format, a...)
	return err
}

// CodeOf returns the code of the first *Error in the chain, or Internal.
func CodeOf(err error) Code {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Code
	}
	return Internal
}

// HintOf returns the hint of the first *Error in the chain, or "".
func HintOf(err error) string {
	var classified *Error
	if errors.As(err, &classified) {
		return classified.Hint
	}
	return ""
}

// ExitCode maps an error to the twt2 process exit code:
// 0 success, 1 internal, 2 invalid usage, 3 failed precondition.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	switch CodeOf(err) {
	case InvalidUsage:
		return 2
	case NotFound, AlreadyExists, PreconditionFailed, Locked, UnsafeState:
		return 3
	default:
		return 1
	}
}
