package rulite_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite"
)

var benchmarkEntryViews []rulite.EntryExplanation
var benchmarkTraceRule rulite.RuleTrace

func groupDiagnosticWorkload(b *testing.B, groups, members int, kind rulite.GroupKind, scenario string) (*rulite.RuleSet[benchmarkInput], benchmarkExpectation) {
	b.Helper()
	position := 0
	switch scenario {
	case "middle":
		position = members / 2
	case "end":
		position = members - 1
	case "all_miss":
		position = members
	}
	rules := benchmarkRules(groups*members, func(index int) byte {
		local := index % members
		if scenario == "fallback" && local == 0 {
			return 3
		}
		if local < position {
			return 0
		}
		return 1
	})
	entries := make([]rulite.Entry[benchmarkInput], 0, groups+1)
	for index := range groups {
		id := rulite.GroupID(fmt.Sprintf("group/%d", index))
		group := rulite.FirstMatchGroup(id, rules[index*members:(index+1)*members]...)
		if kind == rulite.GroupFirstFire {
			group = rulite.FirstFireGroup(id, rules[index*members:(index+1)*members]...)
		}
		entries = append(entries, group.Entry())
	}
	entries = append(entries, rulite.NewRule[benchmarkInput]("audit").When(rulite.All[benchmarkInput]()).Then(func(_ context.Context, input *benchmarkInput) error { input.actions++; return nil }).Entry())
	want := benchmarkExpectation{stop: rulite.StopCompleted, actions: groups + 1, counts: rulite.Counts{Total: groups*members + 1, Evaluated: groups*(position+1) + 1, NotEvaluated: groups * (members - position - 1), Unmatched: groups * position, Matched: groups + 1, Fired: groups + 1}}
	if scenario == "all_miss" {
		want = benchmarkCompleted(groups*members+1, 1)
	}
	if scenario == "fallback" {
		want.hasError = true
		want.counts.Failed, want.counts.ActionFailed, want.counts.Fired = groups, groups, 1
		if kind == rulite.GroupFirstFire {
			want.counts.Evaluated += groups
			want.counts.NotEvaluated -= groups
			want.counts.Matched += groups
			want.counts.Fired += groups
			want.actions += groups
		}
	}
	return mustEntries(b, entries...), want
}

func BenchmarkGroupDiagnostics(b *testing.B) {
	for _, shape := range [][2]int{{1, 10}, {1, 1000}, {10, 100}, {10, 1000}, {100, 100}} {
		groups, members := shape[0], shape[1]
		for _, kind := range []rulite.GroupKind{rulite.GroupFirstMatch, rulite.GroupFirstFire} {
			for _, scenario := range []string{"start", "middle", "end", "all_miss", "fallback"} {
				for _, mode := range []string{"summary", "trace", "observer", "trace_observer"} {
					b.Run(fmt.Sprintf("%s/%s/%s/groups_%d/members_%d", kind, scenario, mode, groups, members), func(b *testing.B) {
						set, want := groupDiagnosticWorkload(b, groups, members, kind, scenario)
						options := []rulite.FireOption{rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError))}
						if strings.Contains(mode, "trace") {
							options = append(options, rulite.WithTrace())
						}
						calls, resolved, finished := 0, 0, 0
						wantCalls, wantResolved, wantFinished := 0, 0, 0
						if strings.Contains(mode, "observer") {
							wantFinished, wantResolved = groups, groups
							if scenario == "all_miss" {
								wantResolved = 0
							}
							wantCalls = 2 + want.counts.Evaluated + 2*want.counts.Matched + wantFinished + wantResolved
							options = append(options, rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
								calls++
								if event.Kind() == rulite.EventGroupResolved {
									resolved++
								}
								if event.Kind() == rulite.EventGroupFinished {
									finished++
								}
								return nil
							})))
						}
						engine := mustReuse(b, set, options...)
						input := benchmarkInput{}
						result, err := engine.Fire(context.Background(), &input)
						checkBenchmark(b, result, err, input, want)
						checkResultConsistency(b, result)
						b.ReportAllocs()
						for b.Loop() {
							input, calls, resolved, finished = benchmarkInput{}, 0, 0, 0
							result, err = engine.Fire(context.Background(), &input)
							if result.Counts() != want.counts || result.StopReason() != want.stop || (err != nil) != want.hasError || input.actions != want.actions || calls != wantCalls || resolved != wantResolved || finished != wantFinished {
								b.Fatal("group diagnostic workload changed")
							}
						}
						benchmarkResult, benchmarkCounter = result, calls+input.actions
					})
				}
			}
		}
	}
}

func BenchmarkGroupViews(b *testing.B) {
	for _, shape := range [][2]int{{1, 10}, {1, 1000}, {10, 100}} {
		groups, members := shape[0], shape[1]
		set, want := groupDiagnosticWorkload(b, groups, members, rulite.GroupFirstFire, "fallback")
		input := benchmarkInput{}
		result, err := mustReuse(b, set, rulite.WithTrace(), rulite.WithPolicy(rulite.DefaultPolicy().WithActionErrors(rulite.ContinueOnError))).Fire(context.Background(), &input)
		checkBenchmark(b, result, err, input, want)
		checkResultConsistency(b, result)
		for _, view := range []string{"entries", "text", "trace"} {
			b.Run(fmt.Sprintf("%s/groups_%d/members_%d", view, groups, members), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					switch view {
					case "entries":
						entries := result.Explain().Entries()
						if len(entries) != groups+1 {
							b.Fatal("incomplete top-level views")
						}
						count := 0
						for _, entry := range entries {
							if group, ok := entry.Group(); ok {
								views := entry.Members()
								if group.EndReason() != rulite.GroupEndResolved || len(views) != members {
									b.Fatal("incomplete member views")
								}
								count += len(views)
								benchmarkRule = views[len(views)-1]
							}
						}
						if count != groups*members {
							b.Fatal("lost members")
						}
						benchmarkEntryViews = entries
					case "text":
						text := result.Explain().String()
						if !strings.Contains(text, "rule audit") || !strings.Contains(text, "not-evaluated reason: group-resolved") {
							b.Fatal("incomplete group text")
						}
						benchmarkText = text
					case "trace":
						trace, _ := result.Trace()
						groupViews, rules := trace.Groups(), trace.Rules()
						if len(groupViews) != groups || len(rules) != groups*members+1 || !rules[len(rules)-1].Fired() {
							b.Fatal("incomplete trace views")
						}
						benchmarkTraceRule, benchmarkCounter = rules[len(rules)-1], len(groupViews)
					}
				}
			})
		}
	}
}
