package rulite

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

var (
	// ErrNilContext means Fire received a nil context.
	ErrNilContext = errors.New("rulite: nil context")
	// ErrNilInput means Fire received a nil input pointer.
	ErrNilInput = errors.New("rulite: nil input")
	// ErrInvalidEngine means Fire received a nil or uninitialized engine.
	ErrInvalidEngine = errors.New("rulite: invalid engine")
	// ErrInvalidPolicy means a policy contains an unknown mode.
	ErrInvalidPolicy = errors.New("rulite: invalid execution policy")
	// ErrInvalidPanicMode means a panic option contains an unknown mode.
	ErrInvalidPanicMode = errors.New("rulite: invalid panic mode")
)

// Phase identifies the callback responsible for a failure.
type Phase uint8

const (
	// ConditionPhase identifies condition evaluation.
	ConditionPhase Phase = iota
	// ActionPhase identifies an action attempt.
	ActionPhase
)

func phaseText(phase Phase) string {
	if phase == ActionPhase {
		return "action"
	}
	return "condition"
}

// Failure is the canonical rule failure in both Result and ExecutionError.
// Its fields are immutable; user error objects are retained by reference and
// must be treated as read-only by callers.
type Failure struct {
	ruleID    RuleID
	phase     Phase
	cause     error
	continued bool
}

// Error describes the rule, phase, and cause. The text is not a stable format.
func (f Failure) Error() string {
	message := fmt.Sprintf("rule %q %s failed", f.ruleID, phaseText(f.phase))
	if f.cause != nil {
		message += ": " + f.cause.Error()
	}
	return message
}

// Unwrap returns the original cause for errors.Is and errors.As.
func (f Failure) Unwrap() error { return f.cause }

// RuleID returns the failed rule's identity.
func (f Failure) RuleID() RuleID { return f.ruleID }

// Phase returns the failed callback phase.
func (f Failure) Phase() Phase { return f.phase }

// Cause returns the same error as Unwrap.
func (f Failure) Cause() error { return f.cause }

// Continued reports a ContinueOnError disposition, even if context, first-match,
// or the end of the snapshot prevented further calls. It is false for panics.
func (f Failure) Continued() bool { return f.continued }

// PanicError describes a recovered callback panic with its original value and
// the stack captured immediately at recovery. Other goroutines, runtime fatal
// errors, process exit, and runtime.Goexit are outside callback recovery.
type PanicError struct {
	ruleID RuleID
	phase  Phase
	value  any
	stack  []byte
}

// Error includes the rule, phase, and panic value's Go type, never its contents
// or stack. It does not call methods on the panic value.
func (e *PanicError) Error() string {
	if e == nil {
		return "rulite: callback panic"
	}
	return fmt.Sprintf("rulite: rule %q %s panic (%T)", e.ruleID, phaseText(e.phase), e.value)
}

// RuleID returns the panicking rule's identity.
func (e *PanicError) RuleID() RuleID {
	if e == nil {
		return ""
	}
	return e.ruleID
}

// Phase returns the panicking callback phase.
func (e *PanicError) Phase() Phase {
	if e == nil {
		return ConditionPhase
	}
	return e.phase
}

// Value returns the original panic value without copying it. Arbitrary values
// cannot be deeply copied; callers must treat this value as read-only.
func (e *PanicError) Value() any {
	if e == nil {
		return nil
	}
	return e.value
}

// Stack returns a defensive copy of the stack captured at recovery.
func (e *PanicError) Stack() []byte {
	if e == nil {
		return nil
	}
	return slices.Clone(e.stack)
}

// ExecutionError wraps every error observed during an execution, even when
// there is just one error or continued failures end in StopCompleted.
// Rule failures appear in observation order; a context boundary adds ctx.Err()
// followed by context.Cause(ctx) when the latter is distinct.
type ExecutionError struct {
	causes   []error
	failures []Failure
}

// Error describes the observed errors. The exact text is not a stable format.
func (e *ExecutionError) Error() string {
	var message strings.Builder
	message.WriteString("rulite: execution failed")
	if e != nil {
		for _, cause := range e.causes {
			message.WriteString("; ")
			message.WriteString(cause.Error())
		}
	}
	return message.String()
}

// Unwrap returns a defensive copy of all observed errors, preserving errors.Is
// and errors.As for business causes, context errors, and PanicError.
func (e *ExecutionError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return slices.Clone(e.causes)
}

// Failures returns a defensive copy of rule failures in observation order.
func (e *ExecutionError) Failures() []Failure {
	if e == nil {
		return nil
	}
	return slices.Clone(e.failures)
}
