package rulite

import "slices"

// Counts partitions the rules in a completed execution ledger. Phase failure
// counts include recovered panics; PanicRecovered overlaps those counts.
// Matched means a condition returned true, nil. Fired means an action returned
// nil, which does not prove that it changed any state.
type Counts struct {
	// Total is the number of rules in the captured engine snapshot.
	Total int
	// Evaluated is the number of conditions called.
	Evaluated int
	// NotEvaluated is the number of conditions never called.
	NotEvaluated int
	// Matched is the number of conditions that returned true, nil.
	Matched int
	// Unmatched is the number of conditions that returned false, nil.
	Unmatched int
	// Fired is the number of actions that returned nil.
	Fired int
	// Skipped is the number of matched actions prevented from starting.
	Skipped int
	// Failed is ConditionFailed plus ActionFailed.
	Failed int
	// ConditionFailed counts condition errors and recovered condition panics.
	ConditionFailed int
	// ActionFailed counts action errors and recovered action panics.
	ActionFailed int
	// PanicRecovered counts recovered business panics already included in Failed.
	PanicRecovered int
}

// RuleState is a rule's final execution state.
type RuleState uint8

const (
	// RuleNotEvaluated means the condition was never called.
	RuleNotEvaluated RuleState = iota
	// RuleUnmatched means the condition returned false, nil.
	RuleUnmatched
	// RuleFired means the action returned nil.
	RuleFired
	// RuleFailed means the condition or action returned an error or panicked.
	RuleFailed
	// RuleSkipped means a matched rule's action did not start.
	RuleSkipped
)

// SkipReason explains why a matched rule's action did not start.
type SkipReason uint8

const (
	// NotSkipped means the rule was not skipped.
	NotSkipped SkipReason = iota
	// SkipContextDone means cancellation was observed before starting the action.
	SkipContextDone
)

// NotEvaluatedReason explains why a condition was never called.
type NotEvaluatedReason uint8

const (
	// NotEvaluatedNone means no non-evaluation reason applies.
	NotEvaluatedNone NotEvaluatedReason = iota
	// NotEvaluatedExecutionStopped means execution ended before this condition.
	NotEvaluatedExecutionStopped
	// NotEvaluatedGroupResolved means local selection bypassed this member.
	// A global stop at the selection boundary instead uses execution-stopped.
	NotEvaluatedGroupResolved
)

// StopReason is the global reason execution ended. Panic has precedence over
// context, then error policy, then first-match or first-fire, then completion.
type StopReason uint8

const (
	// StopNone identifies a result whose execution never started.
	StopNone StopReason = iota
	// StopCompleted means execution reached the end under its policy.
	StopCompleted
	// StopFirstMatch means the first matched rule finished its action attempt.
	StopFirstMatch
	// StopFirstFire means the first successful action ended execution.
	StopFirstFire
	// StopConditionError means a condition error used StopOnError.
	StopConditionError
	// StopActionError means an action error used StopOnError.
	StopActionError
	// StopContextCanceled means a boundary observed context cancellation.
	StopContextCanceled
	// StopContextDeadlineExceeded means a boundary observed an expired deadline.
	StopContextDeadlineExceeded
	// StopPanic means a callback panic was recovered.
	StopPanic
)

// String returns the stop reason's textual name.
func (s StopReason) String() string {
	switch s {
	case StopNone:
		return "none"
	case StopCompleted:
		return "completed"
	case StopFirstMatch:
		return "first-match"
	case StopFirstFire:
		return "first-fire"
	case StopConditionError:
		return "condition-error"
	case StopActionError:
		return "action-error"
	case StopContextCanceled:
		return "context-canceled"
	case StopContextDeadlineExceeded:
		return "context-deadline-exceeded"
	case StopPanic:
		return "panic"
	default:
		return "unknown"
	}
}

// Result is an immutable execution ledger, safe for concurrent reading after
// Fire returns. Its zero value is a safe, not-started result with StopNone.
// It retains shared rule metadata, sparse outcomes, and diagnostics, never
// callbacks, observers, input, context, or options. User errors and panic values
// remain caller-owned read-only values. All slice accessors return copies.
type Result struct {
	metadata         *snapshotMetadata
	counts           Counts
	stop             StopReason
	records          []executionRecord
	failures         []Failure
	trace            *traceData
	diagnostics      []Diagnostic
	evaluatedThrough int
	groups           []groupRecord
}

// Diagnostics returns a defensive copy of observation failures. A single
// configured observer can contribute at most one diagnostic per execution,
// because the first error or recovered panic disables it for that Fire.
// Diagnostics never enter business counts, failures, or ExecutionError.
// The zero Result returns an empty collection, which may be nil.
func (r Result) Diagnostics() []Diagnostic { return slices.Clone(r.diagnostics) }

// Only non-default evaluated outcomes occupy records. The frontier and group
// suffix facts distinguish ordinary misses, local holes, and global termination.
type executionRecord struct {
	order        int
	state        RuleState
	failureIndex int
}

// Counts returns a copy of the execution counts.
func (r Result) Counts() Counts { return r.counts }

// Executed reports whether preflight succeeded and execution started.
func (r Result) Executed() bool { return r.metadata != nil }

// Evaluated returns the number of conditions called.
func (r Result) Evaluated() int { return r.counts.Evaluated }

// Matched returns a defensive copy of matched IDs in observation order.
func (r Result) Matched() []RuleID {
	var ids []RuleID
	for _, record := range r.records {
		if record.state != RuleFailed || r.failures[record.failureIndex].phase == ActionPhase {
			ids = append(ids, r.metadata.rules[record.order].id)
		}
	}
	return ids
}

// Fired returns a defensive copy of successfully fired IDs in observation order.
func (r Result) Fired() []RuleID {
	var ids []RuleID
	for _, record := range r.records {
		if record.state == RuleFired {
			ids = append(ids, r.metadata.rules[record.order].id)
		}
	}
	return ids
}

// Failures returns a defensive copy of failures in observation order.
func (r Result) Failures() []Failure { return slices.Clone(r.failures) }

// Stopped reports a terminal stop other than normal completion.
// Both a zero Result and a completed execution return false.
func (r Result) Stopped() bool { return r.stop != StopNone && r.stop != StopCompleted }

// StopReason returns the global stop reason, or StopNone before execution.
func (r Result) StopReason() StopReason { return r.stop }

// Rule looks up an immutable rule execution view. Unknown IDs return false.
func (r Result) Rule(id RuleID) (RuleExecution, bool) {
	if r.metadata == nil {
		return RuleExecution{}, false
	}
	order, ok := r.metadata.byID[id]
	if !ok {
		return RuleExecution{}, false
	}
	return r.ruleAt(order), true
}

func (r Result) ruleAt(order int) RuleExecution {
	x := RuleExecution{metadata: r.metadata.rules[order], order: order}
	if group := x.metadata.group; group != nil && group.index < len(r.groups) {
		record := r.groups[group.index]
		if record.state == GroupResolved && order >= record.skippedFrom {
			x.notEvaluated = NotEvaluatedGroupResolved
			return x
		}
	}
	if order >= r.evaluatedThrough {
		x.notEvaluated = NotEvaluatedExecutionStopped
		return x
	}
	x.state = RuleUnmatched
	index, found := slices.BinarySearchFunc(r.records, order, func(record executionRecord, order int) int {
		if record.order < order {
			return -1
		}
		if record.order > order {
			return 1
		}
		return 0
	})
	if found {
		record := r.records[index]
		x.state = record.state
		if record.state == RuleFailed {
			x.failure = r.failures[record.failureIndex]
		}
		if record.state == RuleSkipped {
			x.skip = SkipContextDone
		}
	}
	return x
}

// Explain returns a view of the same ledger, without evaluating any callbacks.
func (r Result) Explain() Explanation { return Explanation{result: r} }

// Trace returns the optional full trace and whether tracing was enabled.
// Preflight failures never have a trace, even when WithTrace was supplied.
func (r Result) Trace() (Trace, bool) {
	if r.trace == nil {
		return Trace{}, false
	}
	return Trace{result: r}, true
}

// RuleExecution is an immutable view of one rule's metadata and final outcome.
type RuleExecution struct {
	metadata     ruleMetadata
	order        int
	state        RuleState
	failure      Failure
	skip         SkipReason
	notEvaluated NotEvaluatedReason
}

// ID returns the rule identity.
func (x RuleExecution) ID() RuleID { return x.metadata.id }

// Order returns the zero-based flattened executable position, excluding containers.
func (x RuleExecution) Order() int { return x.order }

// RegistrationIndex returns the original top-level construction argument position.
// Members use their group's CompileEntries position; MemberIndex identifies
// their independent local registration position.
func (x RuleExecution) RegistrationIndex() int { return x.metadata.registrationIndex }

// TopLevelOrder returns the compiled top-level position of this rule or its group.
func (x RuleExecution) TopLevelOrder() int { return x.metadata.topLevelOrder }

// MemberIndex returns the local registration position, or zero and false for a top-level rule.
func (x RuleExecution) MemberIndex() (int, bool) {
	return x.metadata.memberIndex, x.metadata.group != nil
}

// GroupID returns the containing group identity, or an empty ID and false.
func (x RuleExecution) GroupID() (GroupID, bool) {
	if x.metadata.group == nil {
		return "", false
	}
	return x.metadata.group.id, true
}

// Priority returns the compiled priority.
func (x RuleExecution) Priority() Priority { return x.metadata.priority }

// Name returns the normalized display name from the captured metadata.
func (x RuleExecution) Name() string { return x.metadata.details.value().name }

// Description returns the normalized description from the captured metadata.
func (x RuleExecution) Description() string { return x.metadata.details.value().description }

// Tags returns a defensive copy of the captured normalized tags.
func (x RuleExecution) Tags() []string { return slices.Clone(x.metadata.details.value().tags) }

// State returns the final execution state.
func (x RuleExecution) State() RuleState { return x.state }

// Evaluated reports whether the condition was called.
func (x RuleExecution) Evaluated() bool { return x.state != RuleNotEvaluated }

// Matched reports whether the condition returned true, nil.
func (x RuleExecution) Matched() bool {
	return x.state == RuleFired || x.state == RuleSkipped || x.state == RuleFailed && x.failure.phase == ActionPhase
}

// ActionStarted reports whether the action was called.
func (x RuleExecution) ActionStarted() bool { return x.Matched() && x.state != RuleSkipped }

// Fired reports whether the action returned nil, regardless of side effects.
func (x RuleExecution) Fired() bool { return x.state == RuleFired }

// SkipReason returns the reason a matched rule's action did not start.
func (x RuleExecution) SkipReason() SkipReason { return x.skip }

// NotEvaluatedReason returns the reason the condition was never called.
func (x RuleExecution) NotEvaluatedReason() NotEvaluatedReason { return x.notEvaluated }

// Error returns the canonical Failure value for a failed rule, otherwise nil.
func (x RuleExecution) Error() error {
	if x.state == RuleFailed {
		return x.failure
	}
	return nil
}
