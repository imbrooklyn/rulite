package cel

import (
	"context"
	"testing"

	"cel.dev/cel-go/interpreter"
)

type projectionInput struct {
	Total   int64
	Country string
	Items   []int64
}

var activationSink interpreter.Activation

func BenchmarkBindingProjection(b *testing.B) {
	builder, err := NewBuilder[projectionInput]()
	if err != nil {
		b.Fatal(err)
	}
	if err := builder.Bind("order", func(_ context.Context, p *projectionInput) (*projectionInput, error) { return p, nil }); err != nil {
		b.Fatal(err)
	}
	if err := builder.Bind("total", func(_ context.Context, p *projectionInput) (int64, error) { return p.Total, nil }); err != nil {
		b.Fatal(err)
	}
	c, err := builder.Build()
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	input := projectionInput{}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		input = projectionInput{Total: 12000, Country: "GB", Items: []int64{1, 2}}
		activation, err := projectBindings(ctx, &input, c.bindings, c.schema)
		if err != nil {
			b.Fatal(err)
		}
		total, ok := activation.ResolveName("total")
		order, bound := activation.ResolveName("order")
		if !ok || total != int64(12000) || !bound || order != &input {
			b.Fatal("projected values changed")
		}
		activationSink = activation
	}
}
