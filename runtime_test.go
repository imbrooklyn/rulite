package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite"
)

type runtimeInput struct {
	Generation int
	Stage      int
	Calls      int
	Block      bool
	AppliedBy  rulite.RuleID
}

func runtimeEngine(t testing.TB, generation int, gate func(), options ...rulite.FireOption) *rulite.Engine[runtimeInput] {
	t.Helper()
	first := rulite.NewRule[runtimeInput]("pricing/offer").Priority(10).
		When(func(_ context.Context, p *runtimeInput) (bool, error) {
			if p.Block && gate != nil {
				gate()
			}
			return p.Stage == 0, nil
		}).Then(func(_ context.Context, p *runtimeInput) error {
		p.Generation, p.Stage, p.Calls, p.AppliedBy = generation, 1, p.Calls+1, "pricing/offer"
		return nil
	})
	last := rulite.NewRule[runtimeInput]("pricing/audit").When(func(_ context.Context, p *runtimeInput) (bool, error) {
		return p.Stage == 1 && p.Generation == generation, nil
	}).Then(func(_ context.Context, p *runtimeInput) error { p.Stage, p.Calls = 2, p.Calls+1; return nil })
	set, err := rulite.Compile(last, first)
	if err != nil {
		t.Fatal(err)
	}
	set, err = set.WithIdentity(rulite.RuleSetVersion(fmt.Sprintf("release/%d", generation)), rulite.SourceDigest(fmt.Sprintf("source/%d", generation)))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set, options...)
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func checkRuntimeResult(t testing.TB, result rulite.Result, input runtimeInput, identity rulite.SnapshotInfo) {
	t.Helper()
	if result.Snapshot() != identity || result.Explain().Snapshot() != identity || result.StopReason() != rulite.StopCompleted || result.Counts() != (rulite.Counts{Total: 2, Evaluated: 2, Matched: 2, Fired: 2}) || input.Stage != 2 || input.Calls != 2 || input.AppliedBy != "pricing/offer" || identity.Version() != rulite.RuleSetVersion(fmt.Sprintf("release/%d", input.Generation)) || identity.SourceDigest() != rulite.SourceDigest(fmt.Sprintf("source/%d", input.Generation)) {
		t.Errorf("incomplete snapshot execution: input=%+v identity=%+v counts=%+v", input, identity, result.Counts())
	}
	if trace, ok := result.Trace(); ok && (trace.Snapshot() != identity || len(trace.Rules()) != 2) {
		t.Error("trace identity or rules changed")
	}
}

func TestRuntimeCapturesOnePublication(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	oldEngine := runtimeEngine(t, 1, func() { close(entered); <-release })
	runtime, err := rulite.NewRuntime(oldEngine)
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity := runtime.Snapshot()
	type completed struct {
		result rulite.Result
		input  runtimeInput
		err    error
		events []rulite.Event
	}
	done := make(chan completed)
	go func() {
		c := completed{input: runtimeInput{Block: true}}
		c.result, c.err = runtime.Fire(context.Background(), &c.input, rulite.WithTrace(), rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
			c.events = append(c.events, event)
			return nil
		})))
		done <- c
	}()
	<-entered
	newIdentity, err := runtime.Publish(runtimeEngine(t, 2, nil))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	input := runtimeInput{}
	result, err := runtime.Fire(context.Background(), &input, rulite.WithTrace())
	close(release)
	old := <-done
	if err != nil || old.err != nil || oldIdentity.Revision() != 1 || newIdentity.Revision() != 2 {
		t.Fatal("publication or execution failed", err, old.err)
	}
	checkRuntimeResult(t, result, input, newIdentity)
	checkRuntimeResult(t, old.result, old.input, oldIdentity)
	if input.Generation != 2 || old.input.Generation != 1 || len(old.events) != 8 {
		t.Fatal("old and new callbacks mixed")
	}
	for _, event := range old.events {
		if event.Snapshot() != oldIdentity {
			t.Fatal("event changed publication")
		}
	}
	// Publishing an engine never changes its direct execution identity.
	input = runtimeInput{}
	result, err = oldEngine.Fire(context.Background(), &input, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
		if event.Snapshot() != oldEngine.Snapshot() {
			t.Error("direct event acquired a Runtime revision")
		}
		return nil
	})))
	if err != nil || result.Snapshot().Revision() != 0 {
		t.Fatal("direct engine acquired a publication revision")
	}
	checkRuntimeResult(t, result, input, oldEngine.Snapshot())
}

func TestRuntimeZeroInvalidAndRepeatedPublication(t *testing.T) {
	var zero rulite.Runtime[int]
	var absent *rulite.Runtime[int]
	var emptyEngine rulite.Engine[int]
	invalidOption := rulite.WithPolicy(rulite.DefaultPolicy().WithStop(255))
	for _, runtime := range []*rulite.Runtime[int]{nil, &zero} {
		for _, tc := range []struct {
			ctx   context.Context
			input *int
			want  error
		}{{nil, nil, rulite.ErrNilContext}, {context.Background(), nil, rulite.ErrNilInput}, {context.Background(), new(int), rulite.ErrInvalidRuntime}} {
			result, err := runtime.Fire(tc.ctx, tc.input, invalidOption)
			if err != tc.want || result.Executed() || result.Snapshot() != (rulite.SnapshotInfo{}) || runtime.Snapshot() != (rulite.SnapshotInfo{}) {
				t.Fatal("invalid runtime preflight changed", err)
			}
		}
	}
	if info, err := absent.Publish(nil); err != rulite.ErrInvalidRuntime || info != (rulite.SnapshotInfo{}) {
		t.Fatal("nil receiver accepted")
	}
	for _, invalid := range []*rulite.Engine[int]{nil, &emptyEngine} {
		if runtime, err := rulite.NewRuntime(invalid); runtime != nil || err != rulite.ErrInvalidEngine {
			t.Fatal("invalid initial engine accepted")
		}
	}
	set, err := rulite.Compile[int]()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	for revision := rulite.SnapshotRevision(1); revision <= 3; revision++ {
		info, err := zero.Publish(engine)
		if err != nil || info.Revision() != revision || zero.Snapshot() != info {
			t.Fatal("repeat publication did not increment", err)
		}
		for _, invalid := range []*rulite.Engine[int]{nil, &emptyEngine} {
			if failed, err := zero.Publish(invalid); err != rulite.ErrInvalidEngine || failed != (rulite.SnapshotInfo{}) || zero.Snapshot() != info {
				t.Fatal("failed publication changed current")
			}
		}
		result, err := zero.Fire(context.Background(), new(int), rulite.WithTrace())
		trace, ok := result.Trace()
		if err != nil || !result.Executed() || result.Counts() != (rulite.Counts{}) || result.StopReason() != rulite.StopCompleted || result.Snapshot() != info || !ok || trace.Snapshot() != info {
			t.Fatal("valid empty snapshot rejected")
		}
		result, err = zero.Fire(context.Background(), new(int), invalidOption)
		if err != rulite.ErrInvalidPolicy || result.Executed() || result.Snapshot() != (rulite.SnapshotInfo{}) {
			t.Fatal("option preflight changed")
		}
	}
	if (rulite.Trace{}).Snapshot() != (rulite.SnapshotInfo{}) || (rulite.Event{}).Snapshot() != (rulite.SnapshotInfo{}) || (rulite.Explanation{}).Snapshot() != (rulite.SnapshotInfo{}) {
		t.Fatal("zero view has identity")
	}
}

func TestSnapshotIdentityIsIndependent(t *testing.T) {
	for _, invalid := range []*rulite.RuleSet[int]{nil, {}} {
		if set, err := invalid.WithIdentity("release", "digest"); err != rulite.ErrInvalidRuleSet || set != nil || invalid.Snapshot() != (rulite.SnapshotInfo{}) {
			t.Fatal("invalid set accepted")
		}
	}
	set, err := rulite.Compile[int]()
	if err != nil {
		t.Fatal(err)
	}
	a, err := set.WithIdentity(" Release/1 ", "Opaque/ABC")
	if err != nil {
		t.Fatal(err)
	}
	b, err := a.WithIdentity("", "")
	if err != nil {
		t.Fatal(err)
	}
	if set.Snapshot() != (rulite.SnapshotInfo{}) || b.Snapshot() != set.Snapshot() || a.Snapshot().Version() != " Release/1 " || a.Snapshot().SourceDigest() != "Opaque/ABC" || a.Snapshot().Revision() != 0 {
		t.Fatal("identity normalized or mutated another set")
	}
	if (*rulite.Engine[int])(nil).Snapshot() != (rulite.SnapshotInfo{}) {
		t.Fatal("nil engine identity")
	}
}

func TestRuntimePublicationFromObserverAndTerminalIdentity(t *testing.T) {
	businessErr, cause := errors.New("business failure"), errors.New("request canceled")
	for _, scenario := range []string{"completed", "condition_error", "action_error", "condition_panic", "action_panic", "canceled", "deadline", "skipped", "cancel_error", "cancel_panic", "first_match", "first_fire", "observer_error", "observer_panic", "propagate"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			var runtime rulite.Runtime[int]
			var events []rulite.Event
			next, err := rulite.NewEngine[int]()
			if err != nil {
				t.Fatal(err)
			}
			observer := rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
				events = append(events, event)
				if event.Kind() == rulite.EventExecutionStarted {
					if _, err := runtime.Publish(next); err != nil {
						t.Error(err)
					}
					// No publisher lock is held while callbacks run; nested Fire captures the new snapshot.
					inner, err := runtime.Fire(context.Background(), new(int))
					if err != nil || inner.Snapshot().Revision() != 2 || inner.Counts().Total != 0 {
						t.Error("nested Fire missed publication")
					}
				}
				if scenario == "observer_error" {
					return businessErr
				}
				if scenario == "skipped" && event.Kind() == rulite.EventRuleMatched {
					cancel(cause)
				}
				if scenario == "observer_panic" {
					panic(businessErr)
				}
				return nil
			})
			rule := rulite.NewRule[int]("terminal").When(func(context.Context, *int) (bool, error) {
				if scenario == "condition_error" {
					return true, businessErr
				}
				if scenario == "condition_panic" || scenario == "propagate" {
					panic(businessErr)
				}
				return true, nil
			}).Then(func(_ context.Context, input *int) error {
				*input++
				if scenario == "cancel_error" || scenario == "cancel_panic" {
					cancel(cause)
				}
				if scenario == "action_error" || scenario == "cancel_error" {
					return businessErr
				}
				if scenario == "action_panic" || scenario == "cancel_panic" {
					panic(businessErr)
				}
				return nil
			})
			set, err := rulite.CompileEntries(rulite.FirstMatchGroup("selection", rule).Entry())
			if err != nil {
				t.Fatal(err)
			}
			set, err = set.WithIdentity("old", "old-digest")
			if err != nil {
				t.Fatal(err)
			}
			policy := rulite.DefaultPolicy()
			if scenario == "first_match" {
				policy = policy.WithStop(rulite.StopOnFirstMatch)
			}
			if scenario == "first_fire" {
				policy = policy.WithStop(rulite.StopOnFirstFire)
			}
			engine, err := rulite.NewEngineFromRuleSet(set, rulite.WithObserver(observer), rulite.WithTrace(), rulite.WithPolicy(policy))
			if err != nil {
				t.Fatal(err)
			}
			identity, err := runtime.Publish(engine)
			if err != nil {
				t.Fatal(err)
			}
			want := rulite.StopCompleted
			switch scenario {
			case "condition_error":
				want = rulite.StopConditionError
			case "action_error":
				want = rulite.StopActionError
			case "condition_panic", "action_panic", "cancel_panic":
				want = rulite.StopPanic
			case "cancel_error", "canceled", "skipped":
				want = rulite.StopContextCanceled
			case "deadline":
				want = rulite.StopContextDeadlineExceeded
			case "first_match":
				want = rulite.StopFirstMatch
			case "first_fire":
				want = rulite.StopFirstFire
			}
			if scenario == "canceled" {
				cancel(cause)
			}
			var executionCtx context.Context = ctx
			if scenario == "deadline" {
				deadlineCtx, deadlineCancel := context.WithDeadline(ctx, time.Time{})
				defer deadlineCancel()
				executionCtx = deadlineCtx
			}
			if scenario == "propagate" {
				func() {
					defer func() {
						if recover() != businessErr {
							t.Error("panic was not propagated")
						}
					}()
					_, _ = runtime.Fire(executionCtx, new(int), rulite.WithPanicMode(rulite.PropagatePanics))
				}()
			} else {
				input := 0
				result, err := runtime.Fire(executionCtx, &input)
				trace, ok := result.Trace()
				if result.StopReason() != want || result.Snapshot() != identity || !ok || trace.Snapshot() != identity || result.Explain().Snapshot() != identity {
					t.Fatal("terminal snapshot changed", result.StopReason(), want)
				}
				if want == rulite.StopContextCanceled || scenario == "cancel_panic" {
					if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
						t.Fatal("cancellation cause lost")
					}
				}
				if scenario == "condition_error" || scenario == "action_error" || scenario == "cancel_error" {
					var executionErr *rulite.ExecutionError
					if !errors.Is(err, businessErr) || !errors.As(err, &executionErr) || len(result.Failures()) != 1 {
						t.Fatal("canonical failure changed")
					}
				}
				if want == rulite.StopPanic {
					var panicErr *rulite.PanicError
					if !errors.As(err, &panicErr) || panicErr.Value() != businessErr {
						t.Fatal("panic value lost")
					}
				}
				if scenario == "skipped" && (result.Counts().Skipped != 1 || input != 0) {
					t.Fatal("context-skipped action started")
				}
				if scenario == "observer_error" || scenario == "observer_panic" {
					if err != nil || len(result.Diagnostics()) != 1 || result.Diagnostics()[0].Event().Snapshot() != identity {
						t.Fatal("diagnostic identity changed")
					}
				}
			}
			for _, event := range events {
				if event.Snapshot() != identity {
					t.Fatal("event used a newer revision")
				}
			}
			if len(events) == 0 || runtime.Snapshot().Revision() != 2 {
				t.Fatal("observer did not publish")
			}
		})
	}
}

func TestRuntimeConcurrentPublicationProperty(t *testing.T) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			engines := make([]*rulite.Engine[runtimeInput], 4)
			for i := range engines {
				engines[i] = runtimeEngine(t, i, nil, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
					if event.Snapshot().Version() != rulite.RuleSetVersion(fmt.Sprintf("release/%d", i)) || event.Snapshot().Revision() == 0 {
						t.Error("default observer mixed publications")
					}
					return nil
				})))
			}
			runtime, err := rulite.NewRuntime(engines[0])
			if err != nil {
				t.Fatal(err)
			}
			sequence := map[rulite.SnapshotRevision]rulite.SnapshotInfo{1: runtime.Snapshot()}
			var mu sync.Mutex
			var results []rulite.Result
			start := make(chan struct{})
			var wg sync.WaitGroup
			for publisher := range 3 {
				wg.Go(func() {
					<-start
					for i := range 64 {
						if _, err := rulite.Compile(rulite.Rule[runtimeInput]{}); err == nil {
							t.Error("invalid compile accepted")
						}
						if _, err := runtime.Publish(nil); err != rulite.ErrInvalidEngine {
							t.Error("invalid publish accepted")
						}
						info, err := runtime.Publish(engines[(i+publisher)%len(engines)])
						if err != nil {
							t.Error(err)
							return
						}
						mu.Lock()
						if _, exists := sequence[info.Revision()]; exists {
							t.Error("duplicate revision")
						}
						sequence[info.Revision()] = info
						mu.Unlock()
					}
				})
			}
			for range workers {
				wg.Go(func() {
					<-start
					var last rulite.SnapshotRevision
					for i := range 80 {
						current := runtime.Snapshot()
						if current.Revision() < last {
							t.Error("visible revision went backward")
						}
						last = current.Revision()
						input := runtimeInput{}
						var events []rulite.Event
						options := []rulite.FireOption{rulite.WithTrace()}
						if i%2 == 0 {
							options = append(options, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error { events = append(events, event); return nil })))
						}
						result, err := runtime.Fire(context.Background(), &input, options...)
						if err != nil {
							t.Error(err)
							return
						}
						checkRuntimeResult(t, result, input, result.Snapshot())
						for _, event := range events {
							if event.Snapshot() != result.Snapshot() {
								t.Error("event identity differs from result")
							}
						}
						mu.Lock()
						results = append(results, result)
						mu.Unlock()
					}
				})
			}
			close(start)
			wg.Wait()
			if len(sequence) != 193 || runtime.Snapshot().Revision() != 193 {
				t.Fatal("failed publications consumed revisions")
			}
			for revision := rulite.SnapshotRevision(1); revision <= 193; revision++ {
				if sequence[revision].Revision() != revision {
					t.Fatal("publication sequence has a gap")
				}
			}
			for range 4 {
				wg.Go(func() {
					for _, result := range results {
						if sequence[result.Snapshot().Revision()] != result.Snapshot() {
							t.Error("execution has no successful publication")
						}
						trace, _ := result.Trace()
						if trace.Snapshot() != result.Snapshot() || !reflect.DeepEqual(result.Fired(), []rulite.RuleID{"pricing/offer", "pricing/audit"}) {
							t.Error("retained result changed")
						}
						_ = result.Explain().Entries()
						_ = trace.Rules()
					}
				})
			}
			wg.Wait()
		})
	}
}

func TestRuntimeCallerSynchronizesSharedInput(t *testing.T) {
	rule := rulite.NewRule[int]("count").When(func(context.Context, *int) (bool, error) { return true, nil }).Then(func(_ context.Context, p *int) error { *p++; return nil })
	engine, err := rulite.NewEngine(rule)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := rulite.NewRuntime(engine)
	if err != nil {
		t.Fatal(err)
	}
	var input int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 30 {
				mu.Lock()
				_, err := runtime.Fire(context.Background(), &input)
				mu.Unlock()
				if err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if input != 240 {
		t.Fatal("synchronized effects lost")
	}
}
