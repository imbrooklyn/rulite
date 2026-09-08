package rulite

import (
	"context"
	"runtime/debug"
	"time"
)

type execution struct {
	result Result
	causes []error
}

func execute[T any](ctx context.Context, input *T, snapshot *compiledSnapshot[T], config executionConfig) (Result, error) {
	x := execution{result: Result{metadata: snapshot.metadata}}
	x.result.counts.Total = len(snapshot.metadata.rules)
	var start time.Time
	if config.trace {
		start = time.Now()
		x.result.trace = &traceData{rules: make([]ruleTiming, x.result.counts.Total)}
	}
	if x.contextDone(ctx) {
		return x.finish(start)
	}

	conditionCtx := ctx
	// A nested Fire must not write into the enclosing condition's recording.
	if !config.trace && recorderFrom(ctx) != nil {
		conditionCtx = context.WithValue(ctx, conditionRecorderKey{}, (*conditionRecording)(nil))
	}
	for order, callbacks := range snapshot.callbacks {
		if x.contextDone(ctx) {
			return x.finish(start)
		}
		id := snapshot.metadata.rules[order].id
		var node *conditionRecording
		var conditionStart time.Time
		if config.trace {
			node = &conditionRecording{view: ConditionTrace{index: -1}, started: true}
			conditionCtx = context.WithValue(ctx, conditionRecorderKey{}, node)
			conditionStart = time.Now()
		}
		x.result.counts.Evaluated++
		matched, err, panicErr := callCondition(conditionCtx, input, callbacks.condition, id, config.panic)
		if config.trace {
			timing := &x.result.trace.rules[order]
			timing.condition = time.Since(conditionStart)
			if panicErr == nil {
				node.finish(matched, err)
			}
			if node.view.kind != ConditionLeaf {
				tree := node.freeze(panicErr)
				if tree.kind != ConditionLeaf {
					timing.tree, timing.hasTree = tree, true
				}
			}
		}
		if panicErr != nil {
			x.fail(order, ConditionPhase, panicErr, false, true)
			x.result.stop = StopPanic
			x.contextDone(ctx)
			return x.finish(start)
		}
		if err != nil {
			x.fail(order, ConditionPhase, err, config.policy.conditionErrors == ContinueOnError, false)
			if x.contextDone(ctx) {
				return x.finish(start)
			}
			if config.policy.conditionErrors == StopOnError {
				x.result.stop = StopConditionError
				return x.finish(start)
			}
			continue
		}
		if !matched {
			x.result.counts.Unmatched++
			if x.contextDone(ctx) {
				return x.finish(start)
			}
			continue
		}

		// A successful match is preserved even when its action cannot start.
		if x.contextDone(ctx) {
			x.record(order, RuleSkipped)
			x.result.counts.Matched++
			x.result.counts.Skipped++
			return x.finish(start)
		}
		var actionStart time.Time
		if config.trace {
			actionStart = time.Now()
		}
		err, panicErr = callAction(ctx, input, callbacks.action, id, config.panic)
		if config.trace {
			x.result.trace.rules[order].action = time.Since(actionStart)
		}
		if panicErr != nil {
			x.fail(order, ActionPhase, panicErr, false, true)
			x.result.stop = StopPanic
			x.contextDone(ctx)
			return x.finish(start)
		}
		if err != nil {
			x.fail(order, ActionPhase, err, config.policy.actionErrors == ContinueOnError, false)
		} else {
			x.record(order, RuleFired)
			x.result.counts.Matched++
			x.result.counts.Fired++
		}
		if x.contextDone(ctx) {
			return x.finish(start)
		}
		if err != nil && config.policy.actionErrors == StopOnError {
			x.result.stop = StopActionError
			return x.finish(start)
		}
		if config.policy.stop == StopOnFirstMatch {
			x.result.stop = StopFirstMatch
			return x.finish(start)
		}
		if err == nil && config.policy.stop == StopOnFirstFire {
			x.result.stop = StopFirstFire
			return x.finish(start)
		}
	}
	x.result.stop = StopCompleted
	return x.finish(start)
}

func (x *execution) record(order int, state RuleState) {
	x.result.records = append(x.result.records, executionRecord{order: order, state: state})
}

func (x *execution) fail(order int, phase Phase, cause error, continued, recovered bool) {
	failure := Failure{ruleID: x.result.metadata.rules[order].id, phase: phase, cause: cause, continued: continued}
	x.result.records = append(x.result.records, executionRecord{order: order, state: RuleFailed, failureIndex: len(x.result.failures)})
	x.result.failures = append(x.result.failures, failure)
	x.causes = append(x.causes, failure)
	c := &x.result.counts
	c.Failed++
	if phase == ConditionPhase {
		c.ConditionFailed++
	} else {
		c.ActionFailed++
		c.Matched++
	}
	if recovered {
		c.PanicRecovered++
	}
}

func (x *execution) contextDone(ctx context.Context) bool {
	err := ctx.Err()
	if err == nil {
		return false
	}
	x.causes = append(x.causes, err)
	if cause := context.Cause(ctx); cause != nil && cause != err {
		x.causes = append(x.causes, cause)
	}
	if x.result.stop != StopPanic {
		if err == context.DeadlineExceeded {
			x.result.stop = StopContextDeadlineExceeded
		} else {
			x.result.stop = StopContextCanceled
		}
	}
	return true
}

func (x *execution) finish(start time.Time) (Result, error) {
	x.result.counts.NotEvaluated = x.result.counts.Total - x.result.counts.Evaluated
	if x.result.trace != nil {
		x.result.trace.duration = time.Since(start)
	}
	if len(x.causes) == 0 {
		return x.result, nil
	}
	return x.result, &ExecutionError{causes: x.causes, failures: x.result.failures}
}

func callCondition[T any](ctx context.Context, input *T, condition Condition[T], id RuleID, mode PanicMode) (matched bool, err error, panicErr *PanicError) {
	if mode == PropagatePanics {
		matched, err = condition(ctx, input)
		return
	}
	returned := false
	defer func() {
		if !returned {
			value := recover()
			stack := debug.Stack()
			panicErr = &PanicError{ruleID: id, phase: ConditionPhase, value: value, stack: stack}
		}
	}()
	matched, err = condition(ctx, input)
	returned = true
	return
}

func callAction[T any](ctx context.Context, input *T, action Action[T], id RuleID, mode PanicMode) (err error, panicErr *PanicError) {
	if mode == PropagatePanics {
		err = action(ctx, input)
		return
	}
	returned := false
	defer func() {
		if !returned {
			value := recover()
			stack := debug.Stack()
			panicErr = &PanicError{ruleID: id, phase: ActionPhase, value: value, stack: stack}
		}
	}()
	err = action(ctx, input)
	returned = true
	return
}
