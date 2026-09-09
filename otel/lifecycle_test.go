package otel_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type privateError struct{}

func (*privateError) Error() string { panic("private error must not be formatted") }

func TestBusinessStopsAndPartialResults(t *testing.T) {
	cause := &privateError{}
	for _, tc := range []struct {
		name     string
		outcomes []byte
		options  []rulite.FireOption
		stop     rulite.StopReason
	}{
		{"empty", nil, nil, rulite.StopCompleted},
		{"condition_error", []byte{0, 2, 1}, nil, rulite.StopConditionError},
		{"action_error", []byte{3, 1}, nil, rulite.StopActionError},
		{"condition_panic", []byte{4, 1}, nil, rulite.StopPanic},
		{"action_panic", []byte{5, 1}, nil, rulite.StopPanic},
		{"continued_failures", []byte{2, 3, 1}, []rulite.FireOption{continuing()}, rulite.StopCompleted},
		{"first_match", []byte{1, 1}, []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstMatch))}, rulite.StopFirstMatch},
		{"first_fire", []byte{3, 1, 1}, []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire).WithActionErrors(rulite.ContinueOnError))}, rulite.StopFirstFire},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newTelemetry(t, ruliteotel.WithTraceVersions())
			e := engine(t, "release", rules(tc.outcomes, cause)...)
			plainInput, observedInput := state{}, state{}
			plain, plainErr := e.Fire(context.Background(), &plainInput, tc.options...)
			r, err := x.adapter.Fire(context.Background(), e, &observedInput, tc.options...)
			if plainInput != observedInput || r.Counts() != plain.Counts() || r.StopReason() != tc.stop || len(r.Diagnostics()) != 0 || (err != nil) != (plainErr != nil) || r.Snapshot() != plain.Snapshot() {
				t.Fatal("business result changed")
			}
			for i, f := range r.Failures() {
				p := plain.Failures()[i]
				if f.RuleID() != p.RuleID() || f.Phase() != p.Phase() || f.Continued() != p.Continued() {
					t.Fatal("canonical failure changed")
				}
				var recovered *rulite.PanicError
				if errors.As(f, &recovered) {
					if recovered.Value() != cause {
						t.Fatal("panic identity lost")
					}
				} else if !errors.Is(err, cause) {
					t.Fatal("business error identity lost")
				}
			}
			m := x.collect(t)
			if sum(t, m, "rulite.execution.count", "{execution}") != 1 || sum(t, m, "rulite.rule.evaluated", "{rule}") != int64(r.Counts().Evaluated) || sum(t, m, "rulite.rule.failed", "{rule}") != int64(r.Counts().Failed) {
				t.Fatal("summary does not use final facts")
			}
			d := m["rulite.execution.count"].Data.(metricdata.Sum[int64])
			if attr(d.DataPoints[0].Attributes.ToSlice(), "rulite.stop.reason").AsString() != tc.stop.String() {
				t.Fatal("stop label changed")
			}
			s := x.exporter.GetSpans()[0]
			if attr(s.Attributes, "rulite.stop.reason").AsString() != tc.stop.String() || attr(s.Attributes, "rulite.rules.failed").AsInt64() != int64(r.Counts().Failed) {
				t.Fatal("span terminal facts changed")
			}
		})
	}
}

func TestCanceledContextsAndCallbackContextIdentity(t *testing.T) {
	for _, scenario := range []string{"already_canceled", "deadline", "after_condition", "after_action", "finish"} {
		t.Run(scenario, func(t *testing.T) {
			x := newTelemetry(t)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			cause := &privateError{}
			want := rulite.StopContextCanceled
			switch scenario {
			case "already_canceled":
				cancel(cause)
			case "deadline":
				var stop context.CancelFunc
				ctx, stop = context.WithDeadlineCause(context.Background(), time.Unix(1, 0), cause)
				defer stop()
				want = rulite.StopContextDeadlineExceeded
			case "finish":
				want = rulite.StopCompleted
				x.traces.RegisterSpanProcessor(&finishProcessor{onEnd: func(sdktrace.ReadOnlySpan) { cancel(cause) }})
			}
			e := engine(t, "", rulite.NewRule[state]("context").When(func(seen context.Context, _ *state) (bool, error) {
				if seen != ctx {
					t.Fatal("condition context changed")
				}
				if scenario == "after_condition" {
					cancel(cause)
				}
				return true, nil
			}).Then(func(seen context.Context, input *state) error {
				if seen != ctx {
					t.Fatal("action context changed")
				}
				input.value++
				if scenario == "after_action" {
					cancel(cause)
				}
				return nil
			}))
			r, err := x.adapter.Fire(ctx, e, &state{})
			if r.StopReason() != want || len(r.Diagnostics()) != 0 {
				t.Fatal("context boundary changed")
			}
			if scenario == "finish" {
				if err != nil {
					t.Fatal("finish cancellation revised terminal result")
				}
			} else if !errors.Is(err, cause) {
				t.Fatal("context cause lost")
			}
			if scenario == "after_condition" && (r.Counts().Matched != 1 || r.Counts().Skipped != 1 || r.Counts().Fired != 0) {
				t.Fatal("skipped action became fired")
			}
			if sum(t, x.collect(t), "rulite.execution.count", "{execution}") != 1 || len(x.exporter.GetSpans()) != 1 {
				t.Fatal("canceled execution telemetry missing")
			}
		})
	}
}

type finishProcessor struct{ onEnd func(sdktrace.ReadOnlySpan) }

func (*finishProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (p *finishProcessor) OnEnd(s sdktrace.ReadOnlySpan)                 { p.onEnd(s) }
func (*finishProcessor) Shutdown(context.Context) error                  { return nil }
func (*finishProcessor) ForceFlush(context.Context) error                { return nil }

type faultProvider struct {
	trace.TracerProvider
	at      string
	payload any
	ends    atomic.Int64
}

func (p *faultProvider) Tracer(name string, options ...trace.TracerOption) trace.Tracer {
	return faultTracer{Tracer: p.TracerProvider.Tracer(name, options...), owner: p}
}

type faultTracer struct {
	trace.Tracer
	owner *faultProvider
}

func (t faultTracer) Start(ctx context.Context, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	if t.owner.at == "start" {
		panic(t.owner.payload)
	}
	ctx, s := t.Tracer.Start(ctx, name, options...)
	return ctx, &faultSpan{Span: s, owner: t.owner}
}

type faultSpan struct {
	trace.Span
	owner *faultProvider
}

func (s *faultSpan) SetAttributes(attrs ...attribute.KeyValue) {
	if s.owner.at == "attributes" {
		panic(s.owner.payload)
	}
	s.Span.SetAttributes(attrs...)
}
func (s *faultSpan) AddEvent(name string, options ...trace.EventOption) {
	if s.owner.at == "event" {
		panic(s.owner.payload)
	}
	s.Span.AddEvent(name, options...)
}
func (s *faultSpan) End(options ...trace.SpanEndOption) {
	s.owner.ends.Add(1)
	s.Span.End(options...)
	if s.owner.at == "end" {
		panic(s.owner.payload)
	}
}

func TestProviderPanicIsolationAndLifecycle(t *testing.T) {
	for _, at := range []string{"start", "attributes", "event", "end"} {
		for _, propagate := range []bool{false, true} {
			t.Run(at+map[bool]string{false: "_recover", true: "_propagate"}[propagate], func(t *testing.T) {
				x := newTelemetry(t)
				payload := &privateError{}
				p := &faultProvider{TracerProvider: x.traces, at: at, payload: payload}
				a, err := ruliteotel.New(p, x.metrics)
				if err != nil {
					t.Fatal(err)
				}
				e := engine(t, "", rules([]byte{1, 1}, nil)...)
				for range 2 {
					input := state{}
					if propagate {
						func() {
							defer func() {
								if recover() != payload {
									t.Error("provider panic changed")
								}
							}()
							_, _ = a.Fire(context.Background(), e, &input, rulite.WithPanicMode(rulite.PropagatePanics))
							t.Error("provider panic returned")
						}()
					} else {
						r, err := a.Fire(context.Background(), e, &input)
						d := r.Diagnostics()
						var recovered *rulite.ObserverPanicError
						if err != nil || r.StopReason() != rulite.StopCompleted || r.Counts().Fired != 2 || input.value != 2 || len(d) != 1 || !errors.As(d[0], &recovered) || recovered.Value() != payload || len(recovered.Stack()) == 0 {
							t.Fatal("provider failure changed business or lost diagnostic")
						}
						if errors.As(err, &recovered) || strings.Contains(d[0].Error(), "private error must") {
							t.Fatal("diagnostic leaked into business error or text")
						}
					}
				}
				wantEnds := int64(2)
				if at == "start" {
					wantEnds = 0
				}
				if p.ends.Load() != wantEnds || len(x.exporter.GetSpans()) != int(wantEnds) {
					t.Fatal("span leaked or ended repeatedly")
				}
				if at == "event" {
					for _, s := range x.exporter.GetSpans() {
						if !attr(s.Attributes, "rulite.observation.incomplete").AsBool() {
							t.Fatal("missing incomplete marker")
						}
					}
				}
			})
		}
	}
}

func TestPropagatedBusinessPanicPreservesPayloadAndClosesSpan(t *testing.T) {
	for _, cleanupPanic := range []bool{false, true} {
		x := newTelemetry(t)
		p := &faultProvider{TracerProvider: x.traces, payload: "secondary export panic"}
		if cleanupPanic {
			p.at = "end"
		}
		a, err := ruliteotel.New(p, x.metrics)
		if err != nil {
			t.Fatal(err)
		}
		payload := &privateError{}
		e := engine(t, "", rules([]byte{5}, payload)...)
		func() {
			defer func() {
				if recover() != payload {
					t.Error("cleanup replaced business panic")
				}
			}()
			_, _ = a.Fire(context.Background(), e, &state{}, rulite.WithPanicMode(rulite.PropagatePanics))
			t.Error("business panic returned")
		}()
		s := x.exporter.GetSpans()
		if p.ends.Load() != 1 || len(s) != 1 || !attr(s[0].Attributes, "rulite.observation.incomplete").AsBool() || s[0].Status.Code != codes.Error {
			t.Fatal("unfinished span lifecycle changed")
		}
		if sum(t, x.collect(t), "rulite.execution.count", "{execution}") != 0 {
			t.Fatal("invented completed execution metric")
		}
	}
}

type faultMeterProvider struct {
	metric.MeterProvider
	cause    error
	panicAdd bool
}

func (p faultMeterProvider) Meter(name string, options ...metric.MeterOption) metric.Meter {
	return faultMeter{Meter: p.MeterProvider.Meter(name, options...), cause: p.cause, panicAdd: p.panicAdd}
}

type faultMeter struct {
	metric.Meter
	cause    error
	panicAdd bool
}

func (m faultMeter) Int64Counter(name string, options ...metric.Int64CounterOption) (metric.Int64Counter, error) {
	if m.cause != nil {
		return nil, m.cause
	}
	c, err := m.Meter.Int64Counter(name, options...)
	return faultCounter{Int64Counter: c, panics: m.panicAdd}, err
}

type faultCounter struct {
	metric.Int64Counter
	panics bool
}

func (c faultCounter) Enabled(context.Context) bool { return true }
func (c faultCounter) Add(ctx context.Context, value int64, options ...metric.AddOption) {
	if c.panics {
		panic("metric unavailable")
	}
	c.Int64Counter.Add(ctx, value, options...)
}

func TestMetricFailuresAndConstructionErrors(t *testing.T) {
	cause := &privateError{}
	a, err := ruliteotel.New(tracenoop.NewTracerProvider(), faultMeterProvider{MeterProvider: metricnoop.NewMeterProvider(), cause: cause})
	if a != nil || !errors.Is(err, cause) || err.Error() != "rulite/otel: instrument construction failed" {
		t.Fatal("construction error identity or redaction changed")
	}
	x := newTelemetry(t)
	a, err = ruliteotel.New(x.traces, faultMeterProvider{MeterProvider: x.metrics, panicAdd: true})
	if err != nil {
		t.Fatal(err)
	}
	e := engine(t, "", rules([]byte{3}, cause)...)
	r, err := a.Fire(context.Background(), e, &state{})
	var recovered *rulite.ObserverPanicError
	if !errors.Is(err, cause) || r.StopReason() != rulite.StopActionError || r.Counts().Failed != 1 || len(r.Diagnostics()) != 1 || !errors.As(r.Diagnostics()[0], &recovered) {
		t.Fatal("metric failure changed canonical execution error")
	}
	if !attr(x.exporter.GetSpans()[0].Attributes, "rulite.observation.incomplete").AsBool() {
		t.Fatal("metric failure did not close incomplete span")
	}
}

func TestGroupsUseOrderedRuleFacts(t *testing.T) {
	x := newTelemetry(t)
	rules := rules([]byte{3, 1, 1, 1}, errors.New("provider unavailable"))
	group := rulite.FirstFireGroup("providers", rules[:3]...)
	set, err := rulite.CompileEntries(group.Entry(), rules[3].Entry())
	if err != nil {
		t.Fatal(err)
	}
	e, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := x.adapter.Fire(context.Background(), e, &state{}, continuing(), rulite.WithTrace())
	g, ok := r.Group("providers")
	selected, selectedOK := g.SelectedRule()
	if !ok || !selectedOK || selected != "rule/1" || r.Counts().Evaluated != 3 || r.Counts().Fired != 2 || g.EndReason() != rulite.GroupEndResolved {
		t.Fatal("group resolution changed")
	}
	var ids []string
	for _, event := range x.exporter.GetSpans()[0].Events {
		ids = append(ids, attr(event.Attributes, "rulite.rule.id").AsString())
	}
	if !reflect.DeepEqual(ids, []string{"rule/0", "rule/0", "rule/0", "rule/1", "rule/1", "rule/1", "rule/3", "rule/3", "rule/3"}) {
		t.Fatal("group events duplicated rule outcomes or bypassed rule exported")
	}
	if sum(t, x.collect(t), "rulite.rule.fired", "{rule}") != 2 {
		t.Fatal("group summary changed")
	}
}

func TestTraceVersionBoundsAndEmptyAllowlist(t *testing.T) {
	for _, version := range []rulite.RuleSetVersion{"", rulite.RuleSetVersion(strings.Repeat("v", 129)), "\xff"} {
		x := newTelemetry(t, ruliteotel.WithTraceVersions(), ruliteotel.WithTraceRules(), ruliteotel.WithEventLimit(0), ruliteotel.WithMetricVersions(), ruliteotel.WithRuleMetrics())
		e := engine(t, version, rules([]byte{1}, nil)...)
		r, err := x.adapter.Fire(context.Background(), e, &state{})
		if err != nil || len(r.Diagnostics()) != 0 {
			t.Fatal("filtered events consumed limit")
		}
		s := x.exporter.GetSpans()[0]
		if len(s.Events) != 0 || !attr(s.Attributes, "rulite.ruleset.version.omitted").AsBool() || attr(s.Attributes, "rulite.ruleset.version").Type() != attribute.INVALID || attr(s.Attributes, "rulite.events.filtered").AsInt64() != 3 {
			t.Fatal("unsafe version or filter accounting")
		}
		m := x.collect(t)
		if _, ok := m["rulite.rule.events"]; ok {
			t.Fatal("empty rule allowlist recorded metrics")
		}
	}
}

type failingExporter struct {
	cause error
	calls int
}

func (e *failingExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error {
	e.calls++
	return e.cause
}
func (*failingExporter) Shutdown(context.Context) error { return nil }

func TestExporterErrorsRemainProviderOwned(t *testing.T) {
	cause := errors.New("export unavailable")
	exporter := &failingExporter{cause: cause}
	var providerError error
	processor := &finishProcessor{onEnd: func(span sdktrace.ReadOnlySpan) {
		providerError = exporter.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{span})
	}}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(processor))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	a, err := ruliteotel.New(provider, metricnoop.NewMeterProvider())
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Fire(context.Background(), engine(t, "", rules([]byte{1}, nil)...), &state{})
	if err != nil || r.StopReason() != rulite.StopCompleted || len(r.Diagnostics()) != 0 || exporter.calls != 1 || !errors.Is(providerError, cause) {
		t.Fatal("provider export error changed execution or was lost by its owner")
	}
}

func TestSDKLimitsRemainIndependent(t *testing.T) {
	limits := sdktrace.NewSpanLimits()
	limits.EventCountLimit = 2
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanLimits(limits), sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	a, err := ruliteotel.New(provider, metricnoop.NewMeterProvider(), ruliteotel.WithEventLimit(3))
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Fire(context.Background(), engine(t, "", rules([]byte{1}, nil)...), &state{}, rulite.WithTrace())
	if err != nil || len(r.Diagnostics()) != 0 || r.Counts().Fired != 1 {
		t.Fatal("SDK limit changed business or invented adapter diagnostic")
	}
	s := exporter.GetSpans()[0]
	if len(s.Events) != 2 || s.DroppedEvents != 1 || attr(s.Attributes, "rulite.events.recorded").AsInt64() != 3 || attr(s.Attributes, "rulite.events.dropped").AsInt64() != 0 {
		t.Fatal("SDK drop was confused with adapter drop")
	}
}
