package rulite

import (
	"slices"
	"time"
)

// Trace is an immutable, complete per-execution diagnostic view. Rule outcomes
// come from Result's ledger; only timings and condition trees are additional.
type Trace struct{ result Result }

type traceData struct {
	duration time.Duration
	rules    []ruleTiming
}

type ruleTiming struct {
	condition time.Duration
	action    time.Duration
	tree      ConditionTrace
	hasTree   bool
}

// Rules returns a defensive copy of views for every snapshot rule, including
// those never evaluated. Rule order is the compiled order.
func (t Trace) Rules() []RuleTrace {
	if t.result.trace == nil {
		return nil
	}
	rules := make([]RuleTrace, t.result.counts.Total)
	for order := range rules {
		rules[order] = RuleTrace{execution: t.result.ruleAt(order), timing: t.result.trace.rules[order]}
	}
	return rules
}

// Duration returns elapsed execution time measured with Go's monotonic clock.
// It includes synchronous observer delivery, including the finish event.
func (t Trace) Duration() time.Duration {
	if t.result.trace == nil {
		return 0
	}
	return t.result.trace.duration
}

// StopReason returns the execution's global stop reason.
func (t Trace) StopReason() StopReason { return t.result.stop }

// RuleTrace is an immutable rule execution view with optional timing and tree data.
type RuleTrace struct {
	execution RuleExecution
	timing    ruleTiming
}

// ID returns the rule identity.
func (t RuleTrace) ID() RuleID { return t.execution.ID() }

// Order returns the zero-based flattened executable position, excluding containers.
func (t RuleTrace) Order() int { return t.execution.Order() }

// RegistrationIndex returns the original top-level construction argument position.
// A member uses its group's position; MemberIndex supplies local registration.
func (t RuleTrace) RegistrationIndex() int { return t.execution.RegistrationIndex() }

// TopLevelOrder returns the compiled top-level position of this rule or its group.
func (t RuleTrace) TopLevelOrder() int { return t.execution.TopLevelOrder() }

// MemberIndex returns the local registration position, or zero and false for a top-level rule.
func (t RuleTrace) MemberIndex() (int, bool) { return t.execution.MemberIndex() }

// GroupID returns the containing group identity, or an empty ID and false.
func (t RuleTrace) GroupID() (GroupID, bool) { return t.execution.GroupID() }

// Priority returns the compiled priority.
func (t RuleTrace) Priority() Priority { return t.execution.Priority() }

// Name returns the normalized display name from the captured metadata.
func (t RuleTrace) Name() string { return t.execution.Name() }

// Description returns the normalized description from the captured metadata.
func (t RuleTrace) Description() string { return t.execution.Description() }

// Tags returns a defensive copy of the captured normalized tags.
func (t RuleTrace) Tags() []string { return t.execution.Tags() }

// State returns the final state from the canonical ledger.
func (t RuleTrace) State() RuleState { return t.execution.State() }

// Evaluated reports whether the condition was called.
func (t RuleTrace) Evaluated() bool { return t.execution.Evaluated() }

// Matched reports whether the condition returned true, nil.
func (t RuleTrace) Matched() bool { return t.execution.Matched() }

// ActionStarted reports whether the action was called.
func (t RuleTrace) ActionStarted() bool { return t.execution.ActionStarted() }

// Fired reports whether the action returned nil.
func (t RuleTrace) Fired() bool { return t.execution.Fired() }

// Error returns the canonical Failure for a failed state, otherwise nil.
func (t RuleTrace) Error() error { return t.execution.Error() }

// SkipReason returns the reason a matched rule's action did not start.
func (t RuleTrace) SkipReason() SkipReason { return t.execution.SkipReason() }

// NotEvaluatedReason returns the reason the condition was never called.
func (t RuleTrace) NotEvaluatedReason() NotEvaluatedReason { return t.execution.NotEvaluatedReason() }

// ConditionDuration returns elapsed condition time, or zero if not evaluated.
// It excludes observer delivery for this execution's events.
func (t RuleTrace) ConditionDuration() time.Duration { return t.timing.condition }

// ActionDuration returns elapsed action time, or zero if the action did not start.
// It excludes observer delivery for this execution's events.
func (t RuleTrace) ActionDuration() time.Duration { return t.timing.action }

// ConditionTree returns the observed built-in combinator tree, if available.
// Functions with no observed combinators and unevaluated rules have no tree.
// Arbitrary Go control flow inside a callback is not a condition tree.
// A callback that invokes multiple operators or changes an operator's outcome
// remains an opaque leaf, without a tree claiming that operator's semantics.
func (t RuleTrace) ConditionTree() (ConditionTrace, bool) { return t.timing.tree, t.timing.hasTree }

// ConditionKind identifies an observed combinator or an opaque function leaf.
// A child that was never called is opaque and uses ConditionLeaf, even if its
// callback would have invoked a combinator. No function introspection is used.
type ConditionKind uint8

const (
	// ConditionLeaf identifies an opaque condition callback.
	ConditionLeaf ConditionKind = iota
	// ConditionAll identifies All.
	ConditionAll
	// ConditionAny identifies Any.
	ConditionAny
	// ConditionNot identifies Not.
	ConditionNot
)

// ConditionOutcome is an observed condition result. An error takes precedence
// over the callback's boolean result, including inside combinators.
type ConditionOutcome uint8

const (
	// ConditionOutcomeNotEvaluated means this child was never called.
	ConditionOutcomeNotEvaluated ConditionOutcome = iota
	// ConditionOutcomeFalse means the callback returned false, nil.
	ConditionOutcomeFalse
	// ConditionOutcomeTrue means the callback returned true, nil.
	ConditionOutcomeTrue
	// ConditionOutcomeError means the callback failed or panicked.
	ConditionOutcomeError
)

// ConditionNotEvaluatedReason explains an uncalled condition child.
type ConditionNotEvaluatedReason uint8

const (
	// ConditionNotEvaluatedNone means no non-evaluation reason applies.
	ConditionNotEvaluatedNone ConditionNotEvaluatedReason = iota
	// ConditionNotEvaluatedShortCircuit means an earlier child ended evaluation.
	ConditionNotEvaluatedShortCircuit
)

// ConditionTrace is an immutable node in an observed combinator tree.
// Errors are caller-owned values and must be treated as read-only.
type ConditionTrace struct {
	kind     ConditionKind
	index    int
	outcome  ConditionOutcome
	err      error
	reason   ConditionNotEvaluatedReason
	children []ConditionTrace
}

// Kind returns the observed operator or opaque leaf kind.
func (t ConditionTrace) Kind() ConditionKind { return t.kind }

// Index returns -1 for a root, or the zero-based child position within its parent.
func (t ConditionTrace) Index() int { return t.index }

// Outcome returns the observed result, with errors taking precedence over bool.
func (t ConditionTrace) Outcome() ConditionOutcome { return t.outcome }

// Error returns the child cause, or the PanicError for a recovered panic.
func (t ConditionTrace) Error() error { return t.err }

// NotEvaluatedReason returns why the child was not called.
func (t ConditionTrace) NotEvaluatedReason() ConditionNotEvaluatedReason { return t.reason }

// Children returns a defensive copy of immutable child views in argument order.
func (t ConditionTrace) Children() []ConditionTrace { return slices.Clone(t.children) }
