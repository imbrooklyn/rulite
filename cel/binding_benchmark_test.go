package cel_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"google.golang.org/protobuf/types/dynamicpb"
)

type bindingBenchInput struct {
	Native price
	Proto  *dynamicpb.Message
}

var bindingConditionSink []rulite.Condition[bindingBenchInput]

func bindingBenchmark(b *testing.B, mode string) (*cel.Compiler[bindingBenchInput], bindingBenchInput, string) {
	b.Helper()
	desc := messageDescriptor(b)
	input := bindingBenchInput{Native: price{VIP: true, Total: 12000}, Proto: protoOrder(desc, 12000)}
	options := []cel.Option{}
	if mode == "json" {
		options = append(options, cel.WithJSONFieldNames())
	}
	builder := builder[bindingBenchInput](b, options...)
	field := "order.Total"
	if mode == "protobuf" {
		field = "order.total_amount"
		if err := builder.BindProto("order", desc, func(_ context.Context, p *bindingBenchInput) (*dynamicpb.Message, error) { return p.Proto, nil }); err != nil {
			b.Fatal(err)
		}
	} else {
		if mode == "json" {
			field = "order.total_amount"
		}
		if err := builder.Bind("order", func(_ context.Context, p *bindingBenchInput) (*price, error) { return &p.Native, nil }); err != nil {
			b.Fatal(err)
		}
	}
	return built(b, builder), input, field
}

func BenchmarkBindingCompile(b *testing.B) {
	for _, mode := range []string{"native", "json", "protobuf"} {
		for _, n := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("%s/expressions_%d", mode, n), func(b *testing.B) {
				c, input, field := bindingBenchmark(b, mode)
				sources := make([]string, n)
				checks := make([]rulite.Condition[bindingBenchInput], n)
				for i := range sources {
					sources[i] = fmt.Sprintf("%s > %d", field, 12000+i)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					for i, source := range sources {
						var err error
						checks[i], err = c.Compile(source)
						if err != nil {
							b.Fatal(err)
						}
					}
				}
				b.StopTimer()
				for _, check := range checks {
					if ok, err := check(context.Background(), &input); ok || err != nil {
						b.Fatal("compiled outcome changed")
					}
				}
				bindingConditionSink = checks
			})
		}
	}
}

func BenchmarkBindingEval(b *testing.B) {
	for _, mode := range []string{"native", "json", "protobuf"} {
		for _, n := range []int{1, 10, 100} {
			b.Run(fmt.Sprintf("%s/expressions_%d", mode, n), func(b *testing.B) {
				c, seed, field := bindingBenchmark(b, mode)
				checks := make([]rulite.Condition[bindingBenchInput], n)
				for i := range checks {
					checks[i] = condition(b, c, fmt.Sprintf("%s > %d", field, 12000+i-(n+1)/2))
				}
				ctx := context.Background()
				input := seed
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					input = seed
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
						b.Fatal("binding match count changed")
					}
					matchSink = matches
				}
			})
		}
	}
}
