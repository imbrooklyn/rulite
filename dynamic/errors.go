package dynamic

import (
	"errors"
	"strconv"
)

var (
	// ErrInvalidDefinition identifies invalid transport or definition fields.
	ErrInvalidDefinition = errors.New("rulite/dynamic: invalid definition")
	// ErrLimit identifies a configuration byte, depth, count, or schema limit.
	ErrLimit = errors.New("rulite/dynamic: configuration limit exceeded")
	// ErrInvalidRegistry identifies a nil or uninitialized registry.
	ErrInvalidRegistry = errors.New("rulite/dynamic: invalid registry")
	// ErrRegistryFrozen identifies registration attempted after Freeze.
	ErrRegistryFrozen = errors.New("rulite/dynamic: registry is frozen")
	// ErrRegistryNotFrozen identifies compilation before Freeze.
	ErrRegistryNotFrozen = errors.New("rulite/dynamic: registry is not frozen")
	// ErrInvalidAction identifies an invalid name, callback, validator, or schema.
	ErrInvalidAction = errors.New("rulite/dynamic: invalid action registration")
	// ErrDuplicateAction identifies an already registered capability name.
	ErrDuplicateAction = errors.New("rulite/dynamic: duplicate action")
	// ErrUnknownAction identifies a capability absent from the frozen registry.
	ErrUnknownAction = errors.New("rulite/dynamic: unknown action")
	// ErrInvalidParams identifies invalid action parameters or a validator failure.
	ErrInvalidParams = errors.New("rulite/dynamic: invalid action parameters")
)

// CompileError describes a construction failure, never an execution failure.
// Default text contains only the stage and registration index, without source,
// parameters, capability names, or underlying diagnostic text. Unwrap explicitly
// to inspect details; those errors may contain configuration or caller values.
type CompileError struct {
	stage string
	index int
	cause error
}

// Error describes the failed construction stage without formatting its cause.
func (e *CompileError) Error() string {
	if e == nil {
		return "rulite/dynamic: compilation failed"
	}
	s := "rulite/dynamic: " + e.stage + " failed"
	if e.index >= 0 {
		s += " at definition " + strconv.Itoa(e.index)
	}
	return s
}

// Stage returns decode, validate, condition, or action; nil returns empty.
func (e *CompileError) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}

// Index returns the original zero-based definition index, or -1 for a whole
// document failure or nil error. Root ValidationError retains individual issues.
func (e *CompileError) Index() int {
	if e == nil {
		return -1
	}
	return e.index
}

// Unwrap preserves the original cause for errors.Is and errors.As.
func (e *CompileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func compileError(stage string, index int, cause error) error {
	return &CompileError{stage: stage, index: index, cause: cause}
}
