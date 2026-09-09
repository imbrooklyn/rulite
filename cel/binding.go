package cel

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"

	celgo "cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/ast"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/interpreter"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type binding[T any] struct {
	name       string
	tp         reflect.Type
	descriptor protoreflect.MessageDescriptor
	project    func(context.Context, *T) (any, error)
}

// Builder registers explicit typed bindings and trusted functions. Registration
// and Build require caller synchronization. Build creates an independent immutable
// compiler; later registrations do not change it. The zero value is invalid.
type Builder[T any] struct {
	valid     bool
	costLimit uint64
	jsonNames bool
	bindings  []binding[T]
	functions []function
}

// NewBuilder creates an empty registration builder. It consumes options without
// retaining their slice. Only projected values are visible to CEL; T itself need
// not be a supported CEL type. Build does not call projectors or trusted functions.
func NewBuilder[T any](options ...Option) (*Builder[T], error) {
	b := &Builder[T]{valid: true, costLimit: 10000}
	for _, option := range options {
		if option.hasCostLimit {
			if option.costLimit == 0 {
				return nil, environmentError(errors.New("cost limit must be positive"))
			}
			b.costLimit = option.costLimit
		}
		b.jsonNames = b.jsonNames || option.jsonNames
	}
	return b, nil
}

// Bind exposes a native value through a typed, synchronous, read-only projector.
// Projectors run once per condition call in registration order, including unused
// bindings. Errors retain their cause as ordinary RuntimeError values. A nil
// projected pointer is ErrNilBinding; use a presence boolean for optional roots.
// Shared captures and returned mutable storage require caller synchronization.
func (b *Builder[T]) Bind[V any](name string, project func(context.Context, *T) (V, error)) error {
	if project == nil {
		return environmentError(errors.New("projector must not be nil"))
	}
	return b.add(binding[T]{name: name, tp: reflect.TypeFor[V](), project: func(ctx context.Context, input *T) (any, error) { return project(ctx, input) }})
}

// BindProto exposes a protobuf message with an explicit immutable descriptor.
// Only descriptor field names are used, including presence, enums and oneofs.
// Each returned message must use this exact descriptor. Generated and dynamic
// messages are supported without keeping a sample message or consulting a global
// registry. Descriptor implementations and projectors are trusted caller objects.
func (b *Builder[T]) BindProto[V proto.Message](name string, descriptor protoreflect.MessageDescriptor, project func(context.Context, *T) (V, error)) error {
	if descriptor == nil || project == nil {
		return environmentError(errors.New("descriptor and projector must not be nil"))
	}
	return b.add(binding[T]{name: name, tp: reflect.TypeFor[V](), descriptor: descriptor, project: func(ctx context.Context, input *T) (any, error) { return project(ctx, input) }})
}

func (b *Builder[T]) bindInput(name string) error {
	tp := reflect.TypeFor[T]()
	if tp.Kind() != reflect.Struct || tp.Name() == "" || scalarType(tp) != nil {
		return environmentError(errors.New("input must be a named native struct"))
	}
	return b.Bind(name, func(_ context.Context, input *T) (*T, error) { return input, nil })
}

func (b *Builder[T]) add(entry binding[T]) error {
	if b == nil || !b.valid {
		return ErrInvalidCompiler
	}
	if len(b.bindings) >= 64 {
		return environmentError(errors.New("binding count exceeds 64"))
	}
	if err := b.checkName(entry.name); err != nil {
		return environmentError(err)
	}
	// Validate a candidate snapshot so a failed registration never poisons b.
	bindings := append(slices.Clone(b.bindings), entry)
	if _, _, err := buildEnvironment(bindings, b.functions, b.jsonNames); err != nil {
		return environmentError(err)
	}
	b.bindings = bindings
	return nil
}

func (b *Builder[T]) checkName(name string) error {
	if !variableName.MatchString(name) || strings.HasPrefix(name, "__") {
		return errors.New("name must be an ASCII identifier of 1-64 bytes without a reserved prefix")
	}
	for _, entry := range b.bindings {
		if entry.name == name {
			return errors.New("duplicate binding or function name")
		}
	}
	for _, entry := range b.functions {
		if entry.name == name {
			return errors.New("duplicate binding or function name")
		}
	}
	env, err := celgo.NewEnv(celgo.OptionalTypes())
	if err != nil {
		return err
	}
	if env.HasFunction(name) {
		return errors.New("name is reserved by CEL")
	}
	for _, macro := range env.Macros() {
		if macro.Function() == name {
			return errors.New("name is reserved by CEL")
		}
	}
	if _, found := env.CELTypeProvider().FindIdent(name); found {
		return errors.New("name is reserved by CEL")
	}
	parsed, issues := env.Parse(name)
	if issues.Err() != nil {
		return issues.Err()
	}
	if parsed.NativeRep().Expr().Kind() != ast.IdentKind {
		return errors.New("name is reserved by CEL")
	}
	return nil
}

// Build freezes the current bindings and functions into a reusable compiler.
// Registration may continue afterward without changing any previously built
// compiler or condition. No input, projector or trusted function is evaluated.
func (b *Builder[T]) Build() (*Compiler[T], error) {
	if b == nil || !b.valid {
		return nil, ErrInvalidCompiler
	}
	bindings, functions := slices.Clone(b.bindings), slices.Clone(b.functions)
	env, schema, err := buildEnvironment(bindings, functions, b.jsonNames)
	if err != nil {
		return nil, environmentError(err)
	}
	return &Compiler[T]{env: env, bindings: bindings, schema: schema, costLimit: b.costLimit}, nil
}

func environmentError(err error) error { return &CompileError{stage: "environment", cause: err} }

func buildEnvironment[T any](bindings []binding[T], functions []function, jsonNames bool) (*celgo.Env, *schema, error) {
	s := newSchema(jsonNames)
	options := []celgo.EnvOption{
		celgo.OptionalTypes(), celgo.ParserExpressionSizeLimit(maxSourceBytes), celgo.ParserRecursionLimit(64),
		celgo.ParserErrorRecoveryLimit(16), celgo.ExpressionNestingDepthLimit(64),
		celgo.ASTValidators(celgo.ValidateComprehensionNestingLimit(2), regexBudget{}),
	}
	for _, entry := range bindings {
		var ct *celgo.Type
		var err error
		if entry.descriptor != nil {
			ct, err = s.addProto(entry.descriptor)
		} else {
			ct, err = s.native(entry.tp, 0)
		}
		if err != nil {
			return nil, nil, err
		}
		options = append(options, celgo.Variable(entry.name, ct))
	}
	registry, err := s.registry()
	if err != nil {
		return nil, nil, err
	}
	adapter := &nativeAdapter{registry, s}
	options = append(options, celgo.CustomTypeProvider(&nativeProvider{registry, adapter}), celgo.CustomTypeAdapter(adapter))
	for _, function := range functions {
		options = append(options, function.option)
	}
	env, err := celgo.NewEnv(options...)
	if err != nil {
		return nil, nil, err
	}
	for _, entry := range bindings {
		if _, issues := env.Compile(entry.name); issues.Err() != nil {
			return nil, nil, issues.Err()
		}
	}
	return env, s, nil
}

type projectedActivation[T any] struct {
	bindings []binding[T]
	values   []any
}

// ResolveName returns the value projected for this evaluation only.
func (a *projectedActivation[T]) ResolveName(name string) (any, bool) {
	for i, candidate := range a.bindings {
		if candidate.name == name {
			return a.values[i], true
		}
	}
	return nil, false
}

// Parent reports that projected bindings have no inherited activation.
func (*projectedActivation[T]) Parent() interpreter.Activation { return nil }

func projectBindings[T any](ctx context.Context, input *T, bindings []binding[T], schema *schema) (interpreter.Activation, error) {
	budget := inputBudget{bytes: maxInputBytes, items: maxListItems, nodes: 65536, ctx: ctx}
	var single any
	var values []any
	if len(bindings) > 1 {
		values = make([]any, len(bindings))
	}
	for i, binding := range bindings {
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		value, err := binding.project(ctx, input)
		if err != nil {
			return nil, err
		}
		if err := contextError(ctx); err != nil {
			return nil, err
		}
		v := reflect.ValueOf(value)
		if !v.IsValid() || v.Kind() == reflect.Pointer && v.IsNil() {
			return nil, ErrNilBinding
		}
		if binding.descriptor != nil {
			message := value.(proto.Message).ProtoReflect()
			if message.Descriptor() != binding.descriptor {
				return nil, ErrProtoDescriptor
			}
			err = budget.message(message, 0)
		} else {
			err = budget.native(v, schema, 0)
		}
		if err != nil {
			return nil, err
		}
		if len(bindings) == 1 {
			single = value
		} else {
			values[i] = value
		}
	}
	if len(bindings) == 1 {
		return &nativeActivation{bindings[0].name, single}, nil
	}
	return &projectedActivation[T]{bindings, values}, nil
}

var _ types.StructTypeDescriptor = (*nativeType)(nil)
