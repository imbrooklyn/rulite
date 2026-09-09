package cel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
)

var conditionSink []rulite.Condition[price]
var matchSink int
var resultSink rulite.Result

func benchmarkSources(n int) []string {
	sources := make([]string, n)
	for i := range sources {
		sources[i] = fmt.Sprintf("input.VIP && input.Total > %d", i)
	}
	return sources
}

func BenchmarkCompile(b *testing.B) {
	for _, n := range []int{1, 10, 100} {
		b.Run(fmt.Sprintf("expressions_%d", n), func(b *testing.B) {
			c := compiler[price](b)
			sources := benchmarkSources(n)
			compiled := make([]rulite.Condition[price], n)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				for i, source := range sources {
					var err error
					compiled[i], err = c.Compile(source)
					if err != nil {
						b.Fatal(err)
					}
				}
			}
			b.StopTimer()
			input := price{VIP: true, Total: int64((n + 1) / 2)}
			matches := 0
			for _, check := range compiled {
				ok, err := check(context.Background(), &input)
				if err != nil {
					b.Fatal(err)
				}
				if ok {
					matches++
				}
			}
			if matches != (n+1)/2 {
				b.Fatal("compiled match count changed")
			}
			conditionSink = compiled
		})
	}
}

func benchmarkConditions(b *testing.B, n int, provider string) []rulite.Condition[price] {
	b.Helper()
	checks := make([]rulite.Condition[price], n)
	if provider == "cel" {
		c := compiler[price](b)
		for i, source := range benchmarkSources(n) {
			checks[i] = condition(b, c, source)
		}
	} else {
		for i := range checks {
			checks[i] = func(_ context.Context, p *price) (bool, error) { return p.VIP && p.Total > int64(i), nil }
		}
	}
	return checks
}

func BenchmarkEval(b *testing.B) {
	for _, provider := range []string{"cel", "go"} {
		for _, n := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("%s/expressions_%d", provider, n), func(b *testing.B) {
				checks := benchmarkConditions(b, n, provider)
				ctx := context.Background()
				input := price{}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					input = price{VIP: true, Total: int64((n + 1) / 2)}
					matches := 0
					for _, check := range checks {
						ok, err := check(ctx, &input)
						if err != nil {
							b.Fatal(err)
						}
						if ok {
							matches++
						}
					}
					if matches != (n+1)/2 {
						b.Fatal("evaluation match count changed")
					}
					matchSink = matches
				}
			})
		}
	}
}

func BenchmarkFire(b *testing.B) {
	for _, provider := range []string{"cel", "go"} {
		for _, n := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("%s/expressions_%d", provider, n), func(b *testing.B) {
				checks := benchmarkConditions(b, n, provider)
				rules := make([]rulite.Rule[price], n)
				for i, check := range checks {
					rules[i] = rulite.NewRule[price](rulite.RuleID(fmt.Sprintf("pricing/%d", i))).When(check).
						Then(func(_ context.Context, p *price) error { p.Discount++; return nil })
				}
				engine, err := rulite.NewEngine(rules...)
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
						b.Fatal("Fire outcome changed")
					}
					resultSink = result
				}
			})
		}
	}
}
