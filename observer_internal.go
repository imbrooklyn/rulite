package rulite

import (
	"context"
	"runtime/debug"
)

func (x *execution) observe(ctx context.Context, event Event) {
	if x.observer == nil {
		return
	}
	if err := callObserver(ctx, x.observer, event, x.panicMode); err != nil {
		x.result.diagnostics = append(x.result.diagnostics, Diagnostic{event: event, cause: err})
		x.observer = nil
	}
}

func (x *execution) observeRule(ctx context.Context, kind EventKind, order int, outcome ConditionOutcome) {
	if x.observer == nil {
		return
	}
	event := Event{kind: kind, rule: &x.result.metadata.rules[order], order: order, outcome: outcome}
	if kind == EventRuleFailed {
		// fail has already appended this rule's immutable canonical failure.
		event.failure = &x.result.failures[len(x.result.failures)-1]
		if event.failure.phase == ConditionPhase {
			event.kind, event.outcome = EventRuleEvaluated, ConditionOutcomeError
			x.observe(ctx, event)
			event.kind = EventRuleFailed
		}
	}
	x.observe(ctx, event)
}

func (x *execution) observeSummary(ctx context.Context, kind EventKind) {
	if x.observer == nil {
		return
	}
	summary := eventSummary{counts: x.result.counts, stop: x.result.stop}
	if kind == EventExecutionStarted {
		summary.counts.NotEvaluated = summary.counts.Total
	}
	x.observe(ctx, Event{kind: kind, summary: &summary})
}

func callObserver(ctx context.Context, observer Observer, event Event, mode PanicMode) (err error) {
	if mode == PropagatePanics {
		return observer.Observe(ctx, event)
	}
	returned := false
	defer func() {
		if !returned {
			value := recover()
			stack := debug.Stack()
			err = &ObserverPanicError{value: value, stack: stack}
		}
	}()
	err = observer.Observe(ctx, event)
	returned = true
	return
}
