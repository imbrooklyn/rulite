package otel

import (
	"context"
	"strconv"
	"time"

	"github.com/imbrooklyn/rulite"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const scopeName = "github.com/imbrooklyn/rulite/otel"

// Adapter is immutable and safe for concurrent Fire calls, including calls
// sharing a context. Construct it with New. It retains instruments and a tracer,
// but no executions, inputs, engines, or completed results. Provider owners must
// coordinate shutdown with active calls. Adapter has no background work or Close.
type Adapter struct {
	tracer      trace.Tracer
	counters    [5]metric.Int64Counter
	duration    metric.Float64Histogram
	ruleCounter metric.Int64Counter
	config      config
	fallback    terminalAttributes
	versions    map[rulite.RuleSetVersion]terminalAttributes
	rules       map[rulite.RuleID][4]metric.MeasurementOption
}

type terminalAttributes [9]metric.MeasurementOption

// New constructs instruments using explicit providers. Supply official no-op
// providers to disable either signal. Nil providers and invalid options return
// ErrInvalidConfig. Instrument construction errors retain their causes through
// Unwrap but use sanitized text. Provider construction panics propagate.
// Providers must implement the concurrency guarantees of the OTel API.
func New(traces trace.TracerProvider, metrics metric.MeterProvider, options ...Option) (*Adapter, error) {
	if traces == nil || metrics == nil {
		return nil, ErrInvalidConfig
	}
	c, err := configure(options)
	if err != nil {
		return nil, err
	}
	a := &Adapter{config: c, tracer: traces.Tracer(scopeName)}
	meter := metrics.Meter(scopeName)
	names := [5]string{"rulite.execution.count", "rulite.rule.evaluated", "rulite.rule.matched", "rulite.rule.fired", "rulite.rule.failed"}
	descriptions := [5]string{"Finished executions", "Conditions evaluated", "Conditions returning true without error", "Actions returning without error", "Failed conditions or actions, including recovered panics"}
	for i, name := range names {
		unit := "{rule}"
		if i == 0 {
			unit = "{execution}"
		}
		a.counters[i], err = meter.Int64Counter(name, metric.WithUnit(unit), metric.WithDescription(descriptions[i]))
		if err != nil {
			return nil, &instrumentError{cause: err}
		}
	}
	a.duration, err = meter.Float64Histogram("rulite.execution.duration", metric.WithUnit("s"), metric.WithDescription("Elapsed time between execution start and finish observation, excluding finish export"), metric.WithExplicitBucketBoundaries(.0001, .001, .01, .1, 1, 10))
	if err != nil {
		return nil, &instrumentError{cause: err}
	}
	a.fallback = makeTerminalAttributes("", false)
	if c.versionMetrics {
		a.fallback = makeTerminalAttributes("other", true)
		a.versions = make(map[rulite.RuleSetVersion]terminalAttributes, len(c.metricVersions))
		for _, version := range c.metricVersions {
			a.versions[rulite.RuleSetVersion(version)] = makeTerminalAttributes(version, true)
		}
	}
	if len(c.ruleMetrics) != 0 {
		a.ruleCounter, err = meter.Int64Counter("rulite.rule.events", metric.WithUnit("{event}"), metric.WithDescription("Observed rule events for explicitly allowed IDs"))
		if err != nil {
			return nil, &instrumentError{cause: err}
		}
		a.rules = make(map[rulite.RuleID][4]metric.MeasurementOption, len(c.ruleMetrics))
		for _, id := range c.ruleMetrics {
			var sets [4]metric.MeasurementOption
			for i, name := range [...]string{"evaluated", "matched", "fired", "failed"} {
				sets[i] = metric.WithAttributeSet(attribute.NewSet(attribute.String("rulite.rule.id", id), attribute.String("rulite.rule.event", name)))
			}
			a.rules[rulite.RuleID(id)] = sets
		}
	}
	return a, nil
}

type instrumentError struct{ cause error }

func (*instrumentError) Error() string   { return "rulite/otel: instrument construction failed" }
func (e *instrumentError) Unwrap() error { return e.cause }

func makeTerminalAttributes(version string, include bool) terminalAttributes {
	var sets terminalAttributes
	for reason := rulite.StopCompleted; reason <= rulite.StopPanic; reason++ {
		attrs := []attribute.KeyValue{attribute.String("rulite.stop.reason", reason.String())}
		if include {
			attrs = append(attrs, attribute.String("rulite.ruleset.version", version))
		}
		sets[reason] = metric.WithAttributeSet(attribute.NewSet(attrs...))
	}
	return sets
}

// Fire observes one execution of an Engine or Runtime, inferring T from input.
// It appends its private Observer after the supplied options, replacing any
// engine default or per-call observer, including WithObserver(nil). To disable
// observation, call the executor's Fire directly. Other options are unchanged.
// The caller's option slice and business callback context are not modified.
//
// The execution span follows the caller's parent span but is not installed in
// business callback contexts. Preflight failures create no telemetry. Empty and
// already-canceled executions still produce start/finish telemetry when providers
// accept the canceled context. Synchronous provider work contributes to latency.
//
// Provider panics follow the core PanicMode. Recovered panics disable observation
// and become independent Result diagnostics. A propagated panic has no promised
// Result or finish event. Once a span handle is available, unfinished observation
// attempts to mark it incomplete and end it once. Secondary cleanup panics are
// suppressed to preserve the original
// business return or panic. Exporter errors handled by a provider are owned by
// that provider and cannot become retroactive Result diagnostics.
// Custom executors must delegate exactly one core Fire with the supplied options.
func (a *Adapter) Fire[T any](ctx context.Context, executor interface {
	Fire(context.Context, *T, ...rulite.FireOption) (rulite.Result, error)
}, input *T, options ...rulite.FireOption) (rulite.Result, error) {
	if a == nil || a.tracer == nil {
		return rulite.Result{}, ErrInvalidAdapter
	}
	if executor == nil {
		return rulite.Result{}, ErrInvalidExecutor
	}
	s := &executionObserver{adapter: a}
	defer s.cleanup()
	callOptions := make([]rulite.FireOption, len(options)+1)
	copy(callOptions, options)
	callOptions[len(options)] = rulite.WithObserver(s)
	return executor.Fire(ctx, input, callOptions...)
}

type executionObserver struct {
	adapter                    *Adapter
	span                       trace.Span
	ctx                        context.Context
	started                    time.Time
	emitted, dropped, filtered int
	closed                     bool
}

func (s *executionObserver) Observe(ctx context.Context, event rulite.Event) error {
	switch event.Kind() {
	case rulite.EventExecutionStarted:
		if s.adapter.duration.Enabled(ctx) {
			s.started = time.Now()
		}
		s.ctx, s.span = s.adapter.tracer.Start(ctx, "rulite.fire", trace.WithSpanKind(trace.SpanKindInternal))
		if s.span.IsRecording() {
			s.span.SetAttributes(attribute.String("rulite.snapshot.revision", strconv.FormatUint(uint64(event.Snapshot().Revision()), 10)))
			if s.adapter.config.traceVersions {
				version := string(event.Snapshot().Version())
				if validText(version) {
					s.span.SetAttributes(attribute.String("rulite.ruleset.version", version))
				} else {
					s.span.SetAttributes(attribute.Bool("rulite.ruleset.version.omitted", true))
				}
			}
		}
	case rulite.EventExecutionFinished:
		return s.finish(event)
	case rulite.EventRuleEvaluated, rulite.EventRuleMatched, rulite.EventRuleFired, rulite.EventRuleFailed:
		s.rule(event)
	}
	return nil
}

func (s *executionObserver) rule(event rulite.Event) {
	a := s.adapter
	rule, _ := event.Rule()
	if sets, ok := a.rules[rule.ID()]; ok && a.ruleCounter.Enabled(s.ctx) {
		index := 0
		switch event.Kind() {
		case rulite.EventRuleMatched:
			index = 1
		case rulite.EventRuleFired:
			index = 2
		case rulite.EventRuleFailed:
			index = 3
		}
		a.ruleCounter.Add(s.ctx, 1, sets[index])
	}
	if !s.span.IsRecording() {
		return
	}
	if a.config.traceRules != nil {
		if _, ok := a.config.traceRules[string(rule.ID())]; !ok {
			s.filtered++
			return
		}
	}
	if s.emitted >= a.config.eventLimit {
		s.dropped++
		return
	}
	phase, _ := event.Phase()
	phaseName := "condition"
	if phase == rulite.ActionPhase {
		phaseName = "action"
	}
	attrs := make([]attribute.KeyValue, 3, 5)
	attrs[0] = attribute.Int64("rulite.rule.priority", int64(rule.Priority()))
	attrs[1] = attribute.Int("rulite.rule.order", rule.Order())
	attrs[2] = attribute.String("rulite.rule.phase", phaseName)
	if !a.config.hideRuleIDs {
		attrs = append(attrs, attribute.String("rulite.rule.id", string(rule.ID())))
	}
	if outcome, ok := event.ConditionOutcome(); ok {
		name := "error"
		switch outcome {
		case rulite.ConditionOutcomeFalse:
			name = "false"
		case rulite.ConditionOutcomeTrue:
			name = "true"
		}
		attrs = append(attrs, attribute.String("rulite.condition.outcome", name))
	}
	s.span.AddEvent(event.Kind().String(), trace.WithAttributes(attrs...))
	s.emitted++
}

func (s *executionObserver) finish(event rulite.Event) error {
	a := s.adapter
	var duration time.Duration
	if !s.started.IsZero() {
		duration = time.Since(s.started)
	}
	counts, _ := event.Counts()
	sets, ok := a.versions[event.Snapshot().Version()]
	if !ok {
		sets = a.fallback
	}
	attrs := sets[event.StopReason()]
	for i, value := range [...]int{1, counts.Evaluated, counts.Matched, counts.Fired, counts.Failed} {
		if value != 0 && a.counters[i].Enabled(s.ctx) {
			a.counters[i].Add(s.ctx, int64(value), attrs)
		}
	}
	if !s.started.IsZero() {
		a.duration.Record(s.ctx, duration.Seconds(), attrs)
	}
	if s.span.IsRecording() {
		s.span.SetAttributes(
			attribute.String("rulite.stop.reason", event.StopReason().String()),
			attribute.Int("rulite.rules.total", counts.Total),
			attribute.Int("rulite.rules.evaluated", counts.Evaluated),
			attribute.Int("rulite.rules.matched", counts.Matched),
			attribute.Int("rulite.rules.fired", counts.Fired),
			attribute.Int("rulite.rules.failed", counts.Failed),
			attribute.Int("rulite.events.recorded", s.emitted),
			attribute.Int("rulite.events.dropped", s.dropped),
			attribute.Int("rulite.events.filtered", s.filtered),
		)
		if counts.Failed != 0 || event.StopReason() == rulite.StopContextCanceled || event.StopReason() == rulite.StopContextDeadlineExceeded {
			s.span.SetStatus(codes.Error, "execution failed or canceled")
		}
	}
	s.closed = true
	s.span.End()
	if s.dropped > 0 {
		return ErrEventLimit
	}
	return nil
}

func (s *executionObserver) cleanup() {
	if s.span == nil || s.closed {
		return
	}
	s.closed = true
	// Cleanup cannot replace a recovered diagnostic or an original propagated panic.
	defer func() { _ = recover() }()
	defer s.span.End()
	s.span.SetAttributes(attribute.Bool("rulite.observation.incomplete", true))
	s.span.SetStatus(codes.Error, "observation incomplete")
}

var _ rulite.Observer = (*executionObserver)(nil)
