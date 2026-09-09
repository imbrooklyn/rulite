package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

type privateFailure struct{ token byte }

func (*privateFailure) Error() string { panic("private failure must not be formatted") }

var errTelemetry = &privateFailure{}

type observation struct {
	adapter  *ruliteotel.Adapter
	reader   *sdkmetric.ManualReader
	exporter *tracetest.InMemoryExporter
}

func telemetry(t testing.TB, mode string) observation {
	t.Helper()
	x := observation{reader: sdkmetric.NewManualReader(), exporter: tracetest.NewInMemoryExporter()}
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithReader(x.reader))
	traces := sdktrace.NewTracerProvider(sdktrace.WithSyncer(x.exporter))
	t.Cleanup(func() {
		if err := traces.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := metrics.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	var provider trace.TracerProvider = traces
	limit := 128
	if mode == "otel_limit" {
		limit = 2
	}
	if mode == "otel_fault" {
		provider = faultyProvider{TracerProvider: traces}
	}
	var err error
	x.adapter, err = ruliteotel.New(provider, metrics, ruliteotel.WithTraceVersions(), ruliteotel.WithMetricVersions("payments/v1"), ruliteotel.WithEventLimit(limit))
	if err != nil {
		t.Fatal(err)
	}
	return x
}

type faultyProvider struct{ trace.TracerProvider }

func (p faultyProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return faultyTracer{Tracer: p.TracerProvider.Tracer(name, opts...)}
}

type faultyTracer struct{ trace.Tracer }

func (t faultyTracer) Start(ctx context.Context, name string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	ctx, span := t.Tracer.Start(ctx, name, opts...)
	return ctx, faultySpan{Span: span}
}

type faultySpan struct{ trace.Span }

func (faultySpan) AddEvent(string, ...trace.EventOption) { panic(errTelemetry) }

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	source, err := sources.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func load(t testing.TB, attempt func(context.Context, *payment, string) error) *loader {
	t.Helper()
	l, err := newLoader(attempt)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func initialRuntime(t testing.TB, l *loader) *rulite.Runtime[payment] {
	t.Helper()
	e, err := l.compile(fixture(t, "v1"), "payments/v1")
	if err != nil {
		t.Fatal(err)
	}
	r, err := rulite.NewRuntime(e)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type execution struct {
	result rulite.Result
	err    error
	input  payment
	events []rulite.Event
}

func execute(ctx context.Context, runtime *rulite.Runtime[payment], x observation, mode string, input payment, options ...rulite.FireOption) execution {
	out := execution{input: input}
	opts := append([]rulite.FireOption{rulite.WithTrace()}, options...)
	if strings.HasPrefix(mode, "otel") {
		out.result, out.err = x.adapter.Fire(ctx, runtime, &out.input, opts...)
	} else {
		if mode != "plain" {
			opts = append(opts, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, e rulite.Event) error {
				out.events = append(out.events, e)
				if mode == "observer_error" {
					return errTelemetry
				}
				if mode == "observer_panic" {
					panic(errTelemetry)
				}
				return nil
			})))
		}
		out.result, out.err = runtime.Fire(ctx, &out.input, opts...)
	}
	return out
}

func checkViews(t testing.TB, out execution) {
	t.Helper()
	r := out.result
	tr, ok := r.Trace()
	if !ok || tr.Snapshot() != r.Snapshot() || r.Explain().Snapshot() != r.Snapshot() || len(tr.Rules()) != r.Counts().Total {
		t.Fatal("retained views lost captured identity")
	}
	for _, e := range out.events {
		if e.Snapshot() != r.Snapshot() {
			t.Fatal("event identity changed")
		}
		if c, ok := e.Counts(); ok && e.Kind() == rulite.EventExecutionFinished && c != r.Counts() {
			t.Fatal("event ledger diverged")
		}
		if g, ok := e.Group(); ok && e.Kind() == rulite.EventGroupFinished {
			final, _ := r.Group(g.ID())
			if g != final {
				t.Fatal("group event diverged")
			}
		}
	}
	for _, d := range r.Diagnostics() {
		if d.Event().Snapshot() != r.Snapshot() {
			t.Fatal("diagnostic identity changed")
		}
	}
	for _, rule := range tr.Rules() {
		view, ok := r.Rule(rule.ID())
		if !ok || view.State() != rule.State() || view.NotEvaluatedReason() != rule.NotEvaluatedReason() {
			t.Fatal("trace ledger diverged")
		}
	}
	// Accessor copies must not modify retained facts during concurrent reads.
	clear(tr.Rules())
	clear(r.Fired())
	clear(r.Failures())
	clear(r.Diagnostics())
}

func attr(attrs []attribute.KeyValue, name string) attribute.Value {
	for _, a := range attrs {
		if string(a.Key) == name {
			return a.Value
		}
	}
	return attribute.Value{}
}

func ruleEvents(r rulite.Result) []string {
	var events []string
	for _, rule := range r.Explain().Rules() {
		if !rule.Evaluated() {
			continue
		}
		events = append(events, string(rule.ID())+":rule-evaluated")
		if rule.Matched() {
			events = append(events, string(rule.ID())+":rule-matched")
		}
		if rule.Fired() {
			events = append(events, string(rule.ID())+":rule-fired")
		}
		if rule.State() == rulite.RuleFailed {
			events = append(events, string(rule.ID())+":rule-failed")
		}
	}
	return events
}

func checkSpan(t testing.TB, s tracetest.SpanStub, result rulite.Result, limit int) {
	t.Helper()
	if attr(s.Attributes, "rulite.snapshot.revision").AsString() != strconv.FormatUint(uint64(result.Snapshot().Revision()), 10) || attr(s.Attributes, "rulite.ruleset.version").AsString() != string(result.Snapshot().Version()) || attr(s.Attributes, "rulite.stop.reason").AsString() != result.StopReason().String() || attr(s.Attributes, "rulite.rules.failed").AsInt64() != int64(result.Counts().Failed) {
		t.Fatal("span mixed identity or terminal facts")
	}
	want := ruleEvents(result)
	if len(s.Events) != min(limit, len(want)) || attr(s.Attributes, "rulite.events.dropped").AsInt64() != int64(max(0, len(want)-limit)) {
		t.Fatal("export limit accounting changed")
	}
	for i, e := range s.Events {
		if got := attr(e.Attributes, "rulite.rule.id").AsString() + ":" + e.Name; got != want[i] {
			t.Fatalf("ordered rule projection: %s != %s", got, want[i])
		}
	}
	if strings.Contains(fmt.Sprint(s), "private failure") || strings.Contains(fmt.Sprint(s), "sha256:") {
		t.Fatal("private data exported")
	}
}

func checkMetrics(t testing.TB, x observation, results []rulite.Result) {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := x.reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"rulite.execution.count": int64(len(results))}
	for _, r := range results {
		for name, n := range map[string]int{"evaluated": r.Counts().Evaluated, "matched": r.Counts().Matched, "fired": r.Counts().Fired, "failed": r.Counts().Failed} {
			want["rulite.rule."+name] += int64(n)
		}
	}
	var durations uint64
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch values := m.Data.(type) {
			case metricdata.Sum[int64]:
				if !values.IsMonotonic || values.Temporality != metricdata.CumulativeTemporality || m.Unit != map[bool]string{true: "{execution}", false: "{rule}"}[m.Name == "rulite.execution.count"] {
					t.Fatal("counter contract changed")
				}
				var total int64
				for _, p := range values.DataPoints {
					version := attr(p.Attributes.ToSlice(), "rulite.ruleset.version").AsString()
					if p.Attributes.Len() != 2 || version != "payments/v1" && version != "other" {
						t.Fatal("unbounded metric attributes")
					}
					total += p.Value
				}
				if total != want[m.Name] {
					t.Fatalf("metric %s=%d want %d", m.Name, total, want[m.Name])
				}
				delete(want, m.Name)
			case metricdata.Histogram[float64]:
				if m.Unit != "s" {
					t.Fatal("duration unit changed")
				}
				for _, p := range values.DataPoints {
					durations += p.Count
				}
			}
		}
	}
	for _, n := range want {
		if n != 0 {
			t.Fatal("missing metric measurements")
		}
	}
	if durations != uint64(len(results)) {
		t.Fatal("missing execution durations")
	}
}

func TestReloadOldActionOutcomes(t *testing.T) {
	for _, mode := range []string{"plain", "observer", "observer_error", "observer_panic", "otel", "otel_limit", "otel_fault"} {
		for _, outcome := range []string{"success", "error", "panic", "cancel", "error_cancel"} {
			t.Run(mode+"/"+outcome, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				defer func() {
					select {
					case <-release:
					default:
						close(release)
					}
				}()
				cause := &privateFailure{}
				l := load(t, func(ctx context.Context, p *payment, provider string) error {
					p.Attempts = append(p.Attempts, provider)
					if provider == "primary/v1" {
						close(entered)
						<-release
						if outcome == "error" || outcome == "error_cancel" || outcome == "panic" {
							p.Provider, p.AppliedBy = "partial/v1", "payment/primary"
							if outcome == "panic" {
								panic(cause)
							}
							return cause
						}
					}
					return nil
				})
				runtime := initialRuntime(t, l)
				x := observation{}
				if strings.HasPrefix(mode, "otel") {
					x = telemetry(t, mode)
				}
				ctx, cancel := context.WithCancelCause(context.Background())
				defer cancel(nil)
				done := make(chan execution, 1)
				go func() {
					done <- execute(ctx, runtime, x, mode, payment{AmountMinor: 100}, rulite.WithPolicy(rulite.DefaultPolicy()))
				}()
				<-entered
				oldIdentity := runtime.Snapshot()
				if _, err := l.reload(runtime, fixture(t, "v2"), "payments/v2"); err != nil {
					t.Fatal(err)
				}
				next := execute(context.Background(), runtime, x, mode, payment{AmountMinor: 100})
				if next.err != nil || next.input.Provider != "primary/v2" || next.input.AuditVersion != "payments/v2" || next.result.Snapshot().Revision() != 2 {
					t.Fatal("new execution used old callbacks")
				}
				if outcome == "cancel" || outcome == "error_cancel" {
					cancel(cause)
				}
				close(release)
				old := <-done
				if old.result.Snapshot() != oldIdentity || old.input.Attempts[0] != "primary/v1" || len(old.input.Attempts) != 1 {
					t.Fatal("old execution mixed publication")
				}
				wantStop, wantFired, wantFailed := rulite.StopCompleted, 2, 0
				switch outcome {
				case "error":
					wantStop, wantFired, wantFailed = rulite.StopActionError, 0, 1
				case "panic":
					wantStop, wantFired, wantFailed = rulite.StopPanic, 0, 1
				case "cancel":
					wantStop, wantFired = rulite.StopContextCanceled, 1
				case "error_cancel":
					wantStop, wantFired, wantFailed = rulite.StopContextCanceled, 0, 1
				}
				if old.result.StopReason() != wantStop || old.result.Counts().Fired != wantFired || old.result.Counts().Failed != wantFailed {
					t.Fatal("partial outcome changed")
				}
				if outcome == "success" {
					if old.err != nil || old.input.AuditVersion != "payments/v1" {
						t.Fatal("old audit changed")
					}
				} else {
					var executionError *rulite.ExecutionError
					if !errors.As(old.err, &executionError) || old.input.Audit != "" {
						t.Fatal("partial execution or audit changed")
					}
					if outcome == "panic" {
						var panicError *rulite.PanicError
						if !errors.As(old.err, &panicError) || panicError.Value() != cause {
							t.Fatal("business panic lost")
						}
					} else if !errors.Is(old.err, cause) {
						t.Fatal("business/context cause lost")
					}
					if strings.Contains(outcome, "cancel") && !errors.Is(old.err, context.Canceled) {
						t.Fatal("context identity lost")
					}
				}
				if wantFailed == 1 && (old.input.Provider != "partial/v1" || old.input.AppliedBy != "payment/primary") {
					t.Fatal("partial effects rolled back")
				}
				for _, out := range []execution{old, next} {
					checkViews(t, out)
					wantDiagnostics := 0
					if mode == "observer_error" || mode == "observer_panic" || mode == "otel_limit" || mode == "otel_fault" {
						wantDiagnostics = 1
					}
					if len(out.result.Diagnostics()) != wantDiagnostics || errors.Is(out.err, errTelemetry) || errors.Is(out.err, ruliteotel.ErrEventLimit) {
						t.Fatal("telemetry contaminated business failures")
					}
					if wantDiagnostics == 1 {
						d := out.result.Diagnostics()[0]
						var recovered *rulite.ObserverPanicError
						if mode == "otel_limit" && !errors.Is(d, ruliteotel.ErrEventLimit) || mode == "observer_error" && !errors.Is(d, errTelemetry) || (mode == "observer_panic" || mode == "otel_fault") && (!errors.As(d, &recovered) || recovered.Value() != errTelemetry) {
							t.Fatal("diagnostic cause lost")
						}
					}
				}
				if mode == "otel" || mode == "otel_limit" {
					spans := x.exporter.GetSpans()
					if len(spans) != 2 {
						t.Fatal("execution spans lost")
					}
					limit := 128
					if mode == "otel_limit" {
						limit = 2
					}
					checkSpan(t, spans[0], next.result, limit)
					checkSpan(t, spans[1], old.result, limit)
					checkMetrics(t, x, []rulite.Result{old.result, next.result})
				}
			})
		}
	}
}

func checkFallback(t testing.TB, out execution) {
	t.Helper()
	version := out.result.Snapshot().Version()
	suffix := "v1"
	if version == "payments/v2" {
		suffix = "v2"
	} else if version != "payments/v1" {
		t.Fatal("unknown snapshot version")
	}
	want := rulite.Counts{Total: 4, Evaluated: 3, NotEvaluated: 1, Matched: 3, Fired: 2, Failed: 1, ActionFailed: 1}
	if !errors.Is(out.err, errUnavailable) || out.result.Counts() != want || out.result.StopReason() != rulite.StopCompleted || !reflect.DeepEqual(out.input.Attempts, []string{"primary/" + suffix, "secondary/" + suffix}) || out.input.Provider != "secondary/"+suffix || out.input.AppliedBy != "payment/secondary" || out.input.AuditVersion != version || out.input.Audit != out.input.Provider+":payment/secondary" {
		t.Fatal("fallback mixed snapshot or lost partial effects")
	}
	g, _ := out.result.Group("payment/providers")
	id, selected := g.SelectedRule()
	manual, _ := out.result.Rule("payment/manual")
	if !selected || id != "payment/secondary" || g.EndReason() != rulite.GroupEndResolved || manual.NotEvaluatedReason() != rulite.NotEvaluatedGroupResolved {
		t.Fatal("group facts changed")
	}
	checkViews(t, out)
}

func TestInvalidReloadsKeepAvailableSnapshot(t *testing.T) {
	l := load(t, localAttempt)
	runtime := initialRuntime(t, l)
	before := runtime.Snapshot()
	for _, name := range invalidFixtures {
		t.Run(name, func(t *testing.T) {
			info, err := l.reload(runtime, fixture(t, name), "rejected")
			if err == nil || info != (rulite.SnapshotInfo{}) || runtime.Snapshot() != before {
				t.Fatal("invalid configuration replaced current snapshot")
			}
			checkFallback(t, execute(context.Background(), runtime, observation{}, "observer", payment{AmountMinor: 100}))
		})
	}
	if info, err := runtime.Publish(nil); err != rulite.ErrInvalidEngine || info != (rulite.SnapshotInfo{}) || runtime.Snapshot() != before {
		t.Fatal("invalid publication changed identity")
	}
}

var invalidFixtures = []string{"syntax", "type", "unknown_action", "duplicate_id", "params", "unknown_param", "duplicate_key"}

func TestReloadConcurrentPublicationsAndViews(t *testing.T) {
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			l := load(t, localAttempt)
			runtime := initialRuntime(t, l)
			x := telemetry(t, "otel")
			var publications sync.Map
			publications.Store(runtime.Snapshot().Revision(), runtime.Snapshot())
			start := make(chan struct{})
			results := make(chan execution, workers*20)
			var wg sync.WaitGroup
			for publisher := range 3 {
				wg.Go(func() {
					<-start
					for i := range 12 {
						if _, err := l.reload(runtime, fixture(t, invalidFixtures[(publisher+i)%len(invalidFixtures)]), "rejected"); err == nil {
							t.Error("invalid compile accepted")
						}
						version := fmt.Sprintf("v%d", 1+(publisher+i)%2)
						info, err := l.reload(runtime, fixture(t, version), rulite.RuleSetVersion("payments/"+version))
						if err != nil {
							t.Error(err)
							return
						}
						if _, exists := publications.LoadOrStore(info.Revision(), info); exists {
							t.Error("publication revision reused")
						}
					}
				})
			}
			for range workers {
				wg.Go(func() {
					<-start
					var previous rulite.SnapshotRevision
					for range 20 {
						out := execute(context.Background(), runtime, x, "otel", payment{AmountMinor: 100})
						checkFallback(t, out)
						if out.result.Snapshot().Revision() < previous {
							t.Error("visible revision decreased")
						}
						previous = out.result.Snapshot().Revision()
						var readers sync.WaitGroup
						readers.Go(func() { checkViews(t, out) })
						checkViews(t, out)
						readers.Wait()
						results <- out
					}
				})
			}
			close(start)
			wg.Wait()
			close(results)
			if runtime.Snapshot().Revision() != 37 {
				t.Fatal("failed compile consumed revision")
			}
			for i := rulite.SnapshotRevision(1); i <= 37; i++ {
				if _, ok := publications.Load(i); !ok {
					t.Fatal("publication sequence has holes")
				}
			}
			var retained []rulite.Result
			byRevision := make(map[rulite.SnapshotRevision]rulite.Result)
			for out := range results {
				info := out.result.Snapshot()
				stored, ok := publications.Load(info.Revision())
				if !ok || stored.(rulite.SnapshotInfo) != info {
					t.Fatal("execution is not a complete publication")
				}
				retained = append(retained, out.result)
				byRevision[info.Revision()] = out.result
			}
			spans := x.exporter.GetSpans()
			if len(spans) != workers*20 || len(retained) != workers*20 {
				t.Fatal("lost execution lifecycle")
			}
			seen := make(map[trace.SpanID]bool)
			for _, span := range spans {
				id := span.SpanContext.SpanID()
				if seen[id] {
					t.Fatal("span reused across executions")
				}
				seen[id] = true
				revision, err := strconv.ParseUint(attr(span.Attributes, "rulite.snapshot.revision").AsString(), 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				r, ok := byRevision[rulite.SnapshotRevision(revision)]
				if !ok {
					t.Fatal("span has no execution")
				}
				checkSpan(t, span, r, 128)
			}
			checkMetrics(t, x, retained)
		})
	}
}

func TestCallerSynchronizesReloadInput(t *testing.T) {
	l := load(t, localAttempt)
	runtime := initialRuntime(t, l)
	x := telemetry(t, "otel_limit")
	var mu sync.Mutex
	var wg sync.WaitGroup
	var shared payment
	for range 8 {
		wg.Go(func() {
			for range 8 {
				func() {
					mu.Lock()
					defer mu.Unlock()
					shared = payment{AmountMinor: 100}
					r, err := x.adapter.Fire(context.Background(), runtime, &shared, rulite.WithTrace())
					checkFallback(t, execution{result: r, err: err, input: shared})
				}()
			}
		})
	}
	wg.Wait()
}

func TestSnapshotIdentityDoesNotClaimFieldWriter(t *testing.T) {
	l := load(t, func(_ context.Context, p *payment, provider string) error {
		p.Attempts = append(p.Attempts, provider)
		return nil
	})
	runtime := initialRuntime(t, l)
	out := execute(context.Background(), runtime, observation{}, "observer", payment{AmountMinor: 100, Provider: "existing", AppliedBy: "upstream/decision"})
	if out.err != nil || out.result.Counts().Fired != 2 || out.input.Provider != "existing" || out.input.AppliedBy != "upstream/decision" || out.input.AuditVersion != "payments/v1" {
		t.Fatal("version replaced actual field provenance")
	}
	g, _ := out.result.Group("payment/providers")
	id, _ := g.SelectedRule()
	if id != "payment/primary" {
		t.Fatal("selection must still identify the successful action")
	}
}

func TestRun(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
