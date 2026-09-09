package otel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

var benchmarkResult rulite.Result
var benchmarkCompletions int

type checkingProcessor struct {
	ends, events int
	invalid      bool
}

func (*checkingProcessor) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (p *checkingProcessor) OnEnd(s sdktrace.ReadOnlySpan) {
	p.ends++
	if len(s.Events()) != p.events || attr(s.Attributes(), "rulite.snapshot.revision").AsString() != "0" || attr(s.Attributes(), "rulite.ruleset.version").AsString() != "bench" {
		p.invalid = true
	}
}
func (*checkingProcessor) Shutdown(context.Context) error   { return nil }
func (*checkingProcessor) ForceFlush(context.Context) error { return nil }

func BenchmarkFireTelemetry(b *testing.B) {
	for _, matches := range []int{0, 10} {
		for _, mode := range []string{"absent", "noop_observer", "otel_noop", "sdk_not_recording", "metrics_only", "trace_recording", "trace_limit_4", "in_memory_exporter"} {
			b.Run(fmt.Sprintf("%s/matches_%d/rules_100", mode, matches), func(b *testing.B) {
				ctx := context.Background()
				outcomes := make([]byte, 100)
				for i := range matches {
					outcomes[i*10] = 1
				}
				e := engine(b, "bench", rules(outcomes, nil)...)
				var traces trace.TracerProvider = tracenoop.NewTracerProvider()
				var metrics metric.MeterProvider = metricnoop.NewMeterProvider()
				var reader *sdkmetric.ManualReader
				var exporter *tracetest.InMemoryExporter
				processor := &checkingProcessor{events: 100 + 2*matches}
				options := []ruliteotel.Option{ruliteotel.WithTraceVersions()}
				if mode == "trace_limit_4" {
					options = append(options, ruliteotel.WithEventLimit(4))
					processor.events = 4
				}
				if mode == "metrics_only" || mode == "sdk_not_recording" {
					reader = sdkmetric.NewManualReader()
					opts := []sdkmetric.Option{sdkmetric.WithResource(resource.Empty()), sdkmetric.WithReader(reader)}
					if mode == "sdk_not_recording" {
						opts = append(opts, sdkmetric.WithView(sdkmetric.NewView(sdkmetric.Instrument{Name: "*"}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}})))
					}
					mp := sdkmetric.NewMeterProvider(opts...)
					b.Cleanup(func() {
						if err := mp.Shutdown(ctx); err != nil {
							b.Error(err)
						}
					})
					metrics = mp
				}
				if mode == "sdk_not_recording" || mode == "trace_recording" || mode == "trace_limit_4" || mode == "in_memory_exporter" {
					opts := []sdktrace.TracerProviderOption{sdktrace.WithResource(resource.Empty())}
					if mode == "sdk_not_recording" {
						opts = append(opts, sdktrace.WithSampler(sdktrace.NeverSample()))
					} else {
						opts = append(opts, sdktrace.WithSpanProcessor(processor))
					}
					if mode == "in_memory_exporter" {
						exporter = tracetest.NewInMemoryExporter()
						opts = append(opts, sdktrace.WithSyncer(exporter))
					}
					tp := sdktrace.NewTracerProvider(opts...)
					b.Cleanup(func() {
						if err := tp.Shutdown(ctx); err != nil {
							b.Error(err)
						}
					})
					traces = tp
				}
				a, err := ruliteotel.New(traces, metrics, options...)
				if err != nil {
					b.Fatal(err)
				}
				var result rulite.Result
				input := state{}
				calls := 0
				noop := rulite.WithObserver(rulite.ObserverFunc(func(context.Context, rulite.Event) error { return nil }))
				b.ReportAllocs()
				for b.Loop() {
					input = state{}
					if exporter != nil {
						exporter.Reset()
					}
					switch mode {
					case "absent":
						result, err = e.Fire(ctx, &input)
					case "noop_observer":
						result, err = e.Fire(ctx, &input, noop)
					default:
						result, err = a.Fire(ctx, e, &input)
					}
					calls++
					wantDiagnostics := 0
					if mode == "trace_limit_4" {
						wantDiagnostics = 1
					}
					if err != nil || input.value != matches || result.Counts().Evaluated != 100 || result.Counts().Fired != matches || result.StopReason() != rulite.StopCompleted || result.Snapshot().Version() != "bench" || result.Snapshot().Revision() != 0 || len(result.Diagnostics()) != wantDiagnostics {
						b.Fatal("telemetry workload changed")
					}
				}
				wantEnds := 0
				if mode == "trace_recording" || mode == "trace_limit_4" || mode == "in_memory_exporter" {
					wantEnds = calls
				}
				if processor.invalid || processor.ends != wantEnds {
					b.Fatal("span output changed")
				}
				if exporter != nil {
					spans := exporter.GetSpans()
					if len(spans) != 1 || len(spans[0].Events) != processor.events {
						b.Fatal("exporter output changed")
					}
				}
				if reader != nil {
					var data metricdata.ResourceMetrics
					if err := reader.Collect(ctx, &data); err != nil {
						b.Fatal(err)
					}
					var count int64
					for _, scope := range data.ScopeMetrics {
						for _, m := range scope.Metrics {
							if m.Name == "rulite.execution.count" {
								for _, p := range m.Data.(metricdata.Sum[int64]).DataPoints {
									count += p.Value
								}
							}
						}
					}
					want := int64(calls)
					if mode == "sdk_not_recording" {
						want = 0
					}
					if count != want {
						b.Fatal("metric output changed")
					}
				}
				benchmarkResult, benchmarkCompletions = result, calls
			})
		}
	}
}
