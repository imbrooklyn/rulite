package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/imbrooklyn/rulite"
)

type observedFact struct {
	kind    rulite.EventKind
	id      rulite.RuleID
	phase   rulite.Phase
	outcome rulite.ConditionOutcome
}

func expectedEvents(result rulite.Result) []observedFact {
	facts := []observedFact{{kind: rulite.EventExecutionStarted}}
	for _, rule := range result.Explain().Rules() {
		if !rule.Evaluated() {
			continue
		}
		outcome := rulite.ConditionOutcomeFalse
		if rule.Matched() {
			outcome = rulite.ConditionOutcomeTrue
		}
		failure, failed := rule.Error().(rulite.Failure)
		if failed && failure.Phase() == rulite.ConditionPhase {
			outcome = rulite.ConditionOutcomeError
		}
		facts = append(facts, observedFact{rulite.EventRuleEvaluated, rule.ID(), rulite.ConditionPhase, outcome})
		if rule.Matched() {
			facts = append(facts, observedFact{rulite.EventRuleMatched, rule.ID(), rulite.ConditionPhase, rulite.ConditionOutcomeTrue})
		}
		if failed {
			facts = append(facts, observedFact{kind: rulite.EventRuleFailed, id: rule.ID(), phase: failure.Phase()})
		}
		if rule.Fired() {
			facts = append(facts, observedFact{kind: rulite.EventRuleFired, id: rule.ID(), phase: rulite.ActionPhase})
		}
	}
	return append(facts, observedFact{kind: rulite.EventExecutionFinished})
}

func checkObservedEvent(t testing.TB, event rulite.Event, want observedFact, result rulite.Result) {
	t.Helper()
	if event.Kind() != want.kind {
		t.Fatalf("event=%s; want %s", event.Kind(), want.kind)
	}
	info, hasRule := event.Rule()
	phase, hasPhase := event.Phase()
	outcome, hasOutcome := event.ConditionOutcome()
	failure, hasFailure := event.Failure()
	counts, hasCounts := event.Counts()
	if hasRule != (want.id != "") || info.ID() != want.id || hasPhase != hasRule || phase != want.phase || outcome != want.outcome {
		t.Fatalf("event fields disagree with fact: %+v", want)
	}
	if hasOutcome != (want.kind == rulite.EventRuleEvaluated || want.kind == rulite.EventRuleMatched) {
		t.Fatal("invalid condition outcome presence")
	}
	if hasCounts != !hasRule {
		t.Fatal("invalid summary presence")
	}
	if hasRule {
		x, _ := result.Rule(info.ID())
		if info.Order() != x.Order() || info.RegistrationIndex() != x.RegistrationIndex() || info.Priority() != x.Priority() || info.Name() != x.Name() || !slices.Equal(info.Tags(), x.Tags()) {
			t.Fatal("event lost compiled metadata")
		}
		clear(info.Tags())
	}
	wantFailure := want.kind == rulite.EventRuleFailed || want.kind == rulite.EventRuleEvaluated && outcome == rulite.ConditionOutcomeError
	if hasFailure != wantFailure {
		t.Fatal("invalid failure presence")
	}
	if hasFailure {
		x, _ := result.Rule(want.id)
		if !reflect.DeepEqual(failure, x.Error()) {
			t.Fatal("event does not use canonical ledger Failure")
		}
	} else if !reflect.DeepEqual(failure, rulite.Failure{}) {
		t.Fatal("invalid event returned failure data")
	}
	if want.kind == rulite.EventExecutionStarted {
		if counts != (rulite.Counts{Total: result.Counts().Total, NotEvaluated: result.Counts().Total}) {
			t.Fatal("start summary changed after delivery")
		}
	} else if want.kind == rulite.EventExecutionFinished {
		if counts != result.Counts() || event.StopReason() != result.StopReason() {
			t.Fatal("finish summary differs from ledger")
		}
	} else if counts != (rulite.Counts{}) {
		t.Fatal("rule event has summary counts")
	}
	if want.kind != rulite.EventExecutionFinished && event.StopReason() != rulite.StopNone {
		t.Fatal("nonterminal event has stop reason")
	}
}

func checkBusinessParity(t testing.TB, got rulite.Result, gotErr error, want rulite.Result, wantErr error) {
	t.Helper()
	_, gotTrace := got.Trace()
	_, wantTrace := want.Trace()
	if gotTrace != wantTrace {
		t.Fatal("observation changed trace configuration")
	}
	if got.Counts() != want.Counts() || got.Executed() != want.Executed() || got.Stopped() != want.Stopped() || got.StopReason() != want.StopReason() || !slices.Equal(got.Matched(), want.Matched()) || !slices.Equal(got.Fired(), want.Fired()) || got.Explain().String() != want.Explain().String() || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Fatal("observation changed business result")
	}
	// Runtime stacks and trace durations can differ; compare their business facts.
	for i, failure := range got.Failures() {
		other := want.Failures()[i]
		if failure.RuleID() != other.RuleID() || failure.Phase() != other.Phase() || failure.Continued() != other.Continued() {
			t.Fatal("observation changed failure disposition")
		}
		var panicErr, otherPanic *rulite.PanicError
		if errors.As(failure, &panicErr) {
			if !errors.As(other, &otherPanic) || !reflect.DeepEqual(panicErr.Value(), otherPanic.Value()) || panicErr.RuleID() != otherPanic.RuleID() || panicErr.Phase() != otherPanic.Phase() {
				t.Fatal("business panic changed")
			}
		} else if failure.Cause() != other.Cause() || !errors.Is(gotErr, other.Cause()) {
			t.Fatal("business cause identity changed")
		}
	}
	var a, b *rulite.ExecutionError
	if errors.As(gotErr, &a) != errors.As(wantErr, &b) {
		t.Fatal("business error shape changed")
	}
	if a != nil {
		if len(a.Unwrap()) != len(b.Unwrap()) {
			t.Fatal("business unwrap tree changed")
		}
		for i, child := range a.Unwrap() {
			if fmt.Sprint(child) != fmt.Sprint(b.Unwrap()[i]) {
				t.Fatal("business error order changed")
			}
		}
	}
	checkResultConsistency(t, got)
}

// Each byte selects miss, success, condition error, action error, or a phase panic.
func observationRules(outcomes []byte, cause error) []rulite.Rule[executionInput] {
	rules := make([]rulite.Rule[executionInput], len(outcomes))
	for i, encoded := range outcomes {
		kind := encoded % 6
		id := rulite.RuleID(fmt.Sprintf("rule/%d", i))
		rules[i] = rulite.NewRule[executionInput](id).Name("Eligibility").Tags("audit").When(func(_ context.Context, input *executionInput) (bool, error) {
			if kind == 4 {
				panic("condition unavailable")
			}
			if kind == 2 {
				return true, cause
			}
			return kind != 0, nil
		}).Then(func(_ context.Context, input *executionInput) error {
			input.value++
			input.calls = append(input.calls, string(id))
			if kind == 5 {
				panic("action unavailable")
			}
			if kind == 3 {
				return cause
			}
			return nil
		})
	}
	return rules
}

func TestObserverEventOrder(t *testing.T) {
	cause := errors.New("provider unavailable")
	var calls []string
	var events []rulite.Event
	rule := func(id rulite.RuleID, priority rulite.Priority, matched bool, conditionErr, actionErr error) rulite.Rule[executionInput] {
		return rulite.NewRule[executionInput](id).Priority(priority).Tags("audit").When(func(_ context.Context, input *executionInput) (bool, error) {
			calls = append(calls, "condition:"+string(id))
			if id == "success" && input.value != 1 {
				t.Fatal("observation hid earlier partial mutation")
			}
			return matched, conditionErr
		}).Then(func(_ context.Context, input *executionInput) error {
			calls = append(calls, "action:"+string(id))
			input.value++
			return actionErr
		})
	}
	set := mustCompile(t, rule("success", 0, true, nil, nil), rule("miss", 100, false, nil, nil), rule("condition-error", 100, true, cause, nil), rule("action-error", 50, true, nil, cause))
	observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		events = append(events, event)
		info, _ := event.Rule()
		calls = append(calls, event.Kind().String()+":"+string(info.ID()))
		return nil
	})
	policy := rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError)
	result, err := mustReuse(t, set, rulite.WithObserver(observer), rulite.WithPolicy(policy)).Fire(context.Background(), &executionInput{})
	if !errors.Is(err, cause) || len(result.Diagnostics()) != 0 {
		t.Fatal("observer changed execution")
	}
	want := []string{"execution-started:", "condition:miss", "rule-evaluated:miss", "condition:condition-error", "rule-evaluated:condition-error", "rule-failed:condition-error", "condition:action-error", "rule-evaluated:action-error", "rule-matched:action-error", "action:action-error", "rule-failed:action-error", "condition:success", "rule-evaluated:success", "rule-matched:success", "action:success", "rule-fired:success", "execution-finished:"}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls=%v; want %v", calls, want)
	}
	facts := expectedEvents(result)
	if len(events) != len(facts) {
		t.Fatal("incomplete events")
	}
	for i, event := range events {
		checkObservedEvent(t, event, facts[i], result)
	}
}

func TestObserverFailureIsolation(t *testing.T) {
	for stop := range 3 {
		for conditionMode := range 2 {
			for actionMode := range 2 {
				policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(stop)).WithConditionErrors(rulite.ErrorMode(conditionMode)).WithActionErrors(rulite.ErrorMode(actionMode))
				for _, outcomes := range [][]byte{{}, {0, 2, 3, 1, 1}, {1, 4}, {0, 5}} {
					checkObservationStream(t, outcomes, policy, -1, false, false)
					// Test every delivery boundary, including the final event, and restart.
					baseline, _ := mustEngine(t, observationRules(outcomes, errors.New("business unavailable"))...).Fire(context.Background(), &executionInput{}, rulite.WithPolicy(policy))
					for index := range expectedEvents(baseline) {
						checkObservationStream(t, outcomes, policy, index, false, false)
						checkObservationStream(t, outcomes, policy, index, true, true)
					}
				}
			}
		}
	}
}

func checkObservationStream(t testing.TB, outcomes []byte, policy rulite.ExecutionPolicy, failAt int, panics, traced bool) {
	t.Helper()
	cause := errors.New("business unavailable")
	diagnosticCause := errors.New("export unavailable")
	set := mustCompile(t, observationRules(outcomes, cause)...)
	options := []rulite.FireOption{rulite.WithPolicy(policy)}
	if traced {
		options = append(options, rulite.WithTrace())
	}
	input := executionInput{}
	baseline, baselineErr := mustReuse(t, set, options...).Fire(context.Background(), &input)
	facts := expectedEvents(baseline)
	var events []rulite.Event
	observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		events = append(events, event)
		if len(events)-1 == failAt {
			if panics {
				panic("export unavailable")
			}
			return diagnosticCause
		}
		return nil
	})
	engine := mustReuse(t, set, append(options, rulite.WithObserver(observer))...)
	for range 2 {
		events = nil
		actualInput := executionInput{}
		result, err := engine.Fire(context.Background(), &actualInput)
		checkBusinessParity(t, result, err, baseline, baselineErr)
		if !reflect.DeepEqual(input, actualInput) {
			t.Fatal("observation changed callback order or effects")
		}
		wantCount := len(facts)
		diagnostics := result.Diagnostics()
		if failAt >= 0 {
			wantCount = failAt + 1
			if len(diagnostics) != 1 || !reflect.DeepEqual(diagnostics[0].Event(), events[failAt]) {
				t.Fatal("missing failure diagnostic")
			}
			var panicErr *rulite.ObserverPanicError
			if panics {
				if !errors.As(diagnostics[0], &panicErr) || panicErr.Value() != "export unavailable" || len(panicErr.Stack()) == 0 {
					t.Fatal("observer panic not isolated")
				}
			} else if !errors.Is(diagnostics[0], diagnosticCause) || diagnostics[0].Cause() != diagnosticCause || diagnostics[0].Unwrap() != diagnosticCause {
				t.Fatal("observer error identity lost")
			}
			if errors.Is(err, diagnosticCause) || errors.As(err, &panicErr) {
				t.Fatal("diagnostic joined business error")
			}
			clear(diagnostics)
			if result.Diagnostics()[0].Cause() == nil {
				t.Fatal("diagnostics exposed storage")
			}
		} else if len(diagnostics) != 0 {
			t.Fatal("successful observer produced diagnostic")
		}
		if len(events) != wantCount {
			t.Fatalf("observer calls=%d; want %d", len(events), wantCount)
		}
		for i, event := range events {
			checkObservedEvent(t, event, facts[i], result)
		}
	}
}

func TestObserverConfigurationAndPreflight(t *testing.T) {
	var calls int
	observer := rulite.ObserverFunc(func(context.Context, rulite.Event) error { calls++; return nil })
	set := mustCompile[executionInput](t)
	options := []rulite.FireOption{rulite.WithObserver(observer)}
	engine := mustReuse(t, set, options...)
	options[0] = rulite.WithObserver(nil)
	for _, tc := range []struct {
		options []rulite.FireOption
		want    int
	}{
		{nil, 2}, {[]rulite.FireOption{rulite.WithObserver(nil)}, 0}, {[]rulite.FireOption{rulite.WithObserver(nil), {}, rulite.WithObserver(observer)}, 2}, {[]rulite.FireOption{rulite.WithObserver(observer), rulite.WithObserver(nil)}, 0}, {[]rulite.FireOption{{}}, 2},
	} {
		calls = 0
		result, err := engine.Fire(context.Background(), &executionInput{}, tc.options...)
		if err != nil || calls != tc.want || !result.Executed() {
			t.Fatal("observer option changed or was not replaced")
		}
	}
	calls = 0
	var invalid *rulite.Engine[executionInput]
	for _, tc := range []struct {
		engine *rulite.Engine[executionInput]
		ctx    context.Context
		input  *executionInput
		option rulite.FireOption
	}{
		{engine, nil, &executionInput{}, rulite.FireOption{}}, {engine, context.Background(), nil, rulite.FireOption{}}, {invalid, context.Background(), &executionInput{}, rulite.FireOption{}}, {engine, context.Background(), &executionInput{}, rulite.WithPanicMode(255)},
	} {
		result, err := tc.engine.Fire(tc.ctx, tc.input, rulite.WithObserver(observer), tc.option)
		if err == nil || result.Executed() || len(result.Diagnostics()) != 0 || calls != 0 {
			t.Fatal("preflight emitted events")
		}
	}
	var nilFunc rulite.ObserverFunc
	if err := nilFunc.Observe(context.Background(), rulite.Event{}); err != nil {
		t.Fatal(err)
	}
	for _, observer := range []rulite.Observer{nil, nilFunc} {
		result, err := mustReuse(t, set, rulite.WithObserver(observer)).Fire(context.Background(), &executionInput{})
		if err != nil || len(result.Diagnostics()) != 0 {
			t.Fatal("nil observer was not a no-op")
		}
	}
	for _, mode := range []rulite.PanicMode{rulite.RecoverPanics, rulite.PropagatePanics} {
		result, err := engine.Fire(context.Background(), &executionInput{}, rulite.WithPanicMode(mode), rulite.WithObserver(rulite.ObserverFunc(func(context.Context, rulite.Event) error { return context.Canceled })))
		if err != nil || result.StopReason() != rulite.StopCompleted || !errors.Is(result.Diagnostics()[0], context.Canceled) {
			t.Fatal("observer context sentinel became business cancellation")
		}
	}
}

type nilReceiverObserver struct{ calls int }

func (o *nilReceiverObserver) Observe(context.Context, rulite.Event) error { o.calls++; return nil }

func TestObserverPanicValuesAndPropagation(t *testing.T) {
	var typedNil *nilReceiverObserver
	result, err := mustReuse(t, mustCompile[int](t), rulite.WithObserver(typedNil)).Fire(context.Background(), new(int))
	var recovered *rulite.ObserverPanicError
	if err != nil || !errors.As(result.Diagnostics()[0], &recovered) {
		t.Fatal("typed-nil observer panic escaped")
	}
	for _, kind := range []rulite.EventKind{rulite.EventExecutionStarted, rulite.EventRuleEvaluated, rulite.EventRuleMatched, rulite.EventRuleFired, rulite.EventRuleFailed, rulite.EventExecutionFinished} {
		payload := &unsafePanicStringer{}
		var events []rulite.EventKind
		observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
			events = append(events, event.Kind())
			if event.Kind() == kind {
				panic(payload)
			}
			return nil
		})
		set := mustCompile(t, observationRules([]byte{3, 1}, errors.New("provider unavailable"))...)
		policy := rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)
		engine := mustReuse(t, set, rulite.WithObserver(observer), rulite.WithPolicy(policy))
		result, _ := engine.Fire(context.Background(), &executionInput{})
		d := result.Diagnostics()[0]
		if !errors.As(d, &recovered) || recovered.Value() != payload || strings.Contains(d.Error(), "private") {
			t.Fatal("panic payload lost or formatted")
		}
		stack := recovered.Stack()
		clear(recovered.Stack())
		if !slices.Equal(stack, recovered.Stack()) || !strings.Contains(string(stack), "observer_test.go") {
			t.Fatal("panic stack not captured defensively")
		}
		events = nil
		func() {
			defer func() {
				if recover() != payload {
					t.Error("observer panic did not propagate unchanged")
				}
			}()
			_, _ = engine.Fire(context.Background(), &executionInput{}, rulite.WithPanicMode(rulite.PropagatePanics))
			t.Error("observer panic returned")
		}()
		if events[len(events)-1] != kind {
			t.Fatal("observation continued after propagated panic")
		}
	}
	var zero rulite.Diagnostic
	if zero.Cause() != nil || zero.Unwrap() != nil || zero.Error() == "" || zero.Event().Kind() != rulite.EventNone {
		t.Fatal("zero diagnostic unsafe")
	}
	for _, p := range []*rulite.ObserverPanicError{nil, {}} {
		if p.Value() != nil || len(p.Stack()) != 0 || p.Error() == "" {
			t.Fatal("zero panic diagnostic unsafe")
		}
	}
}

type unsafePanicStringer struct{}

func (*unsafePanicStringer) String() string { panic("private panic payload must not be formatted") }

func TestObserverNestedExecutionAndIndependentEngines(t *testing.T) {
	set := mustCompile(t, observationRules([]byte{1}, nil)...)
	var engine *rulite.Engine[executionInput]
	inside, calls := false, 0
	cause := errors.New("nested export unavailable")
	observer := rulite.ObserverFunc(func(ctx context.Context, event rulite.Event) error {
		calls++
		if inside {
			return cause
		}
		if event.Kind() == rulite.EventExecutionStarted {
			inside = true
			result, err := engine.Fire(ctx, &executionInput{})
			inside = false
			if err != nil || result.Counts().Fired != 1 || len(result.Diagnostics()) != 1 || !errors.Is(result.Diagnostics()[0], cause) {
				t.Fatal("nested execution lost its isolated diagnostic")
			}
		}
		return nil
	})
	engine = mustReuse(t, set, rulite.WithObserver(observer))
	plain := mustReuse(t, set)
	for range 2 {
		calls = 0
		result, err := engine.Fire(context.Background(), &executionInput{})
		if err != nil || result.Counts().Fired != 1 || len(result.Diagnostics()) != 0 || calls != 6 {
			t.Fatal("nested diagnostic disabled enclosing execution")
		}
		result, err = plain.Fire(context.Background(), &executionInput{})
		if err != nil || result.Counts().Fired != 1 || len(result.Diagnostics()) != 0 || calls != 6 {
			t.Fatal("observer default crossed engines")
		}
	}
}

func TestObserverTimingAndSynchronousDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		observer := rulite.ObserverFunc(func(context.Context, rulite.Event) error { time.Sleep(7 * time.Second); return nil })
		rule := rulite.NewRule[int]("timed").When(func(context.Context, *int) (bool, error) { time.Sleep(3 * time.Second); return true, nil }).Then(func(context.Context, *int) error { time.Sleep(2 * time.Second); return nil })
		engine := mustReuse(t, mustCompile(t, rule), rulite.WithObserver(observer), rulite.WithTrace())
		start := time.Now()
		result, err := engine.Fire(context.Background(), new(int))
		trace, _ := result.Trace()
		if err != nil || time.Since(start) != 40*time.Second || trace.Duration() != 40*time.Second || trace.Rules()[0].ConditionDuration() != 3*time.Second || trace.Rules()[0].ActionDuration() != 2*time.Second {
			t.Fatal("observation latency entered callback timing or escaped Fire")
		}
	})
}

func TestObserverConcurrentDiagnosticsReads(t *testing.T) {
	var mu sync.Mutex
	counts := make(map[rulite.EventKind]int)
	observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		mu.Lock()
		defer mu.Unlock()
		counts[event.Kind()]++
		if event.Kind() == rulite.EventExecutionFinished {
			panic("export unavailable")
		}
		return nil
	})
	engine := mustReuse(t, mustCompile(t, observationRules([]byte{0, 1}, nil)...), rulite.WithObserver(observer))
	retained, err := engine.Fire(context.Background(), &executionInput{})
	if err != nil {
		t.Fatal(err)
	}
	var panicErr *rulite.ObserverPanicError
	if !errors.As(retained.Diagnostics()[0], &panicErr) {
		t.Fatal("missing diagnostic")
	}
	stack := panicErr.Stack()
	for _, concurrency := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("goroutines_%d", concurrency), func(t *testing.T) {
			var workers sync.WaitGroup
			for range concurrency {
				workers.Go(func() {
					for range 6 {
						input := executionInput{}
						result, err := engine.Fire(context.Background(), &input)
						if err != nil || input.value != 1 || result.Counts().Fired != 1 || len(result.Diagnostics()) != 1 {
							t.Error("shared observer changed business result")
							return
						}
						clear(retained.Diagnostics())
						clear(panicErr.Stack())
						event := retained.Diagnostics()[0].Event()
						if !slices.Equal(stack, panicErr.Stack()) || event.StopReason() != rulite.StopCompleted {
							t.Error("concurrent diagnostic reads changed storage")
							return
						}
						checkResultConsistency(t, retained)
					}
				})
			}
			workers.Wait()
		})
	}
	want := 1 + 6*(1+2+4+8+16+32)
	if counts[rulite.EventExecutionStarted] != want || counts[rulite.EventExecutionFinished] != want || counts[rulite.EventRuleEvaluated] != want*2 {
		t.Fatal("observer missed concurrent executions")
	}
}
