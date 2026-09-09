package cel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"regexp"

	celgo "cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/ext"
	"cel.dev/cel-go/interpreter"
	"github.com/imbrooklyn/rulite"
)

const maxSourceBytes = 4096

// Option is an immutable compiler setting. Its zero value leaves defaults intact.
type Option struct {
	costLimit    uint64
	hasCostLimit bool
}

// WithCostLimit sets a positive per-evaluation CEL cost budget. The default is
// 10,000. Zero is rejected by NewCompiler. Options apply in order; an invalid
// option cannot be repaired by a later one. Cost is work accounting, not a
// wall-clock deadline or memory limit.
func WithCostLimit(limit uint64) Option { return Option{limit, true} }

// Compiler is an immutable native schema and compilation configuration.
// Construct it with NewCompiler; its zero value is invalid. Compile can be
// called concurrently. It never evaluates input or executes an action.
type Compiler[T any] struct {
	env       *celgo.Env
	variable  string
	fields    []int
	costLimit uint64
}

var variableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// NewCompiler binds variable to a named, non-pointer Go struct T without a
// sample input. Returned conditions receive *T directly, without a field map
// conversion or clone. Exported fields use exact Go names, ignoring JSON tags.
// Supported schemas and fixed source/input limits are described in the CEL guide.
// Variable names are ASCII identifiers of 1-64 bytes and cannot be CEL keywords.
// Options are consumed immediately and their slice is not retained. On failure
// the compiler is nil and the error is a *CompileError with stage environment.
func NewCompiler[T any](variable string, options ...Option) (*Compiler[T], error) {
	fail := func(err error) (*Compiler[T], error) { return nil, &CompileError{stage: "environment", cause: err} }
	if !variableName.MatchString(variable) {
		return fail(errors.New("variable must be an ASCII identifier of 1-64 bytes"))
	}
	costLimit := uint64(10000)
	for _, option := range options {
		if option.hasCostLimit {
			if option.costLimit == 0 {
				return fail(errors.New("cost limit must be positive"))
			}
			costLimit = option.costLimit
		}
	}
	tp := reflect.TypeFor[T]()
	fields, err := nativeSchema(tp)
	if err != nil {
		return fail(err)
	}
	nt, err := types.NewNativeType(tp)
	if err != nil {
		return fail(err)
	}
	env, err := celgo.NewEnv(
		ext.NativeTypes(tp), celgo.Variable(variable, celgo.ObjectType(nt.TypeName())),
		celgo.ParserExpressionSizeLimit(maxSourceBytes), celgo.ParserRecursionLimit(64),
		celgo.ParserErrorRecoveryLimit(16), celgo.ExpressionNestingDepthLimit(64),
		celgo.ASTValidators(celgo.ValidateComprehensionNestingLimit(2), regexBudget{}),
	)
	if err != nil {
		return fail(err)
	}
	// Parsing and checking the binding itself also rejects reserved keywords and
	// names that resolve to CEL literals or types instead of the declared object.
	probe, issues := env.Compile(variable)
	if issues.Err() != nil {
		return fail(issues.Err())
	}
	if !probe.OutputType().IsExactType(celgo.ObjectType(nt.TypeName())) {
		return fail(errors.New("variable does not resolve to the native input type"))
	}
	return &Compiler[T]{env: env, variable: variable, fields: fields, costLimit: costLimit}, nil
}

// Compile parses source, type-checks it, requires an exact bool result, and
// builds one reusable program. It returns nil and *CompileError on failure.
// A nil or uninitialized compiler instead returns ErrInvalidCompiler.
// Source is limited to 4,096 bytes. Evaluation is synchronous and uses independent
// activation and cost state on each call. The condition observes cancellation
// before and after evaluation, with cooperative polling inside comprehensions.
func (c *Compiler[T]) Compile(source string) (rulite.Condition[T], error) {
	if c == nil || c.env == nil {
		return nil, ErrInvalidCompiler
	}
	if len(source) > maxSourceBytes {
		return nil, &CompileError{stage: "parse", cause: errors.New("source exceeds 4096 bytes")}
	}
	digest := sha256.Sum256([]byte(source))
	id := hex.EncodeToString(digest[:])
	program, err := compileProgram(c.env, source, id, c.costLimit)
	if err != nil {
		return nil, err
	}
	variable, fields := c.variable, c.fields
	return func(ctx context.Context, input *T) (bool, error) {
		if ctx == nil {
			return false, &RuntimeError{id, rulite.ErrNilContext}
		}
		if input == nil {
			return false, &RuntimeError{id, rulite.ErrNilInput}
		}
		if err := contextError(ctx); err != nil {
			return false, &RuntimeError{id, err}
		}
		if !withinInputLimits(reflect.ValueOf(input).Elem(), fields) {
			return false, &RuntimeError{id, ErrInputLimit}
		}
		return evaluate(ctx, program, &nativeActivation{variable, input}, id)
	}, nil
}

func compileProgram(env *celgo.Env, source, id string, costLimit uint64) (celgo.Program, error) {
	fail := func(stage string, err error) (celgo.Program, error) { return nil, &CompileError{id, stage, err} }
	parsed, issues := env.Parse(source)
	if issues.Err() != nil {
		return fail("parse", issues.Err())
	}
	checked, issues := env.Check(parsed)
	if issues.Err() != nil {
		return fail("check", issues.Err())
	}
	if !checked.OutputType().IsExactType(celgo.BoolType) {
		return fail("output", ErrNonBool)
	}
	program, err := env.Program(checked, celgo.EvalOptions(celgo.OptOptimize),
		celgo.CostLimit(costLimit), celgo.InterruptCheckFrequency(16))
	if err != nil {
		return fail("program", err)
	}
	return program, nil
}

type nativeActivation struct {
	name  string
	input any
}

// ResolveName exposes only the explicitly bound native input variable.
func (a *nativeActivation) ResolveName(name string) (any, bool) { return a.input, name == a.name }

// Parent reports that this activation has no inherited bindings.
func (*nativeActivation) Parent() interpreter.Activation { return nil }

func evaluate(ctx context.Context, program celgo.Program, activation any, id string) (bool, error) {
	value, _, err := program.ContextEval(ctx, activation)
	matched, err := boolOutcome(value, err)
	if canceled := contextError(ctx); canceled != nil {
		err = errors.Join(err, canceled)
	}
	if err != nil {
		return false, &RuntimeError{id, err}
	}
	return matched, nil
}

func boolOutcome(value ref.Val, err error) (bool, error) {
	if err != nil {
		var canceled interpreter.EvalCancelledError
		if errors.As(err, &canceled) && canceled.Cause == interpreter.CostLimitExceeded {
			err = errors.Join(ErrCostLimit, err)
		}
		return false, err
	}
	if types.IsUnknown(value) {
		return false, ErrUnknown
	}
	if types.IsError(value) {
		return false, value.(*types.Err)
	}
	if boolean, ok := value.(types.Bool); ok {
		return bool(boolean), nil
	}
	return false, fmt.Errorf("unexpected CEL result type: %T", value)
}

func contextError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(err, context.Cause(ctx))
	}
	return nil
}
