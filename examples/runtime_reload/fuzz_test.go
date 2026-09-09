package main

import (
	"context"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func FuzzReloadPublications(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 255})
	f.Add([]byte{1, 1, 8, 0, 9, 1, 3, 0})
	l := load(f, localAttempt)
	valid := [][]byte{fixture(f, "v1"), fixture(f, "v2")}
	var invalid [][]byte
	for _, name := range invalidFixtures {
		invalid = append(invalid, fixture(f, name))
	}
	f.Fuzz(func(t *testing.T, operations []byte) {
		// Bound compilation, retained telemetry, and sequential operations. The
		// oracle describes accepted fixtures, not nondeterministic scheduling.
		if len(operations) > 16 {
			operations = operations[:16]
		}
		runtime := initialRuntime(t, l)
		x := telemetry(t, "otel_limit")
		revision, generation := rulite.SnapshotRevision(1), 0
		var results []rulite.Result
		for _, operation := range operations {
			before := runtime.Snapshot()
			index := int(operation) % 10
			if index < 2 {
				generation = index
				version := []rulite.RuleSetVersion{"payments/v1", "payments/v2"}[generation]
				info, err := l.reload(runtime, valid[generation], version)
				revision++
				if err != nil || info.Revision() != revision {
					t.Fatal("successful publication sequence changed")
				}
			} else {
				var err error
				if index == 9 {
					_, err = runtime.Publish(nil)
				} else {
					_, err = l.reload(runtime, invalid[index-2], "rejected")
				}
				if err == nil || runtime.Snapshot() != before {
					t.Fatal("failed construction altered availability")
				}
			}
			out := execute(context.Background(), runtime, x, "otel_limit", payment{AmountMinor: 100})
			checkFallback(t, out)
			if out.result.Snapshot().Revision() != revision || out.result.Snapshot().Version() != []rulite.RuleSetVersion{"payments/v1", "payments/v2"}[generation] {
				t.Fatal("version oracle failed")
			}
			results = append(results, out.result)
			// The Observer path sees the same complete snapshot and ordered rules.
			observed := execute(context.Background(), runtime, observation{}, "observer", payment{AmountMinor: 100})
			checkFallback(t, observed)
			if observed.result.Snapshot() != out.result.Snapshot() {
				t.Fatal("stable publication identity diverged")
			}
			want := ruleEvents(observed.result)
			var seen []string
			for _, event := range observed.events {
				if rule, ok := event.Rule(); ok {
					seen = append(seen, string(rule.ID())+":"+event.Kind().String())
				}
			}
			if len(seen) != len(want) {
				t.Fatal("rule event count diverged")
			}
			for i := range want {
				if seen[i] != want[i] {
					t.Fatal("event sequence oracle failed")
				}
			}
		}
		spans := x.exporter.GetSpans()
		if len(spans) != len(results) {
			t.Fatal("execution lifecycle lost")
		}
		for i, s := range spans {
			checkSpan(t, s, results[i], 2)
		}
		if len(results) != 0 {
			checkMetrics(t, x, results)
		}
	})
}
