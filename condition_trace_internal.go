package rulite

import "context"

type conditionRecorderKey struct{}

// Mutable recordings never escape into Result. Freezing copies the entire
// tree, so even a callback retaining its derived context cannot alter a result.
type conditionRecording struct {
	view     ConditionTrace
	started  bool
	finished bool
	operator bool
	opaque   bool
	children []*conditionRecording
}

func recorderFrom(ctx context.Context) *conditionRecording {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(conditionRecorderKey{}).(*conditionRecording)
	return recorder
}

func (r *conditionRecording) finish(matched bool, err error) {
	r.finished = true
	outcome := conditionOutcome(matched, err)
	// A callback can invoke a combinator and then replace its result. Such
	// control flow has no observable operator topology at this node.
	if r.operator && r.view.outcome != outcome {
		r.opaque = true
	}
	r.view.err = err
	r.view.outcome = outcome
}

func conditionOutcome(matched bool, err error) ConditionOutcome {
	switch {
	case err != nil:
		return ConditionOutcomeError
	case matched:
		return ConditionOutcomeTrue
	default:
		return ConditionOutcomeFalse
	}
}

func (r *conditionRecording) freeze(panicErr *PanicError) ConditionTrace {
	view := r.view
	if r.started && !r.finished && panicErr != nil {
		view.outcome = ConditionOutcomeError
		view.err = panicErr
	}
	if r.opaque {
		view.kind = ConditionLeaf
	} else if len(r.children) > 0 {
		view.children = make([]ConditionTrace, len(r.children))
		for index, child := range r.children {
			view.children[index] = child.freeze(panicErr)
		}
	}
	return view
}

func traceCombined[T any](ctx context.Context, input *T, recorder *conditionRecording, kind ConditionKind, children []Condition[T]) (bool, error) {
	if recorder.operator {
		recorder.opaque = true
	}
	recorder.operator = true
	recorder.view.kind = kind
	recorder.children = make([]*conditionRecording, len(children))
	for index := range children {
		recorder.children[index] = &conditionRecording{view: ConditionTrace{index: index, reason: ConditionNotEvaluatedShortCircuit}}
	}
	matched, err := traceChildren(ctx, input, recorder, kind, children)
	recorder.view.outcome = conditionOutcome(matched, err)
	return matched, err
}

func traceChildren[T any](ctx context.Context, input *T, recorder *conditionRecording, kind ConditionKind, children []Condition[T]) (bool, error) {
	for index, child := range children {
		node := recorder.children[index]
		node.started = true
		node.view.reason = ConditionNotEvaluatedNone
		var matched bool
		var err error
		if child == nil {
			err = ErrInvalidCondition
		} else {
			matched, err = child(context.WithValue(ctx, conditionRecorderKey{}, node), input)
		}
		node.finish(matched, err)
		if err != nil {
			return false, err
		}
		switch kind {
		case ConditionAll:
			if !matched {
				return false, nil
			}
		case ConditionAny:
			if matched {
				return true, nil
			}
		case ConditionNot:
			return !matched, nil
		}
	}
	return kind == ConditionAll, nil
}
