package dynamic_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/dynamic"
)

func FuzzDefinitionsJSON(f *testing.F) {
	valid := string(document(f, []dynamic.Definition{definition()}))
	for _, seed := range []string{valid, "[]", "null", "[", valid + "{}",
		strings.Replace(valid, `"id":`, `"id":"duplicate","id":`, 1),
		strings.Replace(valid, `"id":`, `"priority":2147483648,"id":`, 1),
		strings.Replace(valid, `"id":`, `"groups":[],"id":`, 1),
		strings.Replace(valid, `"input.VIP"`, `"`+strings.Repeat(" ", 4096)+`true"`, 1),
		strings.Replace(valid, `"input.VIP"`, `"1 / input.Total > 0"`, 1),
		strings.Replace(valid, `"input.VIP"`, `"dyn(input).Missing == 0"`, 1),
		strings.Replace(valid, `"rate":20`, `"rate":1,"rate":20`, 1),
	} {
		f.Add([]byte(seed))
	}
	c, r := compiler(f), registry(f)
	f.Fuzz(func(t *testing.T, source []byte) {
		if len(source) > 8192 || strings.Count(string(source), "[")+strings.Count(string(source), "{")+strings.Count(string(source), "(") > 32 {
			t.Skip()
		}
		ds, err := dynamic.Decode(source)
		again, againErr := dynamic.Decode(source)
		if (err == nil) != (againErr == nil) || !reflect.DeepEqual(ds, again) {
			t.Fatal("decode is not deterministic")
		}
		if err != nil && (ds != nil || err.Error() != againErr.Error()) {
			t.Fatal("partial or inconsistent decode failure")
		}
		if len(ds) > 8 {
			t.Skip()
		}
		a, ae := dynamic.CompileJSON(source, c, r)
		b, be := dynamic.CompileJSON(source, c, r)
		if (ae == nil) != (be == nil) {
			t.Fatal("compile is not deterministic")
		}
		if ae != nil {
			var x, y *dynamic.CompileError
			if a != nil || b != nil || !errors.As(ae, &x) || !errors.As(be, &y) || x.Stage() != y.Stage() || x.Index() != y.Index() || x.Error() != y.Error() || x.Unwrap().Error() != y.Unwrap().Error() {
				t.Fatal("partial or inconsistent compile failure")
			}
			return
		}
		xEngine, err := rulite.NewEngineFromRuleSet(a)
		if err != nil {
			t.Fatal(err)
		}
		yEngine, err := rulite.NewEngineFromRuleSet(b)
		if err != nil {
			t.Fatal(err)
		}
		x, y := price{VIP: true}, price{VIP: true}
		xr, xe := xEngine.Fire(context.Background(), &x)
		yr, ye := yEngine.Fire(context.Background(), &y)
		if !reflect.DeepEqual(x, y) || xr.Counts() != yr.Counts() || xr.StopReason() != yr.StopReason() || (xe == nil) != (ye == nil) {
			t.Fatal("bounded execution is not repeatable")
		}
		if xe != nil && (xr.Counts().ConditionFailed != 1 || xr.Counts().Matched != xr.Counts().Fired || xr.StopReason() != rulite.StopConditionError) {
			t.Fatal("condition error became an ordinary miss")
		}
		if xe == nil {
			// Every successful action adds exactly its decoded integer rate.
			expected := 0
			for _, id := range xr.Fired() {
				for _, d := range ds {
					if d.ID == id {
						var p discountParams
						if err := json.Unmarshal(d.Params, &p); err != nil {
							t.Fatal(err)
						}
						expected += p.Rate
					}
				}
			}
			if x.Discount != expected {
				t.Fatal("action parameter oracle failed")
			}
		}
	})
}

func FuzzDefinitionFieldsAndParams(f *testing.F) {
	for _, seed := range []struct{ id, priority, params string }{
		{"pricing/vip", "0", `{"rate":20}`}, {"Invalid", "0", `{}`},
		{"pricing/vip", "1.5", `{}`}, {"pricing/vip", "-2147483649", `{}`},
		{"pricing/vip", "0", `{"rate":101}`}, {"pricing/vip", "0", `{"Rate":20}`},
		{"pricing/vip", "0", `{"rate":1,"\u0072ate":2}`},
		{"pricing/vip", "0", `{"limits":{"x":1,"x":2}}`},
		{"pricing/vip", "0", strings.Repeat(`{"next":`, 17) + `0` + strings.Repeat("}", 17)},
	} {
		f.Add(seed.id, seed.priority, seed.params)
	}
	c, r := compiler(f), registry(f)
	f.Fuzz(func(t *testing.T, id, priority, params string) {
		if len(id) > 160 || len(priority) > 40 || len(params) > 1024 || strings.Count(params, "{")+strings.Count(params, "[") > 32 {
			t.Skip()
		}
		quoted, _ := json.Marshal(id)
		source := []byte(`[{"id":` + string(quoted) + `,"priority":` + priority + `,"when":"true","action":"pricing.apply/v1","params":` + params + `}]`)
		first, err := dynamic.CompileJSON(source, c, r)
		second, again := dynamic.CompileJSON(source, c, r)
		if (err == nil) != (again == nil) {
			t.Fatal("field validation is not deterministic")
		}
		if err != nil {
			if first != nil || second != nil || err.Error() != again.Error() {
				t.Fatal("failed field validation published a set")
			}
			return
		}
		var p discountParams
		if err := json.Unmarshal([]byte(params), &p); err != nil || p.Rate < 0 || p.Rate > 100 {
			t.Fatal("invalid typed parameters accepted")
		}
		engine, err := rulite.NewEngineFromRuleSet(first)
		if err != nil {
			t.Fatal(err)
		}
		input := price{}
		result, err := engine.Fire(context.Background(), &input)
		if err != nil || result.Counts().Fired != 1 || input.Discount != p.Rate {
			t.Fatal("integer parameter oracle failed")
		}
	})
}
