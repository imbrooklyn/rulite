package dynamic_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite/dynamic"
)

type customParams struct{ Rate int }

func (*customParams) UnmarshalJSON([]byte) error { panic("decoder must not run") }

type embeddedParams struct{ discountParams }
type recursiveParams struct{ Next *recursiveParams }

func TestRegistryConstructionAndFreeze(t *testing.T) {
	action := func(context.Context, *price, discountParams) error { return nil }
	r := dynamic.NewRegistry[price]()
	for _, name := range []string{"", "pricing.apply", "pricing.apply/v0", "pricing.apply/v01", "pricing.apply/V1", "Pricing.apply/v1", "pricing..apply/v1", "os/exec.Run/v1", "pricing.apply/v1/extra"} {
		if err := r.Register(name, action); !errors.Is(err, dynamic.ErrInvalidAction) {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	if err := r.Register[discountParams]("pricing.apply/v1", nil); !errors.Is(err, dynamic.ErrInvalidAction) {
		t.Fatal("nil action accepted")
	}
	if err := r.Register("pricing.apply/v1", action, nil); !errors.Is(err, dynamic.ErrInvalidAction) {
		t.Fatal("nil validator accepted")
	}
	validate := func(discountParams) error { return nil }
	if err := r.Register("pricing.apply/v1", action, validate, validate); !errors.Is(err, dynamic.ErrInvalidAction) {
		t.Fatal("multiple validators accepted")
	}
	if err := r.Register("pricing.apply/v1", action); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("pricing.apply/v1", action); !errors.Is(err, dynamic.ErrDuplicateAction) {
		t.Fatal("duplicate accepted")
	}
	if set, err := dynamic.Compile(nil, compiler(t), r); set != nil || !errors.Is(err, dynamic.ErrRegistryNotFrozen) {
		t.Fatal("mutable registry compiled")
	}
	copy := *r
	if err := copy.Freeze(); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal("freeze is not idempotent")
	}
	if err := r.Register("pricing.apply/v2", action); !errors.Is(err, dynamic.ErrRegistryFrozen) {
		t.Fatal("registration after freeze accepted")
	}
	for _, r := range []*dynamic.Registry[price]{nil, {}} {
		if !errors.Is(r.Freeze(), dynamic.ErrInvalidRegistry) || !errors.Is(r.Register("pricing.apply/v1", action), dynamic.ErrInvalidRegistry) {
			t.Fatal("zero registry accepted")
		}
		if set, err := dynamic.CompileJSON([]byte("[]"), compiler(t), r); set != nil || !errors.Is(err, dynamic.ErrInvalidRegistry) {
			t.Fatal("zero registry compiled")
		}
	}
}

func TestParameterSchemas(t *testing.T) {
	r := dynamic.NewRegistry[price]()
	for _, err := range []error{
		r.Register("type.scalar/v1", func(context.Context, *price, int) error { return nil }),
		r.Register("type.pointer/v1", func(context.Context, *price, *discountParams) error { return nil }),
		r.Register("type.custom/v1", func(context.Context, *price, customParams) error { return nil }),
		r.Register("type.embedded/v1", func(context.Context, *price, embeddedParams) error { return nil }),
		r.Register("type.recursive/v1", func(context.Context, *price, recursiveParams) error { return nil }),
		r.Register("type.interface/v1", func(context.Context, *price, struct{ Value any }) error { return nil }),
		r.Register("type.raw/v1", func(context.Context, *price, struct{ Value json.RawMessage }) error { return nil }),
		r.Register("type.time/v1", func(context.Context, *price, struct{ Value time.Time }) error { return nil }),
		r.Register("type.duration/v1", func(context.Context, *price, struct{ Value time.Duration }) error { return nil }),
		r.Register("type.array/v1", func(context.Context, *price, struct{ Value [2]int }) error { return nil }),
		r.Register("type.map/v1", func(context.Context, *price, struct{ Value map[int]int }) error { return nil }),
		r.Register("type.alias/v1", func(context.Context, *price, struct {
			Value int `json:"value,case:ignore"`
		}) error {
			return nil
		}),
		r.Register("type.string/v1", func(context.Context, *price, struct {
			Value int `json:"value,string"`
		}) error {
			return nil
		}),
	} {
		if !errors.Is(err, dynamic.ErrInvalidAction) {
			t.Fatalf("unsupported schema accepted: %v", err)
		}
	}
	type units int64
	type nested struct {
		Count units `json:"count"`
	}
	type params struct {
		Data   []byte            `json:"data"`
		Nested *nested           `json:"nested,omitempty"`
		Rows   []nested          `json:"rows"`
		Lookup map[string]nested `json:"lookup"`
		Hidden any               `json:"-"`
	}
	if err := r.Register("type.supported/v1", func(_ context.Context, p *price, v params) error {
		if string(v.Data) != "ok" || v.Nested.Count != 9007199254740993 || v.Rows[0].Count != 2 || v.Lookup["x"].Count != 3 || v.Hidden != nil {
			return errors.New("typed parameter values changed")
		}
		p.Discount++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	d := definition()
	d.Action = "type.supported/v1"
	d.Params = json.RawMessage(`{"data":"b2s=","nested":{"count":9007199254740993},"rows":[{"count":2}],"lookup":{"x":{"count":3}}}`)
	set, err := dynamic.Compile([]dynamic.Definition{d}, compiler(t), r)
	if err != nil {
		t.Fatal(err)
	}
	fireSet(t, set, price{VIP: true}, 1)
	for _, raw := range []string{`{"data":"!"}`, `{"nested":{"unknown":1}}`, `{"lookup":{"x":{"Count":1}}}`, `{"Hidden":1}`, `{"nested":{"count":9223372036854775808}}`} {
		d.Params = json.RawMessage(raw)
		if set, err := dynamic.Compile([]dynamic.Definition{d}, compiler(t), r); set != nil || !errors.Is(err, dynamic.ErrInvalidParams) {
			t.Fatal("invalid nested parameter accepted")
		}
	}
}

func TestConcurrentRegistrationAndCapacity(t *testing.T) {
	r := dynamic.NewRegistry[price]()
	var wg sync.WaitGroup
	for i := range 256 {
		wg.Go(func() {
			if err := r.Register(fmt.Sprintf("test.action/v%d", i+1), func(context.Context, *price, struct{}) error { return nil }); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := r.Register("test.overflow/v1", func(context.Context, *price, struct{}) error { return nil }); !errors.Is(err, dynamic.ErrLimit) {
		t.Fatal("registry count unbounded")
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
}

type privateError struct{ calls *int }

func (e privateError) Error() string { *e.calls++; return "sensitive parameter value" }

func TestCompilePrivacyOrderingAndAtomicity(t *testing.T) {
	c := compiler(t)
	r := dynamic.NewRegistry[price]()
	formatCalls, validations, actions := 0, 0, 0
	cause := privateError{&formatCalls}
	if err := r.Register("pricing.apply/v1", func(context.Context, *price, discountParams) error { actions++; return nil }, func(p discountParams) error {
		validations++
		if p.Rate == 1 {
			return cause
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	d := definition()
	ds := []dynamic.Definition{d, d}
	ds[1].ID = "pricing/second"
	ds[1].When = "input.Missing"
	if set, err := dynamic.Compile(ds, c, r); set != nil || err == nil || validations != 0 || actions != 0 {
		t.Fatal("callbacks ran before condition compilation completed")
	}
	ds[1].When = "true"
	ds[1].Params = json.RawMessage(`{"rate":1}`)
	set, err := dynamic.Compile(ds, c, r)
	if set != nil || validations != 2 || actions != 0 {
		t.Fatal("partial set or action escaped failed compile")
	}
	var got privateError
	if !errors.As(err, &got) {
		t.Fatal("validator cause lost")
	}
	_ = err.Error()
	if formatCalls != 0 {
		t.Fatal("compile error formatted private cause")
	}
}
