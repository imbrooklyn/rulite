package rulite

import "context"

// Engine holds an immutable, validated snapshot of typed rules.
// Construct it with NewEngine; its zero value is invalid. An engine has no
// mutable configuration. Callback closures are retained by reference, and
// callers remain responsible for synchronizing any shared captured state.
// An engine supports concurrent Fire calls with independently owned inputs.
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

// Fire executes the captured snapshot sequentially in compiled order. Each
// condition is followed immediately by its action when it returns true, nil.
// Earlier action mutations are visible to later conditions, including partial
// mutations before an error. Fire never rolls back, retries, or compensates.
// Conditions must not mutate input; the engine does not copy or inspect T.
//
// Preflight checks nil context, nil input, invalid engine, then options, in
// that order. Failure returns a direct sentinel and zero Result. Once started,
// any observed error returns *ExecutionError alongside the partial Result,
// even if ContinueOnError eventually reaches StopCompleted.
//
// Context is checked only at callback boundaries, including before an empty
// execution. Fire waits for each callback to return; it starts no goroutines
// to interrupt callbacks. A successful action remains fired if cancellation
// is observed afterward. Actions receive the caller's original context;
// traced conditions receive derived contexts preserving its context semantics.
//
// Panics are recovered with a stack and terminate execution by default.
// PropagatePanics preserves the panic and makes no promise to return a Result.
// Concurrent calls must synchronize shared input or callback captures.
func (e *Engine[T]) Fire(ctx context.Context, input *T, options ...FireOption) (Result, error) {
	if ctx == nil {
		return Result{}, ErrNilContext
	}
	if input == nil {
		return Result{}, ErrNilInput
	}
	if e == nil {
		return Result{}, ErrInvalidEngine
	}
	snapshot := e.snapshot
	if snapshot == nil {
		return Result{}, ErrInvalidEngine
	}
	config, err := configureExecution(options)
	if err != nil {
		return Result{}, err
	}
	return execute(ctx, input, snapshot, config)
}
