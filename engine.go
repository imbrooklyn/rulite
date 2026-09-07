package rulite

// Engine holds an immutable, validated snapshot of typed rules.
// Construct it with NewEngine; its zero value is invalid. An engine has no
// mutable configuration. Callback closures are retained by reference, and
// callers remain responsible for synchronizing any shared captured state.
type Engine[T any] struct {
	snapshot *compiledSnapshot[T]
}

// NewEngine validates rules and constructs an immutable engine without
// invoking any condition or action. It copies the definitions, fixes their
// registration indexes, and orders them by descending priority, preserving
// registration order for equal priorities. The supplied slice is not changed
// or retained. Concurrent calls may share a rule slice that is only read.
//
// Validation aggregates ID syntax, duplicate valid ID, nil condition, and nil
// action issues in registration order. On failure it returns nil and a
// *ValidationError. Invalid IDs do not participate in duplicate detection.
// The zero Rule is invalid and contributes multiple issues.
//
// An empty rule list is valid. With no arguments, the type parameter must be
// explicit: NewEngine[T]().
func NewEngine[T any](rules ...Rule[T]) (*Engine[T], error) {
	snapshot, err := compileRules(rules)
	if err != nil {
		return nil, err
	}
	return &Engine[T]{snapshot: snapshot}, nil
}
