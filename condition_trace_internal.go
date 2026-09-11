package rulite

import (
	"context"
	"sync"
)

type conditionRecorderKey struct{}

// Mutable recordings never escape into Result. Freezing copies the entire
// tree, so even a callback retaining its derived context cannot alter a result.
type conditionRecording struct {
	mu       sync.Mutex
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
	r.mu.Lock()
	defer r.mu.Unlock()
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
	r.mu.Lock()
	view := r.view
	children := r.children
	if r.started && !r.finished && panicErr != nil {
		view.outcome = ConditionOutcomeError
		view.err = panicErr
	}
	if r.opaque {
		view.kind = ConditionLeaf
		children = nil
	}
	r.mu.Unlock()
	if len(children) > 0 {
		view.children = make([]ConditionTrace, len(children))
		for index, child := range children {
			view.children[index] = child.freeze(panicErr)
		}
	}
	return view
}

func traceCombined[T any](ctx context.Context, input *T, recorder *conditionRecording, kind ConditionKind, children []Condition[T]) (bool, error) {
	// Each invocation owns its child slice. A callback may reuse this context
	// recursively or concurrently, making the enclosing node opaque.
	nodes := make([]*conditionRecording, len(children))
	for index := range children {
		nodes[index] = &conditionRecording{view: ConditionTrace{index: index, reason: ConditionNotEvaluatedShortCircuit}}
	}
	recorder.mu.Lock()
	if recorder.operator || recorder.finished {
		recorder.opaque = true
	}
	recorder.operator = true
	recorder.view.kind = kind
	recorder.children = nodes
	recorder.mu.Unlock()
	// Never hold a recording lock while running user code.
	matched, err := traceChildren(ctx, input, nodes, kind, children)
	recorder.mu.Lock()
	if !recorder.finished {
		recorder.view.outcome = conditionOutcome(matched, err)
	}
	recorder.mu.Unlock()
	return matched, err
}

func traceChildren[T any](ctx context.Context, input *T, nodes []*conditionRecording, kind ConditionKind, children []Condition[T]) (bool, error) {
	for index, child := range children {
		node := nodes[index]
		node.mu.Lock()
		node.started = true
		node.view.reason = ConditionNotEvaluatedNone
		node.mu.Unlock()
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
