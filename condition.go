package rulite

import (
	"context"
	"slices"
)

// Condition decides whether a rule matches the caller-owned state.
// T should be a non-pointer business state type. Conditions must not mutate
// the state. A non-nil error means evaluation failed, and the boolean result
// is ignored. Callers must synchronize shared callback captures.
type Condition[T any] func(context.Context, *T) (bool, error)

// All returns a condition that evaluates children from left to right and stops
// at the first false result or non-nil error. An error is propagated unchanged,
// with a false result, regardless of the child's boolean result. With no
// children, All[T]() returns a condition that returns true, nil.
//
// The supplied slice is copied. A nil child returns false, ErrInvalidCondition
// when reached; children after a short circuit are never evaluated.
func All[T any](conditions ...Condition[T]) Condition[T] {
	children := slices.Clone(conditions)
	return func(ctx context.Context, input *T) (bool, error) {
		for _, child := range children {
			if child == nil {
				return false, ErrInvalidCondition
			}
			matched, err := child(ctx, input)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}
}

// Any returns a condition that evaluates children from left to right and stops
// at the first true result or non-nil error. An error is propagated unchanged,
// with a false result, regardless of the child's boolean result. With no
// children, Any[T]() returns a condition that returns false, nil.
//
// The supplied slice is copied. A nil child returns false, ErrInvalidCondition
// when reached; children after a short circuit are never evaluated.
func Any[T any](conditions ...Condition[T]) Condition[T] {
	children := slices.Clone(conditions)
	return func(ctx context.Context, input *T) (bool, error) {
		for _, child := range children {
			if child == nil {
				return false, ErrInvalidCondition
			}
			matched, err := child(ctx, input)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}
}

// Not returns a condition that negates a successful child result.
// A child error is propagated unchanged with a false result, regardless of
// the child's boolean result. A nil child returns false, ErrInvalidCondition
// when the returned condition is called.
func Not[T any](condition Condition[T]) Condition[T] {
	return func(ctx context.Context, input *T) (bool, error) {
		if condition == nil {
			return false, ErrInvalidCondition
		}
		matched, err := condition(ctx, input)
		if err != nil {
			return false, err
		}
		return !matched, nil
	}
}
