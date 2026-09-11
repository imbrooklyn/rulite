package rulite

import (
	"context"
	"runtime/debug"
	"time"
)

type execution struct {
	result    Result
	causes    []error
	observer  Observer
	panicMode PanicMode
}

func execute[T any](ctx context.Context, input *T, snapshot *compiledSnapshot[T], config executionConfig, identity SnapshotInfo) (Result, error) {
	x := execution{result: Result{metadata: snapshot.metadata, identity: identity}, observer: config.observer, panicMode: config.panic}
	x.result.counts.Total = len(snapshot.metadata.rules)
	var start time.Time
	if config.trace {
		start = time.Now()
		x.result.trace = &traceData{rules: make([]ruleTiming, x.result.counts.Total)}
	}
	x.observeSummary(ctx, EventExecutionStarted)
	if x.contextDone(ctx) {
		return x.finish(ctx, start)
	}

	if len(snapshot.metadata.entries) == 0 {
		if executeRules(ctx, input, snapshot, config, &x, compiledEntry{end: len(snapshot.callbacks)}) {
			return x.finish(ctx, start)
		}
	} else {
		x.result.groups = make([]groupRecord, len(snapshot.metadata.groups))
		for _, entry := range snapshot.metadata.entries {
			stopped := executeRules(ctx, input, snapshot, config, &x, entry)
			if entry.group != nil {
				x.finishGroup(ctx, entry.group, stopped)
			}
			if stopped {
				return x.finish(ctx, start)
			}
		}
	}
	x.result.stop = StopCompleted
	return x.finish(ctx, start)
}

// executeRules is the only callback state machine for top-level rules and members.
func executeRules[T any](ctx context.Context, input *T, snapshot *compiledSnapshot[T], config executionConfig, x *execution, entry compiledEntry) bool {
	if entry.group != nil {
		if x.contextDone(ctx) {
			return true
		}
		x.result.groups[entry.group.index] = groupRecord{state: GroupInterrupted, skippedFrom: entry.end}
	}
	conditionCtx := ctx
	// A nested Fire must not write into the enclosing condition's recording.
	if !config.trace && recorderFrom(ctx) != nil {
		conditionCtx = context.WithValue(ctx, conditionRecorderKey{}, (*conditionRecording)(nil))
	}
	for order := entry.start; order < entry.end; order++ {
		callbacks := snapshot.callbacks[order]
		if (entry.group == nil || order != entry.start) && x.contextDone(ctx) {
			return true
		}
		id := snapshot.metadata.rules[order].id
		var node *conditionRecording
		var conditionStart time.Time
		if config.trace {
			node = &conditionRecording{view: ConditionTrace{index: -1}, started: true}
			conditionCtx = context.WithValue(ctx, conditionRecorderKey{}, node)
			conditionStart = time.Now()
		}
		x.result.evaluatedThrough = order + 1
		x.result.counts.Evaluated++
		matched, err, panicErr := callCondition(conditionCtx, input, callbacks.condition, id, config.panic)
		if config.trace {
			timing := &x.result.trace.rules[order]
			timing.condition = time.Since(conditionStart)
			if panicErr == nil {
				node.finish(matched, err)
			}
			if tree := node.freeze(panicErr); tree.kind != ConditionLeaf {
				timing.tree, timing.hasTree = tree, true
			}
		}
		if panicErr != nil {
			x.fail(order, ConditionPhase, panicErr, false, true)
			x.result.stop = StopPanic
			x.observeRule(ctx, EventRuleFailed, order, ConditionOutcomeError)
			x.contextDone(ctx)
			return true
		}
		if err != nil {
			x.fail(order, ConditionPhase, err, config.policy.conditionErrors == ContinueOnError, false)
			x.observeRule(ctx, EventRuleFailed, order, ConditionOutcomeError)
			if x.contextDone(ctx) {
				return true
			}
			if config.policy.conditionErrors == StopOnError {
				x.result.stop = StopConditionError
				return true
			}
			continue
		}
		if !matched {
			x.result.counts.Unmatched++
			x.observeRule(ctx, EventRuleEvaluated, order, ConditionOutcomeFalse)
			if x.contextDone(ctx) {
				return true
			}
			continue
		}

		if entry.group != nil && entry.group.kind == GroupFirstMatch {
			x.resolveGroup(entry.group, id)
		}
		x.observeRule(ctx, EventRuleEvaluated, order, ConditionOutcomeTrue)
		x.observeRule(ctx, EventRuleMatched, order, ConditionOutcomeTrue)
		if entry.group != nil && entry.group.kind == GroupFirstMatch {
			x.observeGroup(ctx, EventGroupResolved, entry.group)
		}
		// A successful match is preserved even when its action cannot start.
		if x.contextDone(ctx) {
			x.record(order, RuleSkipped)
			x.result.counts.Matched++
			x.result.counts.Skipped++
			return true
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
			x.observeRule(ctx, EventRuleFailed, order, ConditionOutcomeNotEvaluated)
			x.contextDone(ctx)
			return true
		}
		if err != nil {
			x.fail(order, ActionPhase, err, config.policy.actionErrors == ContinueOnError, false)
			x.observeRule(ctx, EventRuleFailed, order, ConditionOutcomeNotEvaluated)
		} else {
			x.record(order, RuleFired)
			x.result.counts.Matched++
			x.result.counts.Fired++
			if entry.group != nil && entry.group.kind == GroupFirstFire {
				x.resolveGroup(entry.group, id)
			}
			x.observeRule(ctx, EventRuleFired, order, ConditionOutcomeNotEvaluated)
			if entry.group != nil && entry.group.kind == GroupFirstFire {
				x.observeGroup(ctx, EventGroupResolved, entry.group)
			}
		}
		if x.contextDone(ctx) {
			return true
		}
		if err != nil && config.policy.actionErrors == StopOnError {
			x.result.stop = StopActionError
			return true
		}
		if config.policy.stop == StopOnFirstMatch {
			x.result.stop = StopFirstMatch
			return true
		}
		if err == nil && config.policy.stop == StopOnFirstFire {
			x.result.stop = StopFirstFire
			return true
		}
		if entry.group != nil && x.result.groups[entry.group.index].state == GroupResolved {
			x.result.groups[entry.group.index].skippedFrom = order + 1
			return false
		}
	}
	if entry.group != nil {
		x.result.groups[entry.group.index].state = GroupExhausted
	}
	return false
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

func (x *execution) finish(ctx context.Context, start time.Time) (Result, error) {
	x.result.counts.NotEvaluated = x.result.counts.Total - x.result.counts.Evaluated
	x.observeSummary(ctx, EventExecutionFinished)
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
