package rulite

// RuleID is a stable rule identity, unique within an engine.
// It must contain 1 to 128 bytes and match:
//
//	^[a-z0-9][a-z0-9._/-]{0,127}$
//
// IDs are compared byte for byte without trimming or normalization.
type RuleID string

// Priority determines rule order. Higher values come first; equal values
// preserve the registration order passed to NewEngine.
type Priority int32

// DefaultPriority is the priority assigned by NewRule.
const DefaultPriority Priority = 0

// Rule is an immutable definition containing an ID, priority, condition, and
// action. Construct rules with NewRule. The zero value is invalid and is
// rejected by NewEngine. Copying a rule does not clone callback closures.
type Rule[T any] struct {
	id        RuleID
	priority  Priority
	condition Condition[T]
	action    Action[T]
}

// ID returns the rule's identity. It returns an empty ID for the zero value.
func (r Rule[T]) ID() RuleID {
	return r.id
}

// Priority returns the rule's priority, including DefaultPriority for the zero
// value. A higher priority places a rule earlier in the engine's order.
func (r Rule[T]) Priority() Priority {
	return r.priority
}

// RuleBuilder is the immutable stage for selecting a priority and condition.
// Its methods return new values, so a builder can be reused independently.
// Use NewRule to assign an ID; the zero value has an invalid, empty ID.
type RuleBuilder[T any] struct {
	id       RuleID
	priority Priority
}

// ActionBuilder is the immutable stage for supplying a rule's action after
// its condition has been selected. Then completes the rule. Its zero value
// has an invalid, empty ID and a nil condition.
type ActionBuilder[T any] struct {
	id        RuleID
	priority  Priority
	condition Condition[T]
}

// NewRule starts a rule definition with id and DefaultPriority.
// T should be a non-pointer business state type; callbacks receive *T.
// Validation is deferred to NewEngine, so an invalid ID does not panic here.
func NewRule[T any](id RuleID) RuleBuilder[T] {
	return RuleBuilder[T]{id: id, priority: DefaultPriority}
}

// Priority returns a builder with priority, leaving b unchanged.
// Repeated calls use the last supplied value. Every Priority value is valid.
func (b RuleBuilder[T]) Priority(priority Priority) RuleBuilder[T] {
	b.priority = priority
	return b
}

// When returns the action stage with condition, leaving b unchanged.
// A nil condition is accepted here and rejected by NewEngine.
func (b RuleBuilder[T]) When(condition Condition[T]) ActionBuilder[T] {
	return ActionBuilder[T]{id: b.id, priority: b.priority, condition: condition}
}

// Then returns a completed rule with action, leaving b unchanged.
// A nil action is accepted here and rejected by NewEngine.
func (b ActionBuilder[T]) Then(action Action[T]) Rule[T] {
	return Rule[T]{id: b.id, priority: b.priority, condition: b.condition, action: action}
}
