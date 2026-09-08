package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imbrooklyn/rulite"
)

type benchmarkInput struct{ key, actions int }
type benchmarkExpectation struct {
	counts   rulite.Counts
	stop     rulite.StopReason
	actions  int
	hasError bool
}

var (
	benchmarkResult  rulite.Result
	benchmarkEngine  *rulite.Engine[benchmarkInput]
	benchmarkText    string
	benchmarkRule    rulite.RuleExecution
	benchmarkCounter int
	benchmarkError   = errors.New("provider unavailable")
)

// Outcomes: 0 miss, 1 fire, 2 condition error, 3 action error, 4 panic.
func benchmarkRules(size int, outcome func(int) byte) []rulite.Rule[benchmarkInput] {
	rules := make([]rulite.Rule[benchmarkInput], size)
	for i := range rules {
		kind := outcome(i)
		rules[i] = rulite.NewRule[benchmarkInput](rulite.RuleID(fmt.Sprintf("rule/%d", i))).When(func(_ context.Context, input *benchmarkInput) (bool, error) {
			if kind == 2 {
				return true, benchmarkError
			}
			if kind == 4 {
				panic("condition unavailable")
			}
			return kind != 0 && input.key == 0, nil
		}).Then(func(_ context.Context, input *benchmarkInput) error {
			input.actions++
			if kind == 3 {
				return benchmarkError
			}
			return nil
		})
	}
	return rules
}

func benchmarkCompleted(size, matched int) benchmarkExpectation {
	return benchmarkExpectation{counts: rulite.Counts{Total: size, Evaluated: size, Unmatched: size - matched, Matched: matched, Fired: matched}, stop: rulite.StopCompleted, actions: matched}
}

func checkBenchmark(b *testing.B, result rulite.Result, err error, input benchmarkInput, want benchmarkExpectation) {
	b.Helper()
	if result.Counts() != want.counts || result.StopReason() != want.stop || (err != nil) != want.hasError || input.actions != want.actions {
		b.Fatalf("counts=%+v stop=%v actions=%d error=%v; want %+v", result.Counts(), result.StopReason(), input.actions, err, want)
	}
}

func benchmarkFire(b *testing.B, ctx context.Context, rules []rulite.Rule[benchmarkInput], want benchmarkExpectation, options ...rulite.FireOption) {
	engine := mustEngine(b, rules...)
	input := benchmarkInput{}
	result, err := engine.Fire(ctx, &input, options...)
	checkBenchmark(b, result, err, input, want)
	checkResultConsistency(b, result)
	b.ReportAllocs()
	for b.Loop() {
		input = benchmarkInput{}
		result, err = engine.Fire(ctx, &input, options...)
		// These inexpensive assertions are included in the reported time.
		if result.Counts() != want.counts || result.StopReason() != want.stop || (err != nil) != want.hasError || input.actions != want.actions {
			b.Fatal("execution changed during benchmark")
		}
	}
	benchmarkResult = result
	benchmarkCounter = input.actions
	if want.counts.Evaluated > 0 {
		b.ReportMetric(float64(b.N)*float64(want.counts.Evaluated)/b.Elapsed().Seconds(), "evaluated/s")
	}
}

func BenchmarkBuildEngine(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("rules_%d", size), func(b *testing.B) {
			rules := benchmarkRules(size, func(int) byte { return 1 })
			// Mixed priorities exercise sorting; equal priorities still retain registration order.
			for i := range rules {
				rules[i] = rulite.NewRule[benchmarkInput](rules[i].ID()).Priority(rulite.Priority((i*37)%101 - 50)).When(func(context.Context, *benchmarkInput) (bool, error) { return true, nil }).Then(func(_ context.Context, input *benchmarkInput) error { input.actions++; return nil })
			}
			var engine *rulite.Engine[benchmarkInput]
			var err error
			b.ReportAllocs()
			for b.Loop() {
				engine, err = rulite.NewEngine(rules...)
				if err != nil {
					b.Fatal(err)
				}
			}
			benchmarkEngine = engine
			input := benchmarkInput{}
			result, err := engine.Fire(context.Background(), &input)
			checkBenchmark(b, result, err, input, benchmarkCompleted(size, size))
			checkResultConsistency(b, result)
		})
	}
}

func BenchmarkFireScale(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		for _, scenario := range []string{"all_miss", "all_match", "ten_percent"} {
			if size == 10 && scenario == "ten_percent" {
				continue
			}
			b.Run(fmt.Sprintf("%s/rules_%d", scenario, size), func(b *testing.B) {
				matches := 0
				rules := benchmarkRules(size, func(i int) byte {
					if scenario == "all_match" || scenario == "ten_percent" && i%10 == 0 {
						matches++
						return 1
					}
					return 0
				})
				benchmarkFire(b, context.Background(), rules, benchmarkCompleted(size, matches))
			})
		}
	}
}

func BenchmarkFireSelection(b *testing.B) {
	const size = 1000
	for _, mode := range []rulite.StopMode{rulite.StopOnFirstMatch, rulite.StopOnFirstFire} {
		for _, position := range []struct {
			name  string
			index int
		}{{"start", 0}, {"middle", size / 2}, {"end", size - 1}} {
			name := "first_match"
			stop := rulite.StopFirstMatch
			if mode == rulite.StopOnFirstFire {
				name, stop = "first_fire", rulite.StopFirstFire
			}
			b.Run(name+"/"+position.name, func(b *testing.B) {
				rules := benchmarkRules(size, func(i int) byte {
					if i == position.index {
						return 1
					}
					return 0
				})
				want := benchmarkExpectation{counts: rulite.Counts{Total: size, Evaluated: position.index + 1, NotEvaluated: size - position.index - 1, Unmatched: position.index, Matched: 1, Fired: 1}, stop: stop, actions: 1}
				benchmarkFire(b, context.Background(), rules, want, rulite.WithPolicy(rulite.DefaultPolicy().WithStop(mode)))
			})
		}
	}
	b.Run("first_fire/action_error_fallback", func(b *testing.B) {
		const failed = 10
		rules := benchmarkRules(size, func(i int) byte {
			if i < failed {
				return 3
			}
			return 1
		})
		want := benchmarkExpectation{counts: rulite.Counts{Total: size, Evaluated: failed + 1, NotEvaluated: size - failed - 1, Matched: failed + 1, Fired: 1, Failed: failed, ActionFailed: failed}, stop: rulite.StopFirstFire, actions: failed + 1, hasError: true}
		benchmarkFire(b, context.Background(), rules, want, rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire).WithActionErrors(rulite.ContinueOnError)))
	})
}

func BenchmarkFireCombinators(b *testing.B) {
	for _, name := range []string{"all_true", "all_short_circuit", "any_false", "any_short_circuit", "nested"} {
		b.Run(name, func(b *testing.B) {
			calls := 0
			yes := func(_ context.Context, input *benchmarkInput) (bool, error) { calls++; return input.key == 0, nil }
			no := func(_ context.Context, input *benchmarkInput) (bool, error) { calls++; return input.key != 0, nil }
			var condition rulite.Condition[benchmarkInput]
			wantCalls, matches := 3, 0
			switch name {
			case "all_true":
				condition, matches = rulite.All(yes, yes, yes), 1
			case "all_short_circuit":
				condition, wantCalls = rulite.All(no, yes, yes), 1
			case "any_false":
				condition = rulite.Any(no, no, no)
			case "any_short_circuit":
				condition, wantCalls, matches = rulite.Any(yes, no, no), 1, 1
			case "nested":
				condition, matches = rulite.All(yes, rulite.Any(no, rulite.Not(no))), 1
			}
			input := benchmarkInput{}
			got, err := condition(context.Background(), &input)
			if err != nil || got != (matches == 1) || calls != wantCalls {
				b.Fatal("combinator did not follow short-circuit semantics")
			}
			rules := []rulite.Rule[benchmarkInput]{rulite.NewRule[benchmarkInput]("combined").When(condition).Then(func(_ context.Context, input *benchmarkInput) error { input.actions++; return nil })}
			benchmarkFire(b, context.Background(), rules, benchmarkCompleted(1, matches))
			benchmarkCounter = calls
		})
	}
}

func BenchmarkFireErrors(b *testing.B) {
	const size = 100
	for _, phase := range []string{"condition", "action"} {
		for _, mode := range []rulite.ErrorMode{rulite.StopOnError, rulite.ContinueOnError} {
			disposition := "stop"
			if mode == rulite.ContinueOnError {
				disposition = "continue"
			}
			b.Run(phase+"/"+disposition, func(b *testing.B) {
				kind, stop := byte(2), rulite.StopConditionError
				if phase == "action" {
					kind, stop = 3, rulite.StopActionError
				}
				evaluated := 1
				if mode == rulite.ContinueOnError {
					evaluated, stop = size, rulite.StopCompleted
				}
				want := benchmarkExpectation{counts: rulite.Counts{Total: size, Evaluated: evaluated, NotEvaluated: size - evaluated, Failed: evaluated}, stop: stop, hasError: true}
				if phase == "condition" {
					want.counts.ConditionFailed = evaluated
				} else {
					want.counts.ActionFailed, want.counts.Matched, want.actions = evaluated, evaluated, evaluated
				}
				benchmarkFire(b, context.Background(), benchmarkRules(size, func(int) byte { return kind }), want, rulite.WithPolicy(rulite.DefaultPolicy().WithConditionErrors(mode).WithActionErrors(mode)))
			})
		}
	}
	b.Run("panic_recover", func(b *testing.B) {
		benchmarkFire(b, context.Background(), benchmarkRules(size, func(int) byte { return 4 }), benchmarkExpectation{counts: rulite.Counts{Total: size, Evaluated: 1, NotEvaluated: size - 1, Failed: 1, ConditionFailed: 1, PanicRecovered: 1}, stop: rulite.StopPanic, hasError: true})
	})
	b.Run("context_already_canceled", func(b *testing.B) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		benchmarkFire(b, ctx, benchmarkRules(size, func(int) byte { return 1 }), benchmarkExpectation{counts: rulite.Counts{Total: size, NotEvaluated: size}, stop: rulite.StopContextCanceled, hasError: true})
	})
}

func BenchmarkFireDiagnostics(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		for _, matched := range []bool{false, true} {
			scenario := "all_miss"
			matches := 0
			if matched {
				scenario, matches = "ten_percent", size/10
			}
			for _, trace := range []bool{false, true} {
				mode := "summary"
				var options []rulite.FireOption
				if trace {
					mode = "trace"
					options = append(options, rulite.WithTrace())
				}
				b.Run(fmt.Sprintf("%s/%s/rules_%d", mode, scenario, size), func(b *testing.B) {
					rules := benchmarkRules(size, func(i int) byte {
						if matched && i%10 == 0 {
							return 1
						}
						return 0
					})
					benchmarkFire(b, context.Background(), rules, benchmarkCompleted(size, matches), options...)
				})
			}
		}
	}
}

func BenchmarkExplainOnDemand(b *testing.B) {
	input := benchmarkInput{}
	engine := mustEngine(b, benchmarkRules(100, func(i int) byte {
		if i%10 == 0 {
			return 1
		}
		return 0
	})...)
	result, err := engine.Fire(context.Background(), &input)
	checkBenchmark(b, result, err, input, benchmarkCompleted(100, 10))
	checkResultConsistency(b, result)
	b.Run("rules_100/structural", func(b *testing.B) {
		var views []rulite.RuleExecution
		b.ReportAllocs()
		for b.Loop() {
			views = result.Explain().Rules()
			if len(views) != 100 {
				b.Fatal("incomplete explanation")
			}
		}
		benchmarkRule = views[99]
	})
	b.Run("rules_100/text", func(b *testing.B) {
		var text string
		b.ReportAllocs()
		for b.Loop() {
			text = result.Explain().String()
			if text == "" {
				b.Fatal("empty explanation")
			}
		}
		benchmarkText = text
	})
}

func BenchmarkResultRuleLookup(b *testing.B) {
	input := benchmarkInput{}
	engine := mustEngine(b, benchmarkRules(1000, func(int) byte { return 1 })...)
	result, err := engine.Fire(context.Background(), &input)
	checkBenchmark(b, result, err, input, benchmarkCompleted(1000, 1000))
	for _, id := range []rulite.RuleID{"rule/0", "rule/500", "rule/999", "unknown"} {
		b.Run(string(id), func(b *testing.B) {
			var view rulite.RuleExecution
			var ok bool
			b.ReportAllocs()
			for b.Loop() {
				view, ok = result.Rule(id)
				if ok != (id != "unknown") || ok && (view.ID() != id || !view.Fired()) {
					b.Fatal("incorrect lookup")
				}
			}
			benchmarkRule = view
		})
	}
}

func BenchmarkFireParallel(b *testing.B) {
	const size = 1000
	engine := mustEngine(b, benchmarkRules(size, func(i int) byte {
		if i%10 == 0 {
			return 1
		}
		return 0
	})...)
	want := benchmarkCompleted(size, size/10)
	input := benchmarkInput{}
	result, err := engine.Fire(context.Background(), &input)
	checkBenchmark(b, result, err, input, want)
	for _, concurrency := range []int{1, 2, 4, 8, 16, 32} {
		b.Run(fmt.Sprintf("workers_%d", concurrency), func(b *testing.B) {
			// Exactly this many workers share one engine. GOMAXPROCS is unchanged.
			var workers sync.WaitGroup
			var failed atomic.Bool
			totals := make([]int, concurrency)
			b.ReportAllocs()
			b.ResetTimer()
			for worker := range concurrency {
				workers.Go(func() {
					var state benchmarkInput
					total := 0
					for iteration := worker; iteration < b.N; iteration += concurrency {
						state = benchmarkInput{}
						got, err := engine.Fire(context.Background(), &state)
						if got.Counts() != want.counts || got.StopReason() != want.stop || err != nil || state.actions != want.actions {
							failed.Store(true)
						}
						total += state.actions
					}
					totals[worker] = total
				})
			}
			workers.Wait()
			b.StopTimer()
			if failed.Load() {
				b.Fatal("concurrent execution changed semantics")
			}
			total := 0
			for _, count := range totals {
				total += count
			}
			if total != b.N*want.actions {
				b.Fatal("incomplete parallel workload")
			}
			benchmarkCounter = total
			b.ReportMetric(float64(b.N)*size/b.Elapsed().Seconds(), "evaluated/s")
		})
	}
}
