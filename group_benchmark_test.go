package rulite_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func BenchmarkFireGroups(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
			for _, scenario := range []string{"start", "middle", "end", "all_miss", "action_error"} {
				b.Run(fmt.Sprintf("%s/%s/members_%d", kind, scenario, size), func(b *testing.B) {
					selected := 0
					if scenario == "middle" {
						selected = size / 2
					}
					if scenario == "end" {
						selected = size - 1
					}
					if scenario == "all_miss" {
						selected = size
					}
					members := benchmarkRules(size, func(i int) byte {
						if scenario == "action_error" && i == 0 {
							return 3
						}
						if i < selected {
							return 0
						}
						return 1
					})
					group := rulite.FirstMatchGroup("providers", members...)
					if kind == rulite.GroupFirstFire {
						group = rulite.FirstFireGroup("providers", members...)
					}
					audit := rulite.NewRule[benchmarkInput]("audit").When(rulite.All[benchmarkInput]()).Then(func(_ context.Context, input *benchmarkInput) error { input.actions++; return nil })
					engine := mustReuse(b, mustEntries(b, group.Entry(), audit.Entry()), rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError)))
					want := benchmarkExpectation{stop: rulite.StopCompleted, actions: 2, counts: rulite.Counts{Total: size + 1, Evaluated: selected + 2, NotEvaluated: size - selected - 1, Unmatched: selected, Matched: 2, Fired: 2}}
					groupState := rulite.GroupResolved
					if scenario == "all_miss" {
						want = benchmarkCompleted(size+1, 1)
						groupState = rulite.GroupExhausted
					}
					if scenario == "action_error" {
						want.hasError = true
						want.counts.Failed, want.counts.ActionFailed = 1, 1
						want.counts.Fired = 1
						if kind == rulite.GroupFirstFire {
							want.counts.Evaluated++
							want.counts.NotEvaluated--
							want.counts.Matched++
							want.counts.Fired++
							want.actions++
						}
					}
					input := benchmarkInput{}
					result, err := engine.Fire(context.Background(), &input)
					checkBenchmark(b, result, err, input, want)
					checkResultConsistency(b, result)
					b.ReportAllocs()
					for b.Loop() {
						input = benchmarkInput{}
						result, err = engine.Fire(context.Background(), &input)
						view, ok := result.Group("providers")
						if !ok || view.State() != groupState || result.Counts() != want.counts || result.StopReason() != want.stop || (err != nil) != want.hasError || input.actions != want.actions {
							b.Fatal("group workload changed")
						}
					}
					benchmarkResult, benchmarkCounter = result, input.actions
					b.ReportMetric(float64(b.N)*float64(want.counts.Evaluated)/b.Elapsed().Seconds(), "evaluated/s")
				})
			}
		}
	}
}

func BenchmarkCompileEntries(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		for _, groups := range []int{1, 10, 100} {
			if groups > size {
				continue
			}
			b.Run(fmt.Sprintf("groups_%d/rules_%d", groups, size), func(b *testing.B) {
				rules := metadataBenchmarkRules(size)
				entries := make([]rulite.Entry[benchmarkInput], groups)
				for i := range entries {
					entries[i] = rulite.FirstFireGroup(rulite.GroupID(fmt.Sprintf("group/%d", i)), rules[i*size/groups:(i+1)*size/groups]...).WithPriority(rulite.Priority(i % 3)).Entry()
				}
				var set *rulite.RuleSet[benchmarkInput]
				var err error
				b.ReportAllocs()
				for b.Loop() {
					set, err = rulite.CompileEntries(entries...)
					if err != nil || set.Len() != size {
						b.Fatal("incomplete grouped compilation")
					}
				}
				benchmarkSet = set
				input := benchmarkInput{}
				result, err := mustReuse(b, set).Fire(context.Background(), &input)
				if err != nil || result.Counts().Fired != groups || result.Evaluated() != groups || input.actions != groups {
					b.Fatal("compiled group selection changed")
				}
				checkResultConsistency(b, result)
			})
		}
	}
}
