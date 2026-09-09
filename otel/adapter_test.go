package otel_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

type telemetry struct {
	adapter  *ruliteotel.Adapter
	reader   *sdkmetric.ManualReader
	exporter *tracetest.InMemoryExporter
	traces   *sdktrace.TracerProvider
	metrics  *sdkmetric.MeterProvider
}

func newTelemetry(t testing.TB, options ...ruliteotel.Option) telemetry {
	t.Helper()
	x := telemetry{reader: sdkmetric.NewManualReader(), exporter: tracetest.NewInMemoryExporter()}
	limits := sdktrace.NewSpanLimits()
	limits.EventCountLimit = 1024
	x.traces = sdktrace.NewTracerProvider(sdktrace.WithResource(resource.Empty()), sdktrace.WithSpanLimits(limits), sdktrace.WithSyncer(x.exporter))
	x.metrics = sdkmetric.NewMeterProvider(sdkmetric.WithResource(resource.Empty()), sdkmetric.WithReader(x.reader))
	var err error
	x.adapter, err = ruliteotel.New(x.traces, x.metrics, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := x.traces.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
		if err := x.metrics.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return x
}

func (x telemetry) collect(t testing.TB) map[string]metricdata.Metrics {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := x.reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	m := make(map[string]metricdata.Metrics)
	for _, scope := range data.ScopeMetrics {
		if scope.Scope.Name != "github.com/imbrooklyn/rulite/otel" {
			t.Fatalf("unexpected scope: %s", scope.Scope.Name)
		}
		for _, value := range scope.Metrics {
			m[value.Name] = value
		}
	}
	return m
}

func attr(attrs []attribute.KeyValue, key string) attribute.Value {
	for _, item := range attrs {
		if string(item.Key) == key {
			return item.Value
		}
	}
	return attribute.Value{}
}

func sum(t testing.TB, metrics map[string]metricdata.Metrics, name, unit string) int64 {
	t.Helper()
	m, ok := metrics[name]
	if !ok {
		return 0
	}
	data, ok := m.Data.(metricdata.Sum[int64])
	if m.Unit != unit || !ok || !data.IsMonotonic || data.Temporality != metricdata.CumulativeTemporality {
		t.Fatalf("invalid counter contract: %+v", m)
	}
	var total int64
	for _, point := range data.DataPoints {
		if point.Value < 0 {
			t.Fatal("counter decreased")
		}
		total += point.Value
	}
	return total
}

func engine[T any](t testing.TB, version rulite.RuleSetVersion, rules ...rulite.Rule[T]) *rulite.Engine[T] {
	t.Helper()
	set, err := rulite.Compile(rules...)
	if err != nil {
		t.Fatal(err)
	}
	set, err = set.WithIdentity(version, "private-source-digest")
	if err != nil {
		t.Fatal(err)
	}
	e, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

type state struct{ value int }

func rules(outcomes []byte, cause error) []rulite.Rule[state] {
	rules := make([]rulite.Rule[state], len(outcomes))
	for i, outcome := range outcomes {
		rules[i] = rulite.NewRule[state](rulite.RuleID(fmt.Sprintf("rule/%d", i))).
			Name("private-name").Description("private-description").Tags("private-tag").
			When(func(_ context.Context, input *state) (bool, error) {
				switch outcome % 6 {
				case 0:
					return false, nil
				case 2:
					return true, cause
				case 4:
					panic(cause)
				}
				return true, nil
			}).Then(func(_ context.Context, input *state) error {
			input.value++
			switch outcome % 6 {
			case 3:
				return cause
			case 5:
				panic(cause)
			}
			return nil
		})
	}
	return rules
}

func continuing() rulite.FireOption {
	return rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(rulite.ContinueOnError).WithActionErrors(rulite.ContinueOnError))
}

func TestMetricUnitsCountsAndAggregation(t *testing.T) {
	x := newTelemetry(t)
	cause := errors.New("private-business-error")
	e := engine(t, "unbounded-business-version", rules([]byte{0, 1, 2, 3}, cause)...)
	for range 2 {
		input := state{}
		r, err := x.adapter.Fire(context.Background(), e, &input, continuing())
		if !errors.Is(err, cause) || input.value != 2 || r.Counts().Failed != 2 || len(r.Diagnostics()) != 0 {
			t.Fatal("business facts changed")
		}
	}
	m := x.collect(t)
	for name, want := range map[string]int64{"rulite.execution.count": 2, "rulite.rule.evaluated": 8, "rulite.rule.matched": 4, "rulite.rule.fired": 2, "rulite.rule.failed": 4} {
		unit := "{rule}"
		if name == "rulite.execution.count" {
			unit = "{execution}"
		}
		if got := sum(t, m, name, unit); got != want {
			t.Fatalf("%s=%d; want %d", name, got, want)
		}
		data := m[name].Data.(metricdata.Sum[int64])
		if len(data.DataPoints) != 1 || data.DataPoints[0].Attributes.Len() != 1 || attr(data.DataPoints[0].Attributes.ToSlice(), "rulite.stop.reason").AsString() != "completed" {
			t.Fatalf("unbounded default attributes: %+v", data)
		}
	}
	h := m["rulite.execution.duration"]
	hist, ok := h.Data.(metricdata.Histogram[float64])
	if !ok || h.Unit != "s" || hist.Temporality != metricdata.CumulativeTemporality || len(hist.DataPoints) != 1 || hist.DataPoints[0].Count != 2 || hist.DataPoints[0].Sum < 0 {
		t.Fatalf("invalid duration: %+v", h)
	}
	if !slices.Equal(hist.DataPoints[0].Bounds, []float64{.0001, .001, .01, .1, 1, 10}) {
		t.Fatal("histogram advice lost")
	}
	if len(m) != 6 {
		t.Fatalf("default instruments=%d", len(m))
	}
	if got := sum(t, x.collect(t), "rulite.execution.count", "{execution}"); got != 2 {
		t.Fatal("collection reset cumulative counter")
	}
}

func TestSpanOrderParentIdentityAndRedaction(t *testing.T) {
	x := newTelemetry(t, ruliteotel.WithTraceVersions())
	parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), parent)
	cause := errors.New("private-business-error")
	e := engine(t, "release/v1", rules([]byte{0, 1, 2, 3}, cause)...)
	r, err := x.adapter.Fire(ctx, e, &state{}, continuing(), rulite.WithTrace())
	if !errors.Is(err, cause) || len(r.Diagnostics()) != 0 {
		t.Fatal("error model changed")
	}
	spans := x.exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("spans=%d", len(spans))
	}
	s := spans[0]
	if s.Name != "rulite.fire" || s.SpanKind != trace.SpanKindInternal || !s.Parent.Equal(parent) || s.SpanContext.TraceID() != parent.TraceID() || s.Status.Code != codes.Error {
		t.Fatalf("incorrect span: %+v", s)
	}
	if attr(s.Attributes, "rulite.snapshot.revision").AsString() != "0" || attr(s.Attributes, "rulite.ruleset.version").AsString() != string(r.Snapshot().Version()) {
		t.Fatal("wrong captured identity")
	}
	want := []string{"rule-evaluated", "rule-evaluated", "rule-matched", "rule-fired", "rule-evaluated", "rule-failed", "rule-evaluated", "rule-matched", "rule-failed"}
	if len(s.Events) != len(want) {
		t.Fatalf("events=%d", len(s.Events))
	}
	for i, event := range s.Events {
		if event.Name != want[i] || i > 0 && event.Time.Before(s.Events[i-1].Time) {
			t.Fatal("event order changed")
		}
		if attr(event.Attributes, "rulite.rule.id").AsString() == "" {
			t.Fatal("missing rule identity")
		}
	}
	if attr(s.Events[4].Attributes, "rulite.condition.outcome").AsString() != "error" || attr(s.Events[8].Attributes, "rulite.rule.phase").AsString() != "action" {
		t.Fatal("failure outcome or phase changed")
	}
	if strings.Contains(fmt.Sprint(s), "private-") {
		t.Fatal("sensitive metadata or error exported")
	}
	tr, ok := r.Trace()
	if !ok || len(tr.Rules()) != 4 || tr.Snapshot() != r.Snapshot() {
		t.Fatal("core trace changed")
	}
}

func TestTraceLimitsFilteringAndCoreCompleteness(t *testing.T) {
	for _, limit := range []int{0, 1, 4, 9, 1024} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			x := newTelemetry(t, ruliteotel.WithEventLimit(limit), ruliteotel.WithTraceRules("rule/1", "rule/2"), ruliteotel.WithoutTraceRuleIDs())
			e := engine(t, "private-version", rules([]byte{1, 1, 1}, nil)...)
			r, err := x.adapter.Fire(context.Background(), e, &state{}, rulite.WithTrace())
			if err != nil || r.Counts().Fired != 3 || len(r.Failures()) != 0 {
				t.Fatal("truncation changed business outcome")
			}
			s := x.exporter.GetSpans()[0]
			want := min(limit, 6)
			if len(s.Events) != want || attr(s.Attributes, "rulite.events.dropped").AsInt64() != int64(6-want) || attr(s.Attributes, "rulite.events.filtered").AsInt64() != 3 || attr(s.Attributes, "rulite.events.recorded").AsInt64() != int64(want) {
				t.Fatal("limit accounting changed")
			}
			for i, event := range s.Events {
				if attr(event.Attributes, "rulite.rule.id").Type() != attribute.INVALID || attr(event.Attributes, "rulite.rule.order").AsInt64() != int64(1+i/3) {
					t.Fatal("redaction or retained prefix changed")
				}
			}
			if attr(s.Attributes, "rulite.ruleset.version").Type() != attribute.INVALID {
				t.Fatal("version exported without opt-in")
			}
			d := r.Diagnostics()
			if want < 6 {
				if len(d) != 1 || !errors.Is(d[0], ruliteotel.ErrEventLimit) || d[0].Event().Kind() != rulite.EventExecutionFinished || d[0].Event().Snapshot() != r.Snapshot() {
					t.Fatal("limit diagnostic missing or misplaced")
				}
			} else if len(d) != 0 {
				t.Fatal("false limit diagnostic")
			}
			tr, _ := r.Trace()
			if len(tr.Rules()) != 3 || len(r.Fired()) != 3 || sum(t, x.collect(t), "rulite.rule.fired", "{rule}") != 3 {
				t.Fatal("export limit truncated ledger or metrics")
			}
		})
	}
}

func TestBoundedVersionAndRuleMetrics(t *testing.T) {
	versions := []rulite.RuleSetVersion{"allowed"}
	ids := []rulite.RuleID{"rule/0"}
	options := []ruliteotel.Option{ruliteotel.WithMetricVersions(versions...), ruliteotel.WithRuleMetrics(ids...), ruliteotel.WithTraceRules(ids...)}
	versions[0], ids[0] = "changed", "rule/1"
	x := newTelemetry(t, options...)
	for _, version := range []rulite.RuleSetVersion{"allowed", "other-a", "other-b", ""} {
		e := engine(t, version, rules([]byte{1, 1}, nil)...)
		runtime, err := rulite.NewRuntime(e)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err = runtime.Publish(e); err != nil {
				t.Fatal(err)
			}
			if _, err = x.adapter.Fire(context.Background(), runtime, &state{}); err != nil {
				t.Fatal(err)
			}
		}
	}
	m := x.collect(t)
	d := m["rulite.execution.count"].Data.(metricdata.Sum[int64])
	if len(d.DataPoints) != 2 {
		t.Fatalf("version cardinality=%d", len(d.DataPoints))
	}
	for _, point := range d.DataPoints {
		v := attr(point.Attributes.ToSlice(), "rulite.ruleset.version").AsString()
		want := int64(6)
		if v == "allowed" {
			want = 2
		} else if v != "other" {
			t.Fatal("unlisted version label")
		}
		if point.Value != want || point.Attributes.Len() != 2 {
			t.Fatal("revision label or incorrect version aggregation")
		}
	}
	if sum(t, m, "rulite.rule.events", "{event}") != 24 {
		t.Fatal("allowlisted counts changed")
	}
	rules := m["rulite.rule.events"].Data.(metricdata.Sum[int64])
	if len(rules.DataPoints) != 3 {
		t.Fatal("unbounded rule series")
	}
	for _, point := range rules.DataPoints {
		if point.Attributes.Len() != 2 || attr(point.Attributes.ToSlice(), "rulite.rule.id").AsString() != "rule/0" || point.Value != 8 {
			t.Fatal("rule allowlist mutated")
		}
	}
	for _, s := range x.exporter.GetSpans() {
		if len(s.Events) != 3 {
			t.Fatal("trace allowlist mutated")
		}
	}
}

func TestConfigurationAndPreflight(t *testing.T) {
	tp, mp := tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider()
	for _, option := range []ruliteotel.Option{
		ruliteotel.WithEventLimit(-1), ruliteotel.WithEventLimit(1025),
		ruliteotel.WithMetricVersions(""), ruliteotel.WithMetricVersions("other"),
		ruliteotel.WithMetricVersions(rulite.RuleSetVersion(strings.Repeat("v", 129))),
		ruliteotel.WithMetricVersions(rulite.RuleSetVersion("\xff")),
		ruliteotel.WithMetricVersions(make([]rulite.RuleSetVersion, 17)...),
		ruliteotel.WithRuleMetrics(make([]rulite.RuleID, 65)...),
		ruliteotel.WithRuleMetrics(""), ruliteotel.WithTraceRules(""),
		ruliteotel.WithTraceRules(make([]rulite.RuleID, 65)...),
	} {
		if a, err := ruliteotel.New(tp, mp, option, ruliteotel.WithEventLimit(128)); a != nil || !errors.Is(err, ruliteotel.ErrInvalidConfig) {
			t.Fatal("invalid option repaired")
		}
	}
	if _, err := ruliteotel.New(nil, mp); !errors.Is(err, ruliteotel.ErrInvalidConfig) {
		t.Fatal("nil trace provider accepted")
	}
	if _, err := ruliteotel.New(tp, nil); !errors.Is(err, ruliteotel.ErrInvalidConfig) {
		t.Fatal("nil meter provider accepted")
	}
	x := newTelemetry(t)
	e := engine[state](t, "")
	for _, c := range []struct {
		ctx    context.Context
		input  *state
		engine *rulite.Engine[state]
		option rulite.FireOption
	}{
		{nil, &state{}, e, rulite.FireOption{}},
		{context.Background(), nil, e, rulite.FireOption{}},
		{context.Background(), &state{}, nil, rulite.FireOption{}},
		{context.Background(), &state{}, e, rulite.WithPanicMode(255)},
	} {
		r, err := x.adapter.Fire(c.ctx, c.engine, c.input, c.option)
		if err == nil || r.Executed() || len(r.Diagnostics()) != 0 {
			t.Fatal("preflight changed")
		}
	}
	var zero ruliteotel.Adapter
	for _, a := range []*ruliteotel.Adapter{nil, &zero} {
		if _, err := a.Fire(context.Background(), e, &state{}); !errors.Is(err, ruliteotel.ErrInvalidAdapter) {
			t.Fatal("invalid adapter accepted")
		}
	}
	if _, err := x.adapter.Fire[state](context.Background(), nil, &state{}); !errors.Is(err, ruliteotel.ErrInvalidExecutor) {
		t.Fatal("nil executor accepted")
	}
	var emptyRuntime rulite.Runtime[state]
	if _, err := x.adapter.Fire(context.Background(), &emptyRuntime, &state{}); !errors.Is(err, rulite.ErrInvalidRuntime) {
		t.Fatal("unpublished runtime executed")
	}
	if len(x.exporter.GetSpans()) != 0 || len(x.collect(t)) != 0 {
		t.Fatal("preflight exported telemetry")
	}
}

func TestDisabledSignalsAndObserverReplacement(t *testing.T) {
	a, err := ruliteotel.New(tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider(), ruliteotel.WithEventLimit(0))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	observer := rulite.ObserverFunc(func(context.Context, rulite.Event) error { calls++; return nil })
	set, err := rulite.Compile(rules([]byte{1}, nil)...)
	if err != nil {
		t.Fatal(err)
	}
	e, err := rulite.NewEngineFromRuleSet(set, rulite.WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	options := make([]rulite.FireOption, 1, 8)
	options[0] = rulite.WithObserver(observer)
	before := slices.Clone(options[:cap(options)])
	for _, opts := range [][]rulite.FireOption{options, {rulite.WithObserver(nil)}} {
		r, err := a.Fire(context.Background(), e, &state{}, opts...)
		if err != nil || len(r.Diagnostics()) != 0 || r.Counts().Fired != 1 || calls != 0 {
			t.Fatal("disabled signals or observer replacement changed")
		}
	}
	if !reflect.DeepEqual(options[:cap(options)][1:], before[1:]) {
		t.Fatal("caller option storage modified")
	}
	if _, err := e.Fire(context.Background(), &state{}); err != nil || calls != 5 {
		t.Fatal("engine default changed")
	}
	if _, err := e.Fire(context.Background(), &state{}, options...); err != nil || calls != 10 {
		t.Fatal("caller observer option changed")
	}
}

func TestDurationAndSynchronousBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		x := newTelemetry(t)
		x.traces.RegisterSpanProcessor(&delayedProcessor{})
		e := engine(t, "", rulite.NewRule[state]("timed").When(func(context.Context, *state) (bool, error) { time.Sleep(3 * time.Second); return true, nil }).Then(func(context.Context, *state) error { time.Sleep(2 * time.Second); return nil }))
		start := time.Now()
		r, err := x.adapter.Fire(context.Background(), e, &state{}, rulite.WithTrace())
		if err != nil || time.Since(start) != 12*time.Second {
			t.Fatal("finish export escaped synchronous Fire")
		}
		h := x.collect(t)["rulite.execution.duration"].Data.(metricdata.Histogram[float64])
		if h.DataPoints[0].Sum != 5 {
			t.Fatal("duration unit or finish boundary changed")
		}
		tr, _ := r.Trace()
		if tr.Duration() != 12*time.Second || tr.Rules()[0].ConditionDuration() != 3*time.Second || tr.Rules()[0].ActionDuration() != 2*time.Second {
			t.Fatal("adapter latency changed callback timing")
		}
	})
}

type delayedProcessor struct{ sdktrace.SpanProcessor }

func (*delayedProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (*delayedProcessor) OnEnd(sdktrace.ReadOnlySpan)                     { time.Sleep(7 * time.Second) }
func (*delayedProcessor) Shutdown(context.Context) error                  { return nil }
func (*delayedProcessor) ForceFlush(context.Context) error                { return nil }
