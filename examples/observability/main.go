package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type price struct {
	discount  int
	appliedBy rulite.RuleID
}

func run(ctx context.Context, output io.Writer) (err error) {
	// These providers record only in memory and open no network connection.
	reader := sdkmetric.NewManualReader()
	exporter := tracetest.NewInMemoryExporter()
	metrics := sdkmetric.NewMeterProvider(sdkmetric.WithResource(resource.Empty()), sdkmetric.WithReader(reader))
	traces := sdktrace.NewTracerProvider(sdktrace.WithResource(resource.Empty()), sdktrace.WithSyncer(exporter))
	defer func() {
		err = errors.Join(err, traces.Shutdown(context.Background()), metrics.Shutdown(context.Background()))
	}()
	adapter, err := ruliteotel.New(traces, metrics, ruliteotel.WithTraceVersions(), ruliteotel.WithEventLimit(16))
	if err != nil {
		return err
	}
	rule := rulite.NewRule[price]("pricing/offer").
		When(func(context.Context, *price) (bool, error) { return true, nil }).
		Then(func(_ context.Context, p *price) error { p.discount = 20; p.appliedBy = "pricing/offer"; return nil })
	set, err := rulite.Compile(rule)
	if err != nil {
		return err
	}
	set, err = set.WithIdentity("pricing/v1", "")
	if err != nil {
		return err
	}
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		return err
	}
	runtime, err := rulite.NewRuntime(engine)
	if err != nil {
		return err
	}
	input := price{}
	result, err := adapter.Fire(ctx, runtime, &input)
	if err != nil {
		return err
	}
	// Business facts and export diagnostics are inspected independently.
	if _, err := fmt.Fprintf(output, "Discount: %d%%; applied by: %s; version: %s; revision: %d\n", input.discount, input.appliedBy, result.Snapshot().Version(), result.Snapshot().Revision()); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(output, "Fired: %d; telemetry diagnostics: %d\n", result.Counts().Fired, len(result.Diagnostics())); err != nil {
		return err
	}
	if err := traces.ForceFlush(ctx); err != nil {
		return err
	}
	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		return err
	}
	var executions int64
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name == "rulite.execution.count" {
				for _, point := range m.Data.(metricdata.Sum[int64]).DataPoints {
					executions += point.Value
				}
			}
		}
	}
	spans := exporter.GetSpans()
	events := 0
	for _, span := range spans {
		events += len(span.Events)
	}
	_, err = fmt.Fprintf(output, "Finished executions: %d; spans: %d; rule events: %d\n", executions, len(spans), events)
	return err
}

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
