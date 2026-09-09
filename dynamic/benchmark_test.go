package dynamic_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
)

var definitionSink []dynamic.Definition
var setSink *rulite.RuleSet[price]
var resultSink rulite.Result

func benchmarkDefinitions(n int) []dynamic.Definition {
	ds := make([]dynamic.Definition, n)
	for i := range ds {
		ds[i] = dynamic.Definition{ID: rulite.RuleID(fmt.Sprintf("pricing/%d", i)), Priority: rulite.Priority(i % 3), When: fmt.Sprintf("input.VIP && input.Total > %d", i), Action: "pricing.apply/v1", Params: json.RawMessage(`{"rate":1}`)}
	}
	return ds
}

func checkBenchmarkSet(b *testing.B, set *rulite.RuleSet[price], n int) {
	b.Helper()
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		b.Fatal(err)
	}
	input := price{VIP: true, Total: int64((n + 1) / 2)}
	result, err := engine.Fire(context.Background(), &input)
	if err != nil || result.Counts().Evaluated != n || result.Counts().Fired != (n+1)/2 || input.Discount != (n+1)/2 {
		b.Fatal("compiled benchmark outcome changed")
	}
}

func BenchmarkDecode(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("rules_%d", n), func(b *testing.B) {
			raw := document(b, benchmarkDefinitions(n))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				ds, err := dynamic.Decode(raw)
				if err != nil || len(ds) != n {
					b.Fatal("decode outcome changed")
				}
				definitionSink = ds
			}
			b.StopTimer()
			set, err := dynamic.Compile(definitionSink, compiler(b), registry(b))
			if err != nil {
				b.Fatal(err)
			}
			checkBenchmarkSet(b, set, n)
		})
	}
}

func BenchmarkCompile(b *testing.B) {
	for _, path := range []string{"json", "definitions"} {
		for _, n := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("%s/rules_%d", path, n), func(b *testing.B) {
				c, r := compiler(b), registry(b)
				ds := benchmarkDefinitions(n)
				raw := document(b, ds)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					var set *rulite.RuleSet[price]
					var err error
					if path == "json" {
						set, err = dynamic.CompileJSON(raw, c, r)
					} else {
						set, err = dynamic.Compile(ds, c, r)
					}
					if err != nil {
						b.Fatal(err)
					}
					setSink = set
				}
				b.StopTimer()
				checkBenchmarkSet(b, setSink, n)
			})
		}
	}
}

func benchmarkSet(b *testing.B, n int, provider string, c *cel.Compiler[price]) *rulite.RuleSet[price] {
	b.Helper()
	ds := benchmarkDefinitions(n)
	if provider == "dynamic" {
		set, err := dynamic.CompileJSON(document(b, ds), c, registry(b))
		if err != nil {
			b.Fatal(err)
		}
		return set
	}
	rules := make([]rulite.Rule[price], n)
	for i, d := range ds {
		var condition rulite.Condition[price]
		if provider == "typed_cel" {
			var err error
			condition, err = c.Compile(d.When)
			if err != nil {
				b.Fatal(err)
			}
		} else {
			condition = func(_ context.Context, p *price) (bool, error) { return p.VIP && p.Total > int64(i), nil }
		}
		rules[i] = rulite.NewRule[price](d.ID).Priority(d.Priority).When(condition).Then(func(_ context.Context, p *price) error { p.Discount++; return nil })
	}
	set, err := rulite.Compile(rules...)
	if err != nil {
		b.Fatal(err)
	}
	return set
}

func BenchmarkFire(b *testing.B) {
	for _, provider := range []string{"dynamic", "typed_cel", "typed_go"} {
		for _, n := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("%s/rules_%d", provider, n), func(b *testing.B) {
				set := benchmarkSet(b, n, provider, compiler(b))
				checkBenchmarkSet(b, set, n)
				engine, err := rulite.NewEngineFromRuleSet(set)
				if err != nil {
					b.Fatal(err)
				}
				ctx := context.Background()
				input := price{}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					input = price{VIP: true, Total: int64((n + 1) / 2)}
					result, err := engine.Fire(ctx, &input)
					counts := result.Counts()
					if err != nil || counts.Evaluated != n || counts.Matched != (n+1)/2 || counts.Fired != (n+1)/2 || input.Discount != (n+1)/2 || result.StopReason() != rulite.StopCompleted {
						b.Fatal("runtime outcome changed")
					}
					resultSink = result
				}
			})
		}
	}
}
