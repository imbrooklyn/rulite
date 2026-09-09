package otel_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
)

// FuzzTelemetryProjection compares bounded export with the canonical completed
// ledger, including partial failures and all global policies. At most 16 rules
// and 32 exported events are accepted. The event oracle reads immutable rule
// facts rather than duplicating the execution state machine.
func FuzzTelemetryProjection(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3}, uint8(9), uint8(0), false)
	f.Add([]byte{1, 3, 1}, uint8(1), uint8(11), true)
	f.Add([]byte{4}, uint8(0), uint8(4), false)
	f.Add([]byte{5, 0}, uint8(3), uint8(8), true)
	f.Add([]byte{}, uint8(0), uint8(0), false)
	f.Fuzz(func(t *testing.T, outcomes []byte, limitByte, policyByte uint8, filter bool) {
		if len(outcomes) > 16 {
			return
		}
		limit := int(limitByte % 33)
		options := []ruliteotel.Option{ruliteotel.WithEventLimit(limit), ruliteotel.WithTraceVersions()}
		if filter {
			var ids []rulite.RuleID
			for i := range len(outcomes) {
				if i%2 == 0 {
					ids = append(ids, rulite.RuleID(fmt.Sprintf("rule/%d", i)))
				}
			}
			options = append(options, ruliteotel.WithTraceRules(ids...))
		}
		x := newTelemetry(t, options...)
		cause := errors.New("private-business-error")
		e := engine(t, "fuzz", rules(outcomes, cause)...)
		policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(policyByte % 3)).WithConditionErrors(rulite.ErrorMode(policyByte / 3 % 2)).WithActionErrors(rulite.ErrorMode(policyByte / 6 % 2))
		fireOptions := []rulite.FireOption{rulite.WithPolicy(policy), rulite.WithTrace()}
		plainInput, actualInput := state{}, state{}
		plain, plainErr := e.Fire(context.Background(), &plainInput, fireOptions...)
		r, err := x.adapter.Fire(context.Background(), e, &actualInput, fireOptions...)
		if r.Counts() != plain.Counts() || r.StopReason() != plain.StopReason() || actualInput != plainInput || (err != nil) != (plainErr != nil) {
			t.Fatal("export changed execution")
		}
		type fact struct {
			name, id string
			order    int
		}
		var want []fact
		filtered := 0
		for i, rule := range plain.Explain().Rules() {
			if !rule.Evaluated() {
				continue
			}
			names := []string{"rule-evaluated"}
			if rule.Matched() {
				names = append(names, "rule-matched")
			}
			if rule.Fired() {
				names = append(names, "rule-fired")
			}
			if rule.Error() != nil {
				names = append(names, "rule-failed")
			}
			if filter && i%2 != 0 {
				filtered += len(names)
				continue
			}
			for _, name := range names {
				want = append(want, fact{name, string(rule.ID()), i})
			}
		}
		spans := x.exporter.GetSpans()
		if len(spans) != 1 {
			t.Fatal("execution span missing")
		}
		s := spans[0]
		emitted := min(limit, len(want))
		if len(s.Events) != emitted || attr(s.Attributes, "rulite.events.filtered").AsInt64() != int64(filtered) || attr(s.Attributes, "rulite.events.dropped").AsInt64() != int64(len(want)-emitted) {
			t.Fatal("bounded event projection differs from ledger")
		}
		for i, event := range s.Events {
			if event.Name != want[i].name || attr(event.Attributes, "rulite.rule.id").AsString() != want[i].id || attr(event.Attributes, "rulite.rule.order").AsInt64() != int64(want[i].order) {
				t.Fatal("ordered event prefix differs from ledger")
			}
		}
		if emitted < len(want) {
			if len(r.Diagnostics()) != 1 || !errors.Is(r.Diagnostics()[0], ruliteotel.ErrEventLimit) {
				t.Fatal("missing independent limit diagnostic")
			}
		} else if len(r.Diagnostics()) != 0 {
			t.Fatal("unexpected diagnostic")
		}
		if errors.Is(err, ruliteotel.ErrEventLimit) {
			t.Fatal("export failure entered business error")
		}
		m := x.collect(t)
		for name, want := range map[string]int{"rulite.rule.evaluated": r.Counts().Evaluated, "rulite.rule.matched": r.Counts().Matched, "rulite.rule.fired": r.Counts().Fired, "rulite.rule.failed": r.Counts().Failed} {
			if sum(t, m, name, "{rule}") != int64(want) {
				t.Fatalf("%s differs from ledger", name)
			}
		}
		if sum(t, m, "rulite.execution.count", "{execution}") != 1 {
			t.Fatal("completion counter changed")
		}
	})
}
