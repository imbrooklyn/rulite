package cel_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestTrustedFunctionRegistrationAndFailures(t *testing.T) {
	b := builder[price](t)
	cause := errors.New("private function detail")
	callback := func(value int64) (bool, error) { return value > 0, nil }
	for _, name := range []string{"size", "has", "true", "__hidden"} {
		if err := b.Function(name, callback); err == nil {
			t.Fatalf("reserved function name accepted: %s", name)
		}
	}
	if err := b.Function[int64, bool]("nilFunction", nil); err == nil {
		t.Fatal("nil function accepted")
	}
	if err := b.Function("badType", func([]int64) (bool, error) { return true, nil }); err == nil {
		t.Fatal("collection function accepted")
	}
	if err := b.Function("positive", callback); err != nil {
		t.Fatal(err)
	}
	if err := b.Function("positive", callback); err == nil {
		t.Fatal("duplicate function accepted")
	}
	if err := b.Bind("positive", func(context.Context, *price) (int64, error) { return 1, nil }); err == nil {
		t.Fatal("binding/function collision accepted")
	}
	if err := b.Function("reject", func(string) (bool, error) { return false, cause }); err != nil {
		t.Fatal(err)
	}
	if err := b.Function("panicValue", func(string) (bool, error) { panic(cause) }); err != nil {
		t.Fatal(err)
	}
	if err := b.Function("large", func(bool) (string, error) { return strings.Repeat("x", 65537), nil }); err != nil {
		t.Fatal(err)
	}
	c := built(t, b)
	checkTrue(t, c, &price{}, "positive(1) && !positive(0)")
	checkCompileFailure(t, c, "positive('one')", "positive(1, 2)")
	for _, tc := range []struct {
		source string
		cause  error
	}{{"reject('private')", cause}, {"panicValue('private')", cel.ErrFunctionPanic}, {"size(large(true)) > 0", cel.ErrInputLimit}} {
		f := condition(t, c, tc.source)
		for _, mode := range []rulite.PanicMode{rulite.RecoverPanics, rulite.PropagatePanics} {
			e, err := rulite.NewEngine(rulite.NewRule[price]("pricing/capability").When(f).Then(func(context.Context, *price) error { t.Error("failed condition fired"); return nil }))
			if err != nil {
				t.Fatal(err)
			}
			r, err := e.Fire(context.Background(), &price{}, rulite.WithPanicMode(mode), rulite.WithTrace())
			if !errors.Is(err, tc.cause) || r.StopReason() != rulite.StopConditionError || strings.Contains(err.Error(), "private") || r.Counts().PanicRecovered != 0 {
				t.Fatal("trusted failure mapping changed")
			}
		}
	}
	if err := b.Function("later", callback); err != nil {
		t.Fatal(err)
	}
	checkCompileFailure(t, c, "later(1)")
}

func TestProjectionContextAndCause(t *testing.T) {
	type contextKey struct{}
	cause := errors.New("request withdrawn")
	projectError := errors.New("projection unavailable")
	ctx, cancel := context.WithCancelCause(context.WithValue(context.Background(), contextKey{}, "request"))
	b := builder[price](t)
	var later bool
	if err := b.Bind("initial", func(ctx context.Context, _ *price) (bool, error) {
		if ctx.Value(contextKey{}) != "request" {
			t.Error("context value lost")
		}
		cancel(cause)
		return true, projectError
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.Bind("later", func(context.Context, *price) (bool, error) { later = true; return true, nil }); err != nil {
		t.Fatal(err)
	}
	f := condition(t, built(t, b), "true")
	if ok, err := f(ctx, &price{}); ok || !errors.Is(err, cause) || !errors.Is(err, context.Canceled) || !errors.Is(err, projectError) || later {
		t.Fatal("projection boundary lost error, cause, or order")
	}
	panicBuilder := builder[price](t)
	if err := panicBuilder.Bind("projected", func(context.Context, *price) (bool, error) { panic(projectError) }); err != nil {
		t.Fatal(err)
	}
	engine, err := rulite.NewEngine(rulite.NewRule[price]("pricing/project").When(condition(t, built(t, panicBuilder), "projected")).Then(func(context.Context, *price) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	r, err := engine.Fire(context.Background(), &price{})
	var recovered *rulite.PanicError
	if r.StopReason() != rulite.StopPanic || !errors.As(err, &recovered) || recovered.Value() != projectError {
		t.Fatal("projector panic bypassed root policy")
	}
}

func TestBlockingCapabilitiesAreSynchronous(t *testing.T) {
	for _, kind := range []string{"projector", "function"} {
		t.Run(kind, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
				ctx, cancel := context.WithCancelCause(context.Background())
				cause := errors.New("caller canceled")
				b := builder[price](t, cel.WithCostLimit(1))
				source := "projected"
				if kind == "projector" {
					if err := b.Bind("projected", func(context.Context, *price) (bool, error) { close(entered); <-release; return true, nil }); err != nil {
						t.Fatal(err)
					}
				} else {
					source = "blocked(true)"
					if err := b.Function("blocked", func(bool) (bool, error) { close(entered); <-release; return true, nil }); err != nil {
						t.Fatal(err)
					}
				}
				f := condition(t, built(t, b), source)
				go func() { _, err := f(ctx, &price{}); done <- err }()
				<-entered
				cancel(cause)
				synctest.Wait()
				select {
				case <-done:
					t.Fatal("evaluation returned before callback finished")
				default:
				}
				close(release)
				if err := <-done; !errors.Is(err, cause) || !errors.Is(err, context.Canceled) {
					t.Fatal("post-callback cancellation lost")
				}
			})
		})
	}
}

func TestTrustedFunctionCost(t *testing.T) {
	b := builder[price](t, cel.WithCostLimit(20))
	calls := 0
	if err := b.Bind("items", func(_ context.Context, p *price) ([]int64, error) { return p.Items, nil }); err != nil {
		t.Fatal(err)
	}
	if err := b.Function("check", func(int64) (bool, error) { calls++; return true, nil }); err != nil {
		t.Fatal(err)
	}
	f := condition(t, built(t, b), "items.all(x, check(x))")
	if ok, err := f(context.Background(), &price{Items: make([]int64, 100)}); ok || !errors.Is(err, cel.ErrCostLimit) || calls == 0 || calls >= 100 {
		t.Fatal("function comprehension exceeded its bounded work")
	}
}

func TestSharedProjectedProtoAndFunctionPrograms(t *testing.T) {
	type request struct {
		Native  price
		Proto   *dynamicpb.Message
		Applied int
	}
	desc := messageDescriptor(t)
	for _, jsonNames := range []bool{false, true} {
		options := []cel.Option{cel.WithCostLimit(1000)}
		if jsonNames {
			options = append(options, cel.WithJSONFieldNames())
		}
		b := builder[request](t, options...)
		var projections, calls atomic.Int64
		if err := b.Bind("native", func(_ context.Context, p *request) (*price, error) { projections.Add(1); return &p.Native, nil }); err != nil {
			t.Fatal(err)
		}
		if err := b.BindProto("order", desc, func(_ context.Context, p *request) (*dynamicpb.Message, error) {
			projections.Add(1)
			return p.Proto, nil
		}); err != nil {
			t.Fatal(err)
		}
		if err := b.Function("eligible", func(total int64) (bool, error) { calls.Add(1); return total > 50, nil }); err != nil {
			t.Fatal(err)
		}
		c := built(t, b)
		field := "Total"
		if jsonNames {
			field = "total_amount"
		}
		f := condition(t, c, "native."+field+" == order.total_amount && has(order.coupon) && eligible(order.total_amount)")
		e, err := rulite.NewEngine(rulite.NewRule[request]("pricing/shared").When(f).Then(func(_ context.Context, p *request) error { p.Applied++; return nil }))
		if err != nil {
			t.Fatal(err)
		}
		for _, workers := range []int{1, 2, 4, 8, 16, 32} {
			t.Run(fmt.Sprintf("json_%t/workers_%d", jsonNames, workers), func(t *testing.T) {
				projections.Store(0)
				calls.Store(0)
				var wg sync.WaitGroup
				for worker := range workers {
					wg.Go(func() {
						for iteration := range 20 {
							total := int64(worker + iteration*4)
							input := request{Native: price{Total: total}, Proto: protoOrder(desc, total)}
							r, err := e.Fire(context.Background(), &input, rulite.WithTrace())
							want := 0
							if total > 50 {
								want = 1
							}
							if err != nil || r.Counts().Fired != want || input.Applied != want {
								t.Error("shared program mixed independent input")
							}
							tr, _ := r.Trace()
							tree, ok := tr.Rules()[0].ConditionTree()
							if ok && len(tree.Children()) != 0 {
								t.Error("CEL AST was exposed as Go condition children")
							}
						}
					})
				}
				wg.Wait()
				if projections.Load() != int64(workers*40) || calls.Load() != int64(workers*20) {
					t.Fatal("synchronized capture counts changed")
				}
			})
		}
	}
}
