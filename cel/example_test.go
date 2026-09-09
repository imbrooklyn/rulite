package cel_test

import (
	"context"
	"fmt"
	"time"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
)

func ExampleNewCompiler() {
	type Price struct {
		VIP      bool
		Total    int64
		Discount int
	}
	compiler, err := cel.NewCompiler[Price]("input")
	if err != nil {
		panic(err)
	}
	eligible, err := compiler.Compile("input.VIP && input.Total >= 10000")
	if err != nil {
		panic(err)
	}
	rule := rulite.NewRule[Price]("pricing/vip").When(eligible).
		Then(func(_ context.Context, p *Price) error { p.Discount = 20; return nil })
	engine, err := rulite.NewEngine(rule)
	if err != nil {
		panic(err)
	}
	price := Price{VIP: true, Total: 12000}
	result, err := engine.Fire(context.Background(), &price)
	if err != nil {
		panic(err)
	}
	fmt.Println(price.Discount, result.Fired())
	// Output: 20 [pricing/vip]
}

func ExampleNewBuilder() {
	type Order struct {
		Total    int64
		Customer string
		PlacedAt time.Time
	}
	builder, err := cel.NewBuilder[Order](cel.WithCostLimit(1000))
	if err != nil {
		panic(err)
	}
	if err := builder.Bind("order", func(_ context.Context, p *Order) (*Order, error) { return p, nil }); err != nil {
		panic(err)
	}
	if err := builder.Bind[time.Time]("placed", func(_ context.Context, p *Order) (time.Time, error) { return p.PlacedAt, nil }); err != nil {
		panic(err)
	}
	if err := builder.Function("eligible", func(total int64) (bool, error) { return total >= 10000, nil }); err != nil {
		panic(err)
	}
	compiler, err := builder.Build()
	if err != nil {
		panic(err)
	}
	check, err := compiler.Compile("eligible(order.Total) && placed == order.PlacedAt")
	if err != nil {
		panic(err)
	}
	input := Order{Total: 12000}
	matched, err := check(context.Background(), &input)
	fmt.Println(matched, err)
	// Output: true <nil>
}
