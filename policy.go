package rulite

// StopMode determines when successful condition or action outcomes end execution.
type StopMode uint8

const (
	// EvaluateAll visits every top-level entry unless an error, panic, or context
	// stops it. Groups still apply their local member selection.
	EvaluateAll StopMode = iota
	// StopOnFirstMatch stops after the first matched rule's action attempt.
	StopOnFirstMatch
	// StopOnFirstFire stops only after an action returns nil.
	StopOnFirstFire
)

// ErrorMode determines the disposition of an ordinary callback error.
// It never permits execution to continue after a panic or context cancellation.
type ErrorMode uint8

const (
	// StopOnError ends execution after a callback error.
	StopOnError ErrorMode = iota
	// ContinueOnError permits processing to continue, subject to other stops.
	ContinueOnError
)

// ExecutionPolicy is an immutable value. Its zero value is DefaultPolicy().
// With methods return new values; Fire and NewEngineFromRuleSet validate every supplied policy option.
type ExecutionPolicy struct {
	stop            StopMode
	conditionErrors ErrorMode
	actionErrors    ErrorMode
}

// DefaultPolicy returns EvaluateAll with StopOnError for both callback phases.
func DefaultPolicy() ExecutionPolicy { return ExecutionPolicy{} }

// StopMode returns the stopping mode.
func (p ExecutionPolicy) StopMode() StopMode { return p.stop }

// ConditionErrorMode returns the disposition of condition errors.
func (p ExecutionPolicy) ConditionErrorMode() ErrorMode { return p.conditionErrors }

// ActionErrorMode returns the disposition of action errors.
func (p ExecutionPolicy) ActionErrorMode() ErrorMode { return p.actionErrors }

// WithStop returns a copy with mode as its stopping mode.
func (p ExecutionPolicy) WithStop(mode StopMode) ExecutionPolicy {
	p.stop = mode
	return p
}

// WithConditionErrors returns a copy with mode for condition errors.
func (p ExecutionPolicy) WithConditionErrors(mode ErrorMode) ExecutionPolicy {
	p.conditionErrors = mode
	return p
}

// WithActionErrors returns a copy with mode for action errors.
func (p ExecutionPolicy) WithActionErrors(mode ErrorMode) ExecutionPolicy {
	p.actionErrors = mode
	return p
}

// PanicMode determines whether business and observer panics are recovered or propagated.
type PanicMode uint8

const (
	// RecoverPanics records a business PanicError and terminates execution.
	// Observer panics become diagnostics and disable observation for this Fire.
	RecoverPanics PanicMode = iota
	// PropagatePanics lets the original panic propagate to the caller.
	PropagatePanics
)

// FireOption is an immutable option for Fire or NewEngineFromRuleSet defaults.
// Its zero value is a no-op.
// Options apply from left to right. Every nonzero option is validated when
// reached, so a later option cannot repair an earlier invalid option.
type FireOption struct {
	kind     optionKind
	policy   ExecutionPolicy
	panic    PanicMode
	observer Observer
}

type optionKind uint8

const (
	optionNone optionKind = iota
	optionPolicy
	optionTrace
	optionPanic
	optionObserver
)

// WithPolicy replaces the entire execution policy for Fire or an engine default.
func WithPolicy(policy ExecutionPolicy) FireOption {
	return FireOption{kind: optionPolicy, policy: policy}
}

// WithTrace enables complete tracing for Fire or an engine default.
// Repeating it is idempotent. With tracing disabled, Fire does not read a
// duration clock or collect trees.
func WithTrace() FireOption { return FireOption{kind: optionTrace} }

// WithPanicMode selects business and observer panic handling for Fire or an engine default.
func WithPanicMode(mode PanicMode) FireOption { return FireOption{kind: optionPanic, panic: mode} }

type executionConfig struct {
	policy   ExecutionPolicy
	panic    PanicMode
	trace    bool
	observer Observer
}

func configureExecution(config executionConfig, options []FireOption) (executionConfig, error) {
	for _, option := range options {
		switch option.kind {
		case optionNone:
		case optionPolicy:
			p := option.policy
			if p.stop > StopOnFirstFire || p.conditionErrors > ContinueOnError || p.actionErrors > ContinueOnError {
				return executionConfig{}, ErrInvalidPolicy
			}
			config.policy = p
		case optionTrace:
			config.trace = true
		case optionObserver:
			config.observer = option.observer
		case optionPanic:
			if option.panic > PropagatePanics {
				return executionConfig{}, ErrInvalidPanicMode
			}
			config.panic = option.panic
		}
	}
	return config, nil
}
