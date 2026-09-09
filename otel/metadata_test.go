package otel_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/imbrooklyn/rulite"
	ruliteotel "github.com/imbrooklyn/rulite/otel"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRulePriorityAndOrderAttributes(t *testing.T) {
	x := newTelemetry(t)
	build := func(id rulite.RuleID, priority rulite.Priority) rulite.Rule[state] {
		return rulite.NewRule[state](id).Priority(priority).
			When(func(context.Context, *state) (bool, error) { return false, nil }).
			Then(func(context.Context, *state) error { t.Fatal("unmatched action called"); return nil })
	}
	e := engine(t, "", build("low", -7), build("first", 42), build("second", 42))
	if _, err := x.adapter.Fire(context.Background(), e, &state{}); err != nil {
		t.Fatal(err)
	}
	events := x.exporter.GetSpans()[0].Events
	if len(events) != 3 {
		t.Fatal("unexpected event count")
	}
	for i, id := range []string{"first", "second", "low"} {
		priority := int64(42)
		if i == 2 {
			priority = -7
		}
		if attr(events[i].Attributes, "rulite.rule.id").AsString() != id || attr(events[i].Attributes, "rulite.rule.priority").AsInt64() != priority || attr(events[i].Attributes, "rulite.rule.order").AsInt64() != int64(i) {
			t.Fatal("priority or registration ordering lost in export")
		}
	}
}

func TestAllowlistBoundsAndDefaultEventLimit(t *testing.T) {
	versions := make([]rulite.RuleSetVersion, 16)
	ids := make([]rulite.RuleID, 64)
	outcomes := make([]byte, 64)
	for i := range versions {
		versions[i] = rulite.RuleSetVersion(fmt.Sprintf("v/%d", i))
	}
	for i := range ids {
		ids[i] = rulite.RuleID(fmt.Sprintf("rule/%d", i))
		outcomes[i] = 1
	}
	x := newTelemetry(t, ruliteotel.WithMetricVersions(versions...), ruliteotel.WithRuleMetrics(ids...), ruliteotel.WithTraceRules(ids...))
	r, err := x.adapter.Fire(context.Background(), engine(t, versions[15], rules(outcomes, nil)...), &state{})
	if err != nil || r.Counts().Fired != 64 || len(r.Diagnostics()) != 1 || !errors.Is(r.Diagnostics()[0], ruliteotel.ErrEventLimit) {
		t.Fatal("default limit changed business or rejected maximum allowlist")
	}
	s := x.exporter.GetSpans()[0]
	if len(s.Events) != 128 || attr(s.Attributes, "rulite.events.dropped").AsInt64() != 64 {
		t.Fatal("default event bound changed")
	}
	m := x.collect(t)
	if sum(t, m, "rulite.rule.events", "{event}") != 192 || len(m["rulite.rule.events"].Data.(metricdata.Sum[int64]).DataPoints) != 192 {
		t.Fatal("allowlist boundary lost rule events")
	}
	d := m["rulite.execution.count"].Data.(metricdata.Sum[int64])
	if attr(d.DataPoints[0].Attributes.ToSlice(), "rulite.ruleset.version").AsString() != "v/15" {
		t.Fatal("last allowed version was dropped")
	}
}
