package dynamic_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/cel"
	"github.com/imbrooklyn/rulite/dynamic"
)

type price struct {
	VIP      bool
	Total    int64
	Discount int
	Steps    []string
}

type discountParams struct {
	Rate   int            `json:"rate"`
	Labels []string       `json:"labels,omitempty"`
	Limits map[string]int `json:"limits,omitempty"`
	Note   *string        `json:"note,omitempty"`
}

var errRate = errors.New("rate outside accepted range")

func compiler(t testing.TB) *cel.Compiler[price] {
	t.Helper()
	c, err := cel.NewCompiler[price]("input", cel.WithCostLimit(100))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func registry(t testing.TB) *dynamic.Registry[price] {
	t.Helper()
	r := dynamic.NewRegistry[price]()
	if err := r.Register("pricing.apply/v1", func(_ context.Context, p *price, params discountParams) error {
		p.Discount += params.Rate
		return nil
	}, func(p discountParams) error {
		if p.Rate < 0 || p.Rate > 100 {
			return errRate
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	return r
}

func definition() dynamic.Definition {
	return dynamic.Definition{ID: "pricing/vip", When: "input.VIP", Action: "pricing.apply/v1", Params: json.RawMessage(`{"rate":20}`)}
}

func document(t testing.TB, ds []dynamic.Definition) []byte {
	t.Helper()
	b, err := json.Marshal(ds)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func requireCompileError(t testing.TB, err error, stage string, index int, cause error) {
	t.Helper()
	var ce *dynamic.CompileError
	if !errors.As(err, &ce) || ce.Stage() != stage || ce.Index() != index || ce.Unwrap() == nil || cause != nil && !errors.Is(err, cause) {
		t.Fatalf("unexpected construction error: %v; stage=%s index=%d", err, stage, index)
	}
}

func TestStrictDefinitionJSON(t *testing.T) {
	c, r := compiler(t), registry(t)
	valid := string(document(t, []dynamic.Definition{definition()}))
	for _, tc := range []struct{ name, source string }{
		{"empty_input", ""}, {"null", "null"}, {"object", "{}"}, {"null_rule", "[null]"},
		{"scalar_rule", "[1]"}, {"trailing", valid + " {}"}, {"trailing_text", valid + "x"},
		{"unknown", strings.Replace(valid, `"id":`, `"extra":0,"id":`, 1)},
		{"case_alias", strings.Replace(valid, `"id":`, `"ID":`, 1)},
		{"duplicate", strings.Replace(valid, `"id":`, `"id":"other","id":`, 1)},
		{"escaped_duplicate", strings.Replace(valid, `"id":`, `"\u0069d":"other","id":`, 1)},
		{"param_duplicate", strings.Replace(valid, `"rate":20`, `"rate":10,"rate":20`, 1)},
		{"nested_duplicate", strings.Replace(valid, `"rate":20`, `"limits":{"x":1,"x":2}`, 1)},
		{"priority_overflow", strings.Replace(valid, `"id":`, `"priority":2147483648,"id":`, 1)},
		{"priority_underflow", strings.Replace(valid, `"id":`, `"priority":-2147483649,"id":`, 1)},
		{"priority_fraction", strings.Replace(valid, `"id":`, `"priority":1.5,"id":`, 1)},
		{"priority_string", strings.Replace(valid, `"id":`, `"priority":"1","id":`, 1)},
		{"priority_null", strings.Replace(valid, `"id":`, `"priority":null,"id":`, 1)},
		{"params_null", strings.Replace(valid, `{"rate":20}`, `null`, 1)},
		{"nested_null", strings.Replace(valid, `20`, `null`, 1)},
		{"tags_type", strings.Replace(valid, `"id":`, `"tags":[1],"id":`, 1)},
		{"when_type", strings.Replace(valid, `"input.VIP"`, `true`, 1)},
		{"invalid_utf8", strings.Replace(valid, `pricing/vip`, "pricing/\xff", 1)},
		{"invalid_surrogate", strings.Replace(valid, `pricing/vip`, `pricing/\ud800`, 1)},
		{"future_groups", strings.Replace(valid, `"id":`, `"groups":[],"id":`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ds, err := dynamic.Decode([]byte(tc.source))
			if ds != nil {
				t.Fatal("partial definitions returned")
			}
			requireCompileError(t, err, "decode", -1, dynamic.ErrInvalidDefinition)
			set, again := dynamic.CompileJSON([]byte(tc.source), c, r)
			if set != nil || again == nil || err.Error() != again.Error() {
				t.Fatal("strict transport diverged")
			}
		})
	}
	for _, source := range []string{"[]", valid, strings.Replace(valid, `"id":`, `"priority":2147483647,"id":`, 1), strings.Replace(valid, `"id":`, `"priority":-2147483648,"id":`, 1)} {
		if set, err := dynamic.CompileJSON([]byte(source), c, r); err != nil || !set.Valid() {
			t.Fatalf("valid transport failed: %v", err)
		}
	}
}

func TestDefinitionValidationAndFailureStages(t *testing.T) {
	c, r := compiler(t), registry(t)
	for _, tc := range []struct {
		name, stage string
		cause       error
		mutate      func(*dynamic.Definition)
	}{
		{"empty_id", "validate", rulite.ErrInvalidRule, func(d *dynamic.Definition) { d.ID = "" }},
		{"invalid_id", "validate", rulite.ErrInvalidRule, func(d *dynamic.Definition) { d.ID = "Pricing/vip" }},
		{"long_id", "validate", rulite.ErrInvalidRule, func(d *dynamic.Definition) { d.ID = rulite.RuleID(strings.Repeat("a", 129)) }},
		{"missing_when", "validate", dynamic.ErrInvalidDefinition, func(d *dynamic.Definition) { d.When = " " }},
		{"missing_params", "validate", dynamic.ErrInvalidParams, func(d *dynamic.Definition) { d.Params = nil }},
		{"array_params", "validate", dynamic.ErrInvalidParams, func(d *dynamic.Definition) { d.Params = json.RawMessage(`[]`) }},
		{"invalid_action_name", "validate", dynamic.ErrInvalidAction, func(d *dynamic.Definition) { d.Action = "os.Exit" }},
		{"non_bool", "condition", cel.ErrNonBool, func(d *dynamic.Definition) { d.When = "input.Total" }},
		{"renamed_field", "condition", nil, func(d *dynamic.Definition) { d.When = "input.Renamed" }},
		{"syntax", "condition", nil, func(d *dynamic.Definition) { d.When = "(" }},
		{"long_source", "condition", nil, func(d *dynamic.Definition) { d.When = strings.Repeat(" ", 4096) + "true" }},
		{"unknown_action", "action", dynamic.ErrUnknownAction, func(d *dynamic.Definition) { d.Action = "pricing.unknown/v1" }},
		{"unknown_param", "action", dynamic.ErrInvalidParams, func(d *dynamic.Definition) { d.Params = json.RawMessage(`{"unknown":1}`) }},
		{"param_case", "action", dynamic.ErrInvalidParams, func(d *dynamic.Definition) { d.Params = json.RawMessage(`{"Rate":20}`) }},
		{"param_type", "action", dynamic.ErrInvalidParams, func(d *dynamic.Definition) { d.Params = json.RawMessage(`{"rate":"20"}`) }},
		{"param_validation", "action", errRate, func(d *dynamic.Definition) { d.Params = json.RawMessage(`{"rate":101}`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := definition()
			tc.mutate(&d)
			set, err := dynamic.Compile([]dynamic.Definition{d}, c, r)
			if set != nil {
				t.Fatal("partial set returned")
			}
			index := 0
			if errors.Is(tc.cause, rulite.ErrInvalidRule) {
				index = -1
			}
			requireCompileError(t, err, tc.stage, index, tc.cause)
		})
	}
	d := definition()
	set, err := dynamic.Compile([]dynamic.Definition{d, d}, c, r)
	var validation *rulite.ValidationError
	if set != nil || !errors.As(err, &validation) || !errors.Is(err, rulite.ErrDuplicateRuleID) || validation.Issues()[0].Index() != 1 {
		t.Fatal("canonical duplicate validation lost")
	}
	for _, c := range []*cel.Compiler[price]{nil, {}} {
		if set, err := dynamic.Compile(nil, c, r); set != nil || !errors.Is(err, cel.ErrInvalidCompiler) {
			t.Fatal("invalid empty compiler accepted")
		}
	}
	var ce *dynamic.CompileError
	if ce.Stage() != "" || ce.Index() != -1 || ce.Unwrap() != nil || ce.Error() == "" {
		t.Fatal("nil diagnostic unsafe")
	}
}

func TestDefinitionBounds(t *testing.T) {
	c, r := compiler(t), registry(t)
	for _, mutate := range []func(*dynamic.Definition){
		func(d *dynamic.Definition) { d.When = strings.Repeat("x", 1<<20) + "\xff" },
		func(d *dynamic.Definition) { d.Description = strings.Repeat("d", 4097) },
		func(d *dynamic.Definition) { d.Tags = make([]string, 33) },
		func(d *dynamic.Definition) { d.Tags = []string{strings.Repeat("t", 129)} },
		func(d *dynamic.Definition) {
			d.Params = json.RawMessage(`{"note":"` + strings.Repeat("x", 16384) + `"}`)
		},
		func(d *dynamic.Definition) {
			d.Params = json.RawMessage(strings.Repeat(`{"x":`, 17) + `0` + strings.Repeat(`}`, 17))
		},
		func(d *dynamic.Definition) {
			d.Params = json.RawMessage(`{"labels":[` + strings.Repeat(`"",`, 4096) + `""]}`)
		},
	} {
		d := definition()
		mutate(&d)
		if set, err := dynamic.Compile([]dynamic.Definition{d}, c, r); set != nil || !errors.Is(err, dynamic.ErrLimit) {
			t.Fatalf("bound failed: %v", err)
		}
	}
	if ds, err := dynamic.Decode([]byte(strings.Repeat(" ", 1<<20) + "[]")); ds != nil || !errors.Is(err, dynamic.ErrLimit) {
		t.Fatal("document bytes unbounded")
	}
	if ds, err := dynamic.Decode([]byte(strings.Repeat("[", 19) + strings.Repeat("]", 19))); ds != nil || !errors.Is(err, dynamic.ErrLimit) {
		t.Fatal("document depth unbounded")
	}
	if set, err := dynamic.Compile(make([]dynamic.Definition, 257), c, r); set != nil || !errors.Is(err, dynamic.ErrLimit) {
		t.Fatal("definition count unbounded")
	}
}

func TestDefinitionAndValidatorOwnership(t *testing.T) {
	c := compiler(t)
	var retained discountParams
	var validators = []func(discountParams) error{func(p discountParams) error { retained = p; return nil }}
	r := dynamic.NewRegistry[price]()
	if err := r.Register[discountParams]("pricing.apply/v1", func(_ context.Context, p *price, params discountParams) error {
		p.Discount = params.Rate + params.Limits["bonus"] + len(params.Labels) + len(*params.Note)
		return nil
	}, validators...); err != nil {
		t.Fatal(err)
	}
	validators[0] = func(discountParams) error { return errRate }
	if err := r.Freeze(); err != nil {
		t.Fatal(err)
	}
	d := definition()
	d.Description = " offer "
	d.Tags = []string{" vip ", "vip", ""}
	d.Params = json.RawMessage(`{"rate":20,"labels":["v"],"limits":{"bonus":2},"note":"ok"}`)
	raw := document(t, []dynamic.Definition{d})
	ds, err := dynamic.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	clear(raw)
	set, err := dynamic.Compile(ds, c, r)
	if err != nil {
		t.Fatal(err)
	}
	clear(ds[0].Params)
	ds[0].Tags[0] = "changed"
	ds[0] = dynamic.Definition{}
	retained.Labels[0] = "changed"
	retained.Limits["bonus"] = 500
	*retained.Note = "changed"
	engine, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		input := price{VIP: true}
		result, err := engine.Fire(context.Background(), &input)
		if err != nil || input.Discount != 25 || result.Counts().Fired != 1 {
			t.Fatal("mutable configuration leaked")
		}
		info, _ := set.Rule("pricing/vip")
		if info.Description() != "offer" || !reflect.DeepEqual(info.Tags(), []string{"vip"}) {
			t.Fatal("metadata ownership changed")
		}
	}
}
