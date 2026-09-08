package rulite

import "context"

// Engine holds an immutable, validated snapshot of typed rules.
// Construct it with NewEngine or NewEngineFromRuleSet; its zero value is invalid.
// An engine has no mutable configuration. Callback closures are retained by reference, and
// callers remain responsible for synchronizing any shared captured state.
// An engine supports concurrent Fire calls with independently owned inputs.
type Engine[T any] struct {
	snapshot *compiledSnapshot[T]
	defaults executionConfig
}

// NewEngine is the convenience path for Compile followed by NewEngineFromRuleSet
// with default options. It validates rules and constructs an immutable engine without
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
	set, err := Compile(rules...)
	if err != nil {
		return nil, err
	}
	return NewEngineFromRuleSet(set)
}

// NewEngineFromRuleSet constructs an engine sharing set's compiled snapshot.
// It does not revalidate or sort rules, normalize metadata, copy executable
// nodes, or invoke callbacks. Each engine owns independent immutable defaults.
//
// A nil or uninitialized set returns nil and ErrInvalidRuleSet before options
// are inspected. A successfully compiled empty set is valid. Options use the
// same FireOption values and left-to-right validation as Fire; invalid options
// return nil and ErrInvalidPolicy or ErrInvalidPanicMode. The option slice is
// not retained. With no options, defaults are EvaluateAll, StopOnError for both
// phases, RecoverPanics, and tracing disabled.
//
// Fire applies its options to a value copy of these defaults. WithPolicy and
// WithPanicMode replace their values; WithTrace enables tracing, including as
// an engine default. There is no per-call option to disable an enabled trace.
func NewEngineFromRuleSet[T any](set *RuleSet[T], options ...FireOption) (*Engine[T], error) {
	if !set.Valid() {
		return nil, ErrInvalidRuleSet
	}
	config, err := configureExecution(executionConfig{}, options)
	if err != nil {
		return nil, err
	}
	return &Engine[T]{snapshot: set.snapshot, defaults: config}, nil
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
// Options apply to a value copy of the engine's defaults and affect only this call.
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
	config, err := configureExecution(e.defaults, options)
	if err != nil {
		return Result{}, err
	}
	return execute(ctx, input, snapshot, config)
}
