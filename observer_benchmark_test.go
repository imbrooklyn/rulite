package rulite_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
)

var benchmarkEvents []rulite.Event

func BenchmarkFireObserver(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		for _, scenario := range []string{"all_miss", "ten_percent"} {
			for _, mode := range []string{"none", "noop", "collect", "first_error", "first_panic", "trace_none", "trace_noop", "trace_collect"} {
				b.Run(fmt.Sprintf("%s/%s/rules_%d", mode, scenario, size), func(b *testing.B) {
					matches := 0
					if scenario == "ten_percent" {
						matches = size / 10
					}
					want := benchmarkCompleted(size, matches)
					set := mustCompile(b, benchmarkRules(size, func(i int) byte {
						if matches != 0 && i%10 == 0 {
							return 1
						}
						return 0
					})...)
					calls, wantCalls := 0, 2+size+2*matches
					wantDiagnostics := 0
					collect := mode == "collect" || mode == "trace_collect"
					var events []rulite.Event
					if collect {
						events = make([]rulite.Event, 0, wantCalls)
					}
					var options []rulite.FireOption
					if mode == "trace_none" || mode == "trace_noop" || mode == "trace_collect" {
						options = append(options, rulite.WithTrace())
					}
					if mode == "none" || mode == "trace_none" {
						wantCalls = 0
					} else {
						if mode == "first_error" || mode == "first_panic" {
							wantCalls, wantDiagnostics = 1, 1
						}
						options = append(options, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
							calls++
							if collect {
								events = append(events, event)
							}
							if mode == "first_error" {
								return benchmarkError
							}
							if mode == "first_panic" {
								panic("export unavailable")
							}
							return nil
						})))
					}
					engine := mustReuse(b, set, options...)
					input := benchmarkInput{}
					result, err := engine.Fire(context.Background(), &input)
					checkBenchmark(b, result, err, input, want)
					checkResultConsistency(b, result)
					check := func() {
						if result.Counts() != want.counts || result.StopReason() != want.stop || err != nil || input.actions != want.actions || calls != wantCalls || len(result.Diagnostics()) != wantDiagnostics || collect && len(events) != wantCalls {
							b.Fatal("observation workload changed")
						}
					}
					check()
					b.ReportAllocs()
					for b.Loop() {
						input, calls, events = benchmarkInput{}, 0, events[:0]
						result, err = engine.Fire(context.Background(), &input)
						// Counts and defensive diagnostic reads are included in timing.
						check()
					}
					benchmarkResult, benchmarkCounter, benchmarkEvents = result, calls, events
				})
			}
		}
	}
}
