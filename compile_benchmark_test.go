package rulite_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/imbrooklyn/rulite"
)

var (
	benchmarkSet    *rulite.RuleSet[benchmarkInput]
	benchmarkInfo   rulite.RuleInfo
	benchmarkTags   []string
	benchmarkIssues []rulite.ValidationIssue
)

func metadataBenchmarkRules(size int) []rulite.Rule[benchmarkInput] {
	rules := make([]rulite.Rule[benchmarkInput], size)
	for i := range rules {
		rules[i] = rulite.NewRule[benchmarkInput](rulite.RuleID(fmt.Sprintf("rule/%d", i))).Priority(rulite.Priority((i*37)%101-50)).Name("Pricing offer").Description("Apply an eligible offer.").Tags(" pricing ", "audit", "pricing").When(func(context.Context, *benchmarkInput) (bool, error) { return true, nil }).Then(func(_ context.Context, input *benchmarkInput) error { input.actions++; return nil })
	}
	return rules
}

func BenchmarkCompile(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("rules_%d", size), func(b *testing.B) {
			// Builder normalization and closure construction are outside Compile timing.
			rules := metadataBenchmarkRules(size)
			var set *rulite.RuleSet[benchmarkInput]
			var err error
			b.ReportAllocs()
			for b.Loop() {
				set, err = rulite.Compile(rules...)
				if err != nil || set.Len() != size {
					b.Fatal("incomplete compilation")
				}
			}
			benchmarkSet = set
			input := benchmarkInput{}
			result, err := mustReuse(b, set).Fire(context.Background(), &input)
			checkBenchmark(b, result, err, input, benchmarkCompleted(size, size))
			checkResultConsistency(b, result)
		})
	}
}

func BenchmarkNewEngineFromRuleSet(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("rules_%d", size), func(b *testing.B) {
			set := mustCompile(b, metadataBenchmarkRules(size)...)
			var engine *rulite.Engine[benchmarkInput]
			var err error
			b.ReportAllocs()
			for b.Loop() {
				engine, err = rulite.NewEngineFromRuleSet(set, rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)))
				if err != nil {
					b.Fatal(err)
				}
			}
			benchmarkEngine = engine
			input := benchmarkInput{}
			result, err := engine.Fire(context.Background(), &input)
			checkBenchmark(b, result, err, input, benchmarkExpectation{counts: rulite.Counts{Total: size, Evaluated: 1, NotEvaluated: size - 1, Matched: 1, Fired: 1}, stop: rulite.StopFirstFire, actions: 1})
			checkResultConsistency(b, result)
		})
	}
}

func BenchmarkRuleSetLookup(b *testing.B) {
	set := mustCompile(b, metadataBenchmarkRules(1000)...)
	for _, id := range []rulite.RuleID{"rule/0", "rule/500", "rule/999", "unknown"} {
		b.Run(string(id), func(b *testing.B) {
			var info rulite.RuleInfo
			var ok bool
			b.ReportAllocs()
			for b.Loop() {
				info, ok = set.Rule(id)
				if ok != (id != "unknown") || ok && (info.ID() != id || info.Name() != "Pricing offer") {
					b.Fatal("incorrect metadata lookup")
				}
			}
			benchmarkInfo = info
		})
	}
	b.Run("tags_copy", func(b *testing.B) {
		info, _ := set.Rule("rule/500")
		var tags []string
		b.ReportAllocs()
		for b.Loop() {
			tags = info.Tags()
			if !slices.Equal(tags, []string{"pricing", "audit"}) {
				b.Fatal("incorrect tag copy")
			}
		}
		benchmarkTags = tags
	})
}

func BenchmarkValidationLookup(b *testing.B) {
	rules := metadataBenchmarkRules(1000)
	for i := range rules {
		rules[i] = rulite.NewRule[benchmarkInput](rules[i].ID()).When(nil).Then(nil)
	}
	_, err := rulite.Compile(rules...)
	validation, ok := err.(*rulite.ValidationError)
	if !ok || len(validation.Issues()) != 2000 {
		b.Fatal("incorrect validation setup")
	}
	for _, id := range []rulite.RuleID{"rule/500", "unknown"} {
		b.Run(string(id), func(b *testing.B) {
			var issues []rulite.ValidationIssue
			want := 2
			if id == "unknown" {
				want = 0
			}
			b.ReportAllocs()
			for b.Loop() {
				issues = validation.IssuesForRule(id)
				if len(issues) != want || want > 0 && (issues[0].Index() != 500 || issues[1].Index() != 500) {
					b.Fatal("incorrect indexed validation issues")
				}
			}
			benchmarkIssues = issues
		})
	}
}
