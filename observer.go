package rulite

import (
	"context"
	"fmt"
	"slices"
)

// Observer receives synchronous execution facts. Observe cannot change policy,
// retry a callback, or replace a business outcome through its return value.
// An error disables observation for the rest of this Fire and becomes a
// Diagnostic. The next Fire starts with the configured observer enabled again.
// Panics are isolated the same way unless PropagatePanics is selected.
//
// Implementations must synchronize state shared by concurrent Fire calls.
// Latency and backpressure belong to the implementation; Fire waits for Observe.
// The context is the caller's context. Cancellation and captured-state side
// effects remain real effects, observed at ordinary callback boundaries.
type Observer interface {
	// Observe consumes one immutable event. Its error is diagnostic only.
	Observe(context.Context, Event) error
}

// ObserverFunc adapts a function to Observer. A nil function is a no-op.
type ObserverFunc func(context.Context, Event) error

// Observe calls f, or returns nil when f is nil.
func (f ObserverFunc) Observe(ctx context.Context, event Event) error {
	if f == nil {
		return nil
	}
	return f(ctx, event)
}

// WithObserver replaces the observer for Fire or an immutable engine default.
// Nil disables observation. Options apply from left to right; a per-call value
// replaces the default without changing the engine. Implementations are retained
// by reference and must synchronize shared mutable state. Except for nil
// ObserverFunc, typed-nil implementations are invoked normally; panics follow
// the selected PanicMode. This option does not enable Trace or duration clocks.
func WithObserver(observer Observer) FireOption {
	return FireOption{kind: optionObserver, observer: observer}
}

// EventKind identifies an execution fact delivered to an Observer.
type EventKind uint8

const (
	// EventNone is the kind of a zero Event; it is never delivered.
	EventNone EventKind = iota
	// EventExecutionStarted follows preflight and precedes the initial context check.
	EventExecutionStarted
	// EventRuleEvaluated follows condition return or recovery, before match/failure events.
	EventRuleEvaluated
	// EventRuleMatched follows evaluation returning true, nil, before the action boundary.
	EventRuleMatched
	// EventRuleFired follows a successful action return and ledger update.
	EventRuleFired
	// EventRuleFailed follows a recorded condition or action failure, including recovery.
	EventRuleFailed
	// EventExecutionFinished follows the final business stop and counts, before Fire returns.
	EventExecutionFinished
)

// String returns the event kind's textual name.
func (k EventKind) String() string {
	switch k {
	case EventNone:
		return "none"
	case EventExecutionStarted:
		return "execution-started"
	case EventRuleEvaluated:
		return "rule-evaluated"
	case EventRuleMatched:
		return "rule-matched"
	case EventRuleFired:
		return "rule-fired"
	case EventRuleFailed:
		return "rule-failed"
	case EventExecutionFinished:
		return "execution-finished"
	default:
		return "unknown"
	}
}

// Event is a small immutable tagged value referencing only metadata and facts.
// It can be retained after Observe returns. It never retains input, context,
// callbacks, observers, or options. Business errors available through Failure
// remain caller-owned read-only values, including any PanicError payload.
// Events do not contain timings or directly expose panic values.
//
// A condition error or recovered panic emits evaluated(error), then failed,
// sharing the canonical Failure. A true, nil condition emits evaluated(true),
// then matched. Its action emits fired on success or failed on error/recovery.
// A skipped action emits neither; an uncalled rule emits no events.
// EventRuleMatched proves eligibility, not action start or success.
//
// Delivery stops at the first observer error or recovered observer panic.
// No events are sent on preflight failure. Empty and already-canceled executions
// still have start/finish events when observation stays enabled. A propagated
// panic makes no promise of a finish event or returned Result.
type Event struct {
	rule    *ruleMetadata
	order   int
	failure *Failure
	summary *eventSummary
	kind    EventKind
	outcome ConditionOutcome
}

type eventSummary struct {
	counts Counts
	stop   StopReason
}

// Kind returns the fact's tag, or EventNone for a zero Event.
func (e Event) Kind() EventKind { return e.kind }

// Rule returns metadata for a rule event, or a zero view and false otherwise.
// Order is the flattened executable position; RegistrationIndex is the original
// top-level argument position. GroupID, MemberIndex, and TopLevelOrder provide
// group coordinates. RuleID identity and descriptive metadata are unchanged.
func (e Event) Rule() (RuleInfo, bool) {
	if e.rule == nil {
		return RuleInfo{}, false
	}
	return RuleInfo{metadata: *e.rule, order: e.order}, true
}

// Phase returns ConditionPhase for evaluated/matched events, ActionPhase for
// fired events, and the canonical failure phase for failed events. Execution
// events and a zero Event return ConditionPhase and false.
func (e Event) Phase() (Phase, bool) {
	switch e.kind {
	case EventRuleEvaluated, EventRuleMatched:
		return ConditionPhase, true
	case EventRuleFired:
		return ActionPhase, true
	case EventRuleFailed:
		return e.failure.Phase(), true
	default:
		return ConditionPhase, false
	}
}

// ConditionOutcome returns the observed condition result for evaluated/matched
// events. Errors and recovered panics use ConditionOutcomeError, ignoring any
// boolean returned with an error. Other events return ConditionOutcomeNotEvaluated
// and false. Matched events always have ConditionOutcomeTrue.
func (e Event) ConditionOutcome() (ConditionOutcome, bool) {
	if e.kind == EventRuleEvaluated || e.kind == EventRuleMatched {
		return e.outcome, true
	}
	return ConditionOutcomeNotEvaluated, false
}

// Failure returns the canonical business failure for a failed event or an
// evaluated event with a condition error/recovered panic. Other events return
// a zero failure and false. This is the same Failure recorded in Result and
// ExecutionError, never an observer diagnostic.
func (e Event) Failure() (Failure, bool) {
	if e.failure == nil {
		return Failure{}, false
	}
	return *e.failure, true
}

// Counts returns a copied summary for start/finish events, or zero and false
// otherwise. Start has Total=NotEvaluated and all other counts zero. Finish
// has the final business counts, unaffected by observation diagnostics.
func (e Event) Counts() (Counts, bool) {
	if e.summary == nil {
		return Counts{}, false
	}
	return e.summary.counts, true
}

// StopReason returns the final business reason for a finish event, otherwise
// StopNone. Cancellation during finish delivery cannot retroactively change
// terminal business facts; there is no later context boundary in this Fire.
func (e Event) StopReason() StopReason {
	if e.summary == nil {
		return StopNone
	}
	return e.summary.stop
}

// Diagnostic describes an observer failure without changing business outcomes.
// It retains the delivered event and original error, never the Observer or
// execution configuration. User error/panic values remain caller-owned read-only.
type Diagnostic struct {
	event Event
	cause error
}

// Event returns the immutable event whose delivery failed.
func (d Diagnostic) Event() Event { return d.event }

// Cause returns the observer error, or *ObserverPanicError after recovery.
func (d Diagnostic) Cause() error { return d.cause }

// Unwrap returns Cause for errors.Is and errors.As on the diagnostic alone.
func (d Diagnostic) Unwrap() error { return d.cause }

// Error describes the observation failure. Text is not a stable format.
// User error messages are included as supplied; panic payloads are not formatted.
func (d Diagnostic) Error() string {
	message := "rulite: observer failed at " + d.event.Kind().String()
	if d.cause != nil {
		message += ": " + d.cause.Error()
	}
	return message
}

// ObserverPanicError describes an isolated observation panic. It is a diagnostic
// cause, not a business PanicError or Failure, and does not cause StopPanic.
// Runtime fatal errors, other goroutines' panics, process exit, and runtime.Goexit
// are outside observer recovery.
type ObserverPanicError struct {
	value any
	stack []byte
}

// Error includes only the panic payload's Go type, never its contents or stack.
// It does not call methods on the payload.
func (e *ObserverPanicError) Error() string {
	if e == nil {
		return "rulite: observer panic"
	}
	return fmt.Sprintf("rulite: observer panic (%T)", e.value)
}

// Value returns the original panic payload. Callers must treat it as read-only.
func (e *ObserverPanicError) Value() any {
	if e == nil {
		return nil
	}
	return e.value
}

// Stack returns a defensive copy of the stack captured immediately at recovery.
func (e *ObserverPanicError) Stack() []byte {
	if e == nil {
		return nil
	}
	return slices.Clone(e.stack)
}
