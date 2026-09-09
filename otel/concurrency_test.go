package otel_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
)

func TestRuntimeSwapCapturesOneSpanSnapshot(t *testing.T) {
	x := newTelemetry(t, ruliteotel.WithTraceVersions())
	entered, release := make(chan struct{}), make(chan struct{})
	old := engine(t, "old", rulite.NewRule[state]("old").When(func(context.Context, *state) (bool, error) { close(entered); <-release; return true, nil }).Then(func(_ context.Context, s *state) error { s.value = 1; return nil }))
	next := engine(t, "new", rulite.NewRule[state]("new").When(func(context.Context, *state) (bool, error) { return true, nil }).Then(func(_ context.Context, s *state) error { s.value = 2; return nil }))
	runtime, err := rulite.NewRuntime(old)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	finished := make(chan rulite.Result, 1)
	go func() {
		input := state{}
		r, err := x.adapter.Fire(ctx, runtime, &input, rulite.WithTrace())
		if err != nil || input.value != 1 {
			t.Error("old execution changed")
		}
		finished <- r
	}()
	<-entered
	if _, err := runtime.Publish(nil); !errors.Is(err, rulite.ErrInvalidEngine) {
		t.Fatal("invalid publication accepted")
	}
	if s, err := runtime.Publish(next); err != nil || s.Revision() != 2 {
		t.Fatal("new publication failed")
	}
	input := state{}
	newResult, err := x.adapter.Fire(ctx, runtime, &input, rulite.WithTrace())
	if err != nil || input.value != 2 || newResult.Snapshot().Revision() != 2 {
		t.Fatal("new execution captured old snapshot")
	}
	close(release)
	oldResult := <-finished
	if oldResult.Snapshot().Revision() != 1 || oldResult.Counts().Fired != 1 {
		t.Fatal("old identity changed")
	}
	spans := x.exporter.GetSpans()
	if len(spans) != 2 || spans[0].SpanContext.SpanID() == spans[1].SpanContext.SpanID() {
		t.Fatal("executions share span")
	}
	for i, s := range spans {
		version, revision := "new", "2"
		if i == 1 {
			version, revision = "old", "1"
		}
		if attr(s.Attributes, "rulite.snapshot.revision").AsString() != revision || attr(s.Attributes, "rulite.ruleset.version").AsString() != version || len(s.Events) != 3 {
			t.Fatal("span identity crossed publication")
		}
		for _, e := range s.Events {
			if attr(e.Attributes, "rulite.rule.id").AsString() != version {
				t.Fatal("mixed rule versions in execution span")
			}
		}
	}
}

func TestSharedAdapterConcurrentRuntimeAndResultReads(t *testing.T) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			x := newTelemetry(t, ruliteotel.WithTraceVersions(), ruliteotel.WithMetricVersions("a", "b"), ruliteotel.WithRuleMetrics("a/0", "b/0"))
			build := func(version string, value int) *rulite.Engine[state] {
				var rs []rulite.Rule[state]
				for i := range 2 {
					rs = append(rs, rulite.NewRule[state](rulite.RuleID(fmt.Sprintf("%s/%d", version, i))).When(func(_ context.Context, s *state) (bool, error) {
						if i == 1 && s.value != value {
							t.Error("condition did not see captured prior action")
						}
						return true, nil
					}).Then(func(_ context.Context, s *state) error { s.value += value; return nil }))
				}
				return engine(t, rulite.RuleSetVersion(version), rs...)
			}
			engines := []*rulite.Engine[state]{build("a", 1), build("b", 2)}
			runtime, err := rulite.NewRuntime(engines[0])
			if err != nil {
				t.Fatal(err)
			}
			var publications sync.Map
			publications.Store(rulite.SnapshotRevision(1), rulite.RuleSetVersion("a"))
			start := make(chan struct{})
			ctx := context.Background()
			var wg sync.WaitGroup
			for publisher := range 2 {
				wg.Go(func() {
					<-start
					for i := range 24 {
						if _, err := runtime.Publish(nil); !errors.Is(err, rulite.ErrInvalidEngine) {
							t.Error("invalid publication accepted")
						}
						s, err := runtime.Publish(engines[(publisher+i)%2])
						if err != nil {
							t.Error(err)
							return
						}
						if _, loaded := publications.LoadOrStore(s.Revision(), s.Version()); loaded {
							t.Error("duplicate publication revision")
						}
					}
				})
			}
			for range workers {
				wg.Go(func() {
					<-start
					for range 16 {
						input := state{}
						r, err := x.adapter.Fire(ctx, runtime, &input, rulite.WithTrace())
						want := 2
						if r.Snapshot().Version() == "b" {
							want = 4
						}
						if err != nil || input.value != want || r.Counts().Fired != 2 || len(r.Diagnostics()) != 0 {
							t.Error("concurrent execution mixed facts")
						}
						read := func() {
							tr, _ := r.Trace()
							if tr.Snapshot() != r.Snapshot() || r.Explain().Snapshot() != r.Snapshot() || len(tr.Rules()) != 2 {
								t.Error("result identity changed during reads")
							}
							clear(r.Fired())
							clear(r.Matched())
							clear(tr.Rules())
						}
						var readers sync.WaitGroup
						readers.Go(read)
						read()
						readers.Wait()
					}
				})
			}
			close(start)
			wg.Wait()
			spans := x.exporter.GetSpans()
			if len(spans) != workers*16 || runtime.Snapshot().Revision() != 49 {
				t.Fatal("lost executions or publications")
			}
			seen := make(map[string]bool)
			for _, s := range spans {
				id := s.SpanContext.SpanID().String()
				if seen[id] {
					t.Fatal("span crossed executions")
				}
				seen[id] = true
				revision, err := strconv.ParseUint(attr(s.Attributes, "rulite.snapshot.revision").AsString(), 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				version, ok := publications.Load(rulite.SnapshotRevision(revision))
				if !ok || string(version.(rulite.RuleSetVersion)) != attr(s.Attributes, "rulite.ruleset.version").AsString() {
					t.Fatal("execution not traceable to successful publication")
				}
				if len(s.Events) != 6 {
					t.Fatal("span lost or gained another execution's events")
				}
				for i, e := range s.Events {
					if attr(e.Attributes, "rulite.rule.id").AsString() != fmt.Sprintf("%s/%d", version, i/3) {
						t.Fatal("mixed snapshot event stream")
					}
				}
			}
			if sum(t, x.collect(t), "rulite.execution.count", "{execution}") != int64(workers*16) {
				t.Fatal("concurrent metrics lost updates")
			}
		})
	}
}

func TestNestedCallsAndCallerSynchronizedInput(t *testing.T) {
	x := newTelemetry(t, ruliteotel.WithTraceVersions())
	ctx := context.Background()
	inner := engine(t, "inner", rules([]byte{1}, nil)...)
	outer := engine(t, "outer", rulite.NewRule[state]("outer").When(func(seen context.Context, _ *state) (bool, error) {
		if seen != ctx {
			t.Error("nested callback context changed")
		}
		_, err := x.adapter.Fire(seen, inner, &state{})
		return true, err
	}).Then(func(_ context.Context, s *state) error { s.value++; return nil }))
	var mu sync.Mutex
	var wg sync.WaitGroup
	input := state{}
	for range 8 {
		wg.Go(func() {
			for range 8 {
				mu.Lock()
				r, err := x.adapter.Fire(ctx, outer, &input)
				if err != nil || r.Counts().Fired != 1 {
					t.Error("synchronized execution changed")
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if input.value != 64 {
		t.Fatal("shared input mutation lost")
	}
	spans := x.exporter.GetSpans()
	if len(spans) != 128 {
		t.Fatal("nested execution lifecycle missing")
	}
	for _, s := range spans {
		if len(s.Events) != 3 {
			t.Fatal("nested event stream crossed calls")
		}
		version := attr(s.Attributes, "rulite.ruleset.version").AsString()
		id := attr(s.Events[0].Attributes, "rulite.rule.id").AsString()
		if version == "inner" && id != "rule/0" || version == "outer" && id != "outer" {
			t.Fatal("nested version association changed")
		}
		if s.Parent.IsValid() {
			t.Fatal("adapter silently changed callback parenting")
		}
	}
}
