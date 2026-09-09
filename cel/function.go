package cel

import (
	"errors"
	"reflect"
	"slices"

	celgo "cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

type function struct {
	name   string
	option celgo.EnvOption
}

// Function registers one trusted unary scalar function, with type inference from
// its callback. Scalars include named primitives, time.Time and time.Duration.
// Functions must be pure, read-only and bounded; callers synchronize captures.
// They execute synchronously, cannot be preempted by CEL cost or cancellation,
// and are charged CEL's default unit call cost after returning. No Go symbol or
// method lookup is performed. CEL error propagation rules apply to returned errors.
// Panics become ErrFunctionPanic without exposing or formatting the panic value;
// this third-party evaluation boundary is independent of root PanicMode.
func (b *Builder[T]) Function[A, R any](name string, callback func(A) (R, error)) error {
	if b == nil || !b.valid {
		return ErrInvalidCompiler
	}
	if callback == nil {
		return environmentError(errors.New("function must not be nil"))
	}
	if len(b.functions) >= 64 {
		return environmentError(errors.New("function count exceeds 64"))
	}
	if err := b.checkName(name); err != nil {
		return environmentError(err)
	}
	arg, result := reflect.TypeFor[A](), reflect.TypeFor[R]()
	for _, tp := range []reflect.Type{arg, result} {
		if scalarType(tp) == nil || tp.Implements(reflect.TypeFor[ref.Val]()) || reflect.PointerTo(tp).Implements(reflect.TypeFor[ref.Val]()) {
			return environmentError(errors.New("trusted function arguments and results must be native scalars"))
		}
	}
	option := celgo.Function(name, celgo.Overload("rulite_trusted_"+name, []*celgo.Type{scalarType(arg)}, scalarType(result), celgo.UnaryBinding(func(value ref.Val) (out ref.Val) {
		defer func() {
			if recover() != nil {
				out = types.WrapErr(ErrFunctionPanic)
			}
		}()
		if text, ok := value.(types.String); ok && len(text) > maxInputBytes {
			return types.WrapErr(ErrInputLimit)
		}
		native, err := value.ConvertToNative(arg)
		if err != nil {
			return types.WrapErr(err)
		}
		answer, err := callback(native.(A))
		if err != nil {
			return types.WrapErr(err)
		}
		v := reflect.ValueOf(answer)
		if v.Kind() == reflect.String && v.Len() > maxInputBytes {
			return types.WrapErr(ErrInputLimit)
		}
		return types.DefaultTypeAdapter.NativeToValue(answer)
	})))
	functions := append(slices.Clone(b.functions), function{name, option})
	if _, _, err := buildEnvironment(b.bindings, functions, b.jsonNames); err != nil {
		return environmentError(err)
	}
	b.functions = functions
	return nil
}
