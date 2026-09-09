package main

import (
	"context"
	"errors"
	"testing"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

var retainedResult rulite.Result
var retainedEngine *rulite.Engine[payment]

func BenchmarkReloadCompile(b *testing.B) {
	l, source := load(b, localAttempt), fixture(b, "v1")
	var e *rulite.Engine[payment]
	var err error
	b.ReportAllocs()
	for b.Loop() {
		e, err = l.compile(source, "payments/v1")
		if err != nil {
			b.Fatal(err)
		}
	}
	retainedEngine = e
	runtime, err := rulite.NewRuntime(e)
	if err != nil {
		b.Fatal(err)
	}
	checkFallback(b, execute(context.Background(), runtime, observation{}, "observer", payment{AmountMinor: 100}))
}

func BenchmarkReloadFire(b *testing.B) {
	for _, mode := range []string{"engine", "runtime", "observer", "otel_noop", "otel_limit"} {
		b.Run(mode, func(b *testing.B) {
			l, source := load(b, localAttempt), fixture(b, "v1")
			e, err := l.compile(source, "payments/v1")
			if err != nil {
				b.Fatal(err)
			}
			runtime, err := rulite.NewRuntime(e)
			if err != nil {
				b.Fatal(err)
			}
			fire := runtime.Fire
			revision := rulite.SnapshotRevision(1)
			if mode == "engine" {
				fire, revision = e.Fire, 0
			}
			options := []rulite.FireOption(nil)
			events := 0
			if mode == "observer" {
				options = append(options, rulite.WithObserver(rulite.ObserverFunc(func(context.Context, rulite.Event) error { events++; return nil })))
			}
			var a *ruliteotel.Adapter
			x := observation{}
			if mode == "otel_noop" {
				a, err = ruliteotel.New(tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider())
				if err != nil {
					b.Fatal(err)
				}
			}
			if mode == "otel_limit" {
				x = telemetry(b, mode)
				a = x.adapter
			}
			want := rulite.Counts{Total: 4, Evaluated: 3, NotEvaluated: 1, Matched: 3, Fired: 2, Failed: 1, ActionFailed: 1}
			var r rulite.Result
			var input payment
			b.ReportAllocs()
			for b.Loop() {
				input = payment{AmountMinor: 100}
				events = 0
				if mode == "otel_limit" {
					x.exporter.Reset()
				}
				if a != nil {
					r, err = a.Fire(context.Background(), runtime, &input)
				} else {
					r, err = fire(context.Background(), &input, options...)
				}
				diagnostics := 0
				if mode == "otel_limit" {
					diagnostics = 1
				}
				if !errors.Is(err, errUnavailable) || r.Counts() != want || r.StopReason() != rulite.StopCompleted || r.Snapshot().Revision() != revision || r.Snapshot().Version() != "payments/v1" || input.Provider != "secondary/v1" || input.AuditVersion != "payments/v1" || input.AppliedBy != "payment/secondary" || len(input.Attempts) != 2 || len(r.Diagnostics()) != diagnostics || mode == "observer" && events != 13 {
					b.Fatal("joint workload changed")
				}
			}
			retainedResult = r
			if mode == "otel_limit" {
				spans := x.exporter.GetSpans()
				if len(spans) != 1 {
					b.Fatal("exporter retention unbounded")
				}
				checkSpan(b, spans[0], r, 2)
			}
		})
	}
}
