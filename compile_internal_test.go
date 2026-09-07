package rulite

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"
)

type callbackState struct {
	condition RuleID
	action    RuleID
}

func orderedRule(id RuleID, priority Priority) Rule[callbackState] {
	return NewRule[callbackState](id).Priority(priority).
		When(func(_ context.Context, state *callbackState) (bool, error) {
			return state.condition == id, nil
		}).
		Then(func(_ context.Context, state *callbackState) error {
			state.action = id
			return nil
		})
}

func checkSnapshot(t *testing.T, engine *Engine[callbackState], rules []Rule[callbackState]) {
	t.Helper()
	if engine == nil || engine.snapshot == nil || engine.snapshot.metadata == nil {
		t.Fatal("constructed engine has no snapshot metadata")
	}
	snapshot := engine.snapshot
	metadata := snapshot.metadata
	if len(metadata.rules) != len(rules) || len(snapshot.callbacks) != len(rules) || len(metadata.byID) != len(rules) {
		t.Fatal("snapshot sizes do not match the supplied rules")
	}
	seen := make([]bool, len(rules))
	for order, entry := range metadata.rules {
		index := entry.registrationIndex
		if index < 0 || index >= len(rules) || seen[index] {
			t.Fatalf("order %d: invalid or repeated registration index %d", order, index)
		}
		seen[index] = true
		if entry.id != rules[index].ID() || entry.priority != rules[index].Priority() {
			t.Fatalf("order %d: metadata differs from registered rule %d", order, index)
		}
		if indexedOrder, ok := metadata.byID[entry.id]; !ok || indexedOrder != order {
			t.Fatalf("ID %q: lookup = %d, %t; want order %d", entry.id, indexedOrder, ok, order)
		}
		if order > 0 {
			previous := metadata.rules[order-1]
			if previous.priority < entry.priority || previous.priority == entry.priority && previous.registrationIndex > index {
				t.Fatalf("entries out of order: %+v before %+v", previous, entry)
			}
		}
		state := &callbackState{condition: entry.id}
		matched, err := snapshot.callbacks[order].condition(context.Background(), state)
		if !matched || err != nil {
			t.Fatalf("order %d: condition does not correspond to %q: %t, %v", order, entry.id, matched, err)
		}
		if err := snapshot.callbacks[order].action(context.Background(), state); err != nil || state.action != entry.id {
			t.Fatalf("order %d: action does not correspond to %q: %q, %v", order, entry.id, state.action, err)
		}
	}
}

func TestCompiledOrdering(t *testing.T) {
	rules := []Rule[callbackState]{
		orderedRule("default/first", DefaultPriority),
		orderedRule("high/first", 100),
		orderedRule("minimum/first", math.MinInt32),
		orderedRule("high/second", 100),
		orderedRule("negative", -10),
		orderedRule("maximum/first", math.MaxInt32),
		orderedRule("default/second", DefaultPriority),
		orderedRule("maximum/second", math.MaxInt32),
		orderedRule("minimum/second", math.MinInt32),
	}
	engine, err := NewEngine(rules...)
	if err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, engine, rules)
	want := []int{5, 7, 1, 3, 0, 6, 4, 2, 8}
	for order, entry := range engine.snapshot.metadata.rules {
		if entry.registrationIndex != want[order] {
			t.Errorf("order %d: registration index = %d; want %d", order, entry.registrationIndex, want[order])
		}
	}
}

func TestOrderingProperties(t *testing.T) {
	random := rand.New(rand.NewPCG(29, 71))
	for trial := 0; trial < 200; trial++ {
		rules := make([]Rule[callbackState], random.IntN(100))
		for index := range rules {
			priority := Priority(int32(random.Uint32()))
			switch index % 5 {
			case 0:
				priority = Priority(random.IntN(5) - 2)
			case 1:
				priority = math.MinInt32
			case 2:
				priority = math.MaxInt32
			}
			rules[index] = orderedRule(RuleID(fmt.Sprintf("rule/%d", index)), priority)
		}
		random.Shuffle(len(rules), func(a, b int) { rules[a], rules[b] = rules[b], rules[a] })
		var previous []ruleMetadata
		for attempt := 0; attempt < 3; attempt++ {
			engine, err := NewEngine(rules...)
			if err != nil {
				t.Fatalf("trial %d: %v", trial, err)
			}
			checkSnapshot(t, engine, rules)
			if attempt > 0 && !slices.Equal(previous, engine.snapshot.metadata.rules) {
				t.Fatalf("trial %d: order changed across identical constructions", trial)
			}
			previous = engine.snapshot.metadata.rules
		}
	}
}

func TestSnapshotOwnsDefinitions(t *testing.T) {
	rules := []Rule[callbackState]{orderedRule("low", -10), orderedRule("high", 100)}
	original := slices.Clone(rules)
	engine, err := NewEngine(rules...)
	if err != nil {
		t.Fatal(err)
	}
	for index, rule := range rules {
		if rule.ID() != original[index].ID() || rule.Priority() != original[index].Priority() {
			t.Fatal("construction changed the input rule order")
		}
	}
	rules[0], rules[1] = Rule[callbackState]{}, orderedRule("replacement", math.MaxInt32)
	checkSnapshot(t, engine, original)
}

func TestConstructionDoesNotInvokeCallbacks(t *testing.T) {
	rule := NewRule[callbackState]("pricing/discount").
		When(func(context.Context, *callbackState) (bool, error) {
			t.Fatal("construction invoked a condition")
			return false, nil
		}).
		Then(func(context.Context, *callbackState) error {
			t.Fatal("construction invoked an action")
			return nil
		})
	if _, err := NewEngine(rule); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(rule, rule); err == nil {
		t.Fatal("duplicate rule was accepted")
	}
	composed := NewRule[callbackState]("pricing/composed").When(All[callbackState](nil)).Then(rule.action)
	if _, err := NewEngine(composed); err != nil {
		t.Fatalf("construction inspected a composed condition: %v", err)
	}
}

func TestBuilderCallbacksAreIndependent(t *testing.T) {
	first := orderedRule("first", 0)
	second := orderedRule("second", 0)
	builder := NewRule[callbackState]("shared")
	firstStage := builder.When(first.condition)
	secondStage := builder.When(second.condition)
	rule := firstStage.Then(first.action)
	copyOfRule := rule
	_ = firstStage.Then(second.action)
	_ = secondStage.Then(first.action)
	state := &callbackState{condition: "first"}
	if matched, err := copyOfRule.condition(context.Background(), state); !matched || err != nil {
		t.Fatal("reusing a builder changed a completed condition")
	}
	if err := copyOfRule.action(context.Background(), state); err != nil || state.action != "first" {
		t.Fatal("reusing a builder changed a completed action")
	}
}

func TestEmptySnapshotAndZeroEngine(t *testing.T) {
	var zero Engine[callbackState]
	if zero.snapshot != nil {
		t.Fatal("zero engine has a snapshot")
	}
	engine, err := NewEngine[callbackState]()
	if err != nil {
		t.Fatal(err)
	}
	checkSnapshot(t, engine, nil)
}

func TestConcurrentConstructionAndSnapshotReads(t *testing.T) {
	rules := make([]Rule[callbackState], 64)
	for index := range rules {
		rules[index] = orderedRule(RuleID(fmt.Sprintf("rule/%d", index)), Priority(index%7-3))
	}
	shared, err := NewEngine(rules...)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		workers.Go(func() {
			for attempt := 0; attempt < 20; attempt++ {
				engine, err := NewEngine(rules...)
				if err != nil {
					t.Error(err)
					return
				}
				if !slices.Equal(engine.snapshot.metadata.rules, shared.snapshot.metadata.rules) {
					t.Error("concurrent construction changed snapshot order")
					return
				}
				for order, entry := range shared.snapshot.metadata.rules {
					if shared.snapshot.metadata.byID[entry.id] != order || rules[entry.registrationIndex].ID() != entry.id {
						t.Error("concurrent snapshot read found inconsistent metadata")
						return
					}
				}
			}
		})
	}
	workers.Wait()
}
