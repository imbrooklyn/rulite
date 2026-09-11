package cel

import "errors"

var (
	// ErrInvalidCompiler identifies a nil or uninitialized compiler.
	ErrInvalidCompiler = errors.New("rulite/cel: invalid compiler")
	// ErrNonBool identifies an expression whose checked result is not exactly bool.
	ErrNonBool = errors.New("rulite/cel: expression must return bool")
	// ErrUnknown identifies an unknown evaluation outcome, never an ordinary miss.
	ErrUnknown = errors.New("rulite/cel: unknown outcome")
	// ErrCostLimit identifies evaluation stopped by its CEL cost budget.
	ErrCostLimit = errors.New("rulite/cel: evaluation cost limit exceeded")
	// ErrInputLimit identifies input exceeding the documented size limits.
	ErrInputLimit = errors.New("rulite/cel: input size limit exceeded")
	// ErrNativeLimit identifies native construction or equality exceeding its
	// documented traversal, collection, byte, or storage budget.
	ErrNativeLimit = errors.New("rulite/cel: native operation limit exceeded")
	// ErrNilBinding identifies a nil projected root value.
	ErrNilBinding = errors.New("rulite/cel: nil binding value")
	// ErrProtoDescriptor identifies a message with an unexpected descriptor.
	ErrProtoDescriptor = errors.New("rulite/cel: protobuf descriptor mismatch")
	// ErrFunctionPanic identifies a panic inside a trusted CEL function.
	ErrFunctionPanic = errors.New("rulite/cel: trusted function panicked")
)

// CompileError identifies an environment, parse, check, output, or program build
// failure. No condition is returned on failure. Its default text omits source
// and the underlying diagnostic; explicitly unwrap it to inspect those details.
type CompileError struct {
	expressionID string
	stage        string
	cause        error
}

// Error describes the compile stage and expression identity without source text.
func (e *CompileError) Error() string {
	if e == nil {
		return "rulite/cel: compilation failed"
	}
	return "rulite/cel: " + e.stage + " failed" + expressionLabel(e.expressionID)
}

// ExpressionID returns the source's SHA-256 hex digest. It is empty for
// environment failures and sources rejected before parsing by the byte limit.
func (e *CompileError) ExpressionID() string {
	if e == nil {
		return ""
	}
	return e.expressionID
}

// Stage returns environment, parse, check, output, or program, or empty for nil.
func (e *CompileError) Stage() string {
	if e == nil {
		return ""
	}
	return e.stage
}

// Unwrap preserves the original diagnostic for errors.Is and errors.As.
// Upstream compile diagnostics may contain source excerpts and positions.
func (e *CompileError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// RuntimeError is an ordinary condition error, wrapped by the root engine's
// canonical Failure and ExecutionError. It retains no activation or input.
// Explicitly unwrapped upstream errors may contain business values; callers
// must treat error objects as read-only and decide whether to disclose them.
type RuntimeError struct {
	expressionID string
	cause        error
}

// Error describes evaluation failure without formatting the source, input,
// underlying error, or any value recovered by the CEL implementation.
func (e *RuntimeError) Error() string {
	if e == nil {
		return "rulite/cel: evaluation failed"
	}
	return "rulite/cel: evaluation failed" + expressionLabel(e.expressionID)
}

// ExpressionID returns the compiled source's SHA-256 hex digest, or empty for nil.
func (e *RuntimeError) ExpressionID() string {
	if e == nil {
		return ""
	}
	return e.expressionID
}

// Unwrap preserves upstream errors, budget sentinels, and context causes for
// errors.Is and errors.As. The cause is not automatically formatted by Error.
func (e *RuntimeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func expressionLabel(id string) string {
	if id == "" {
		return ""
	}
	return " (expression " + id + ")"
}
