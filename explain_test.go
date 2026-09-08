package rulite_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite"
)

func TestExplanationGolden(t *testing.T) {
	// This golden protects readability of the current rendering. Structural
	// assertions define the contract; exact text is not a v0.x compatibility API.
	cause := errors.New("provider unavailable")
	type scenario struct {
		name   string
		result rulite.Result
	}
	scenarios := []scenario{{name: "not_started"}}
	success := rulite.NewRule[executionInput]("success").When(constantCondition(true, nil)).Then(successfulAction)
	miss := rulite.NewRule[executionInput]("miss").When(constantCondition(false, nil)).Then(successfulAction)
	failure := rulite.NewRule[executionInput]("failure").When(constantCondition(true, nil)).Then(func(context.Context, *executionInput) error { return cause })
	conditionFailure := rulite.NewRule[executionInput]("condition-failure").When(constantCondition(true, cause)).Then(successfulAction)
	unreached := rulite.NewRule[executionInput]("unreached").When(constantCondition(true, nil)).Then(successfulAction)
	add := func(name string, ctx context.Context, policy rulite.ExecutionPolicy, rules ...rulite.Rule[executionInput]) {
		result, _ := mustEngine(t, rules...).Fire(ctx, &executionInput{}, rulite.WithPolicy(policy))
		checkResultConsistency(t, result)
		scenarios = append(scenarios, scenario{name, result})
	}
	active := context.Background()
	policy := rulite.DefaultPolicy()
	add("completed", active, policy, success, miss)
	add("completed_with_failure", active, policy.WithConditionErrors(rulite.ContinueOnError), conditionFailure, success)
	add("first_match", active, policy.WithStop(rulite.StopOnFirstMatch).WithActionErrors(rulite.ContinueOnError), failure, unreached)
	add("first_fire", active, policy.WithStop(rulite.StopOnFirstFire).WithActionErrors(rulite.ContinueOnError), failure, success, unreached)
	add("condition_error", active, policy, conditionFailure, unreached)
	add("action_error", active, policy, failure, unreached)
	ctx, cancel := context.WithCancel(active)
	defer cancel()
	skip := rulite.NewRule[executionInput]("skip").When(func(context.Context, *executionInput) (bool, error) { cancel(); return true, nil }).Then(successfulAction)
	add("context_skipped", ctx, policy.WithActionErrors(rulite.ContinueOnError), success, failure, miss, skip, unreached)
	add("context_before_condition", ctx, policy, unreached)
	deadline, release := context.WithDeadline(active, time.Now().Add(-time.Hour))
	defer release()
	add("deadline", deadline, policy, unreached)
	conditionPanic := rulite.NewRule[executionInput]("condition-panic").When(func(context.Context, *executionInput) (bool, error) { panic("private panic contents") }).Then(successfulAction)
	actionPanic := rulite.NewRule[executionInput]("action-panic").When(constantCondition(true, nil)).Then(func(context.Context, *executionInput) error { panic("private panic contents") })
	add("condition_panic", active, policy, conditionPanic, unreached)
	add("action_panic", active, policy, actionPanic, unreached)
	var output strings.Builder
	for _, scenario := range scenarios {
		fmt.Fprintf(&output, "=== %s ===\n%s\n", scenario.name, scenario.result.Explain())
	}
	got := strings.TrimSuffix(output.String(), "\n")
	if strings.Contains(got, "private panic contents") || strings.Contains(got, "runtime/debug") {
		t.Fatal("explanation disclosed panic value or stack")
	}
	want, err := os.ReadFile("testdata/explain.golden")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if got != string(want) {
		t.Fatalf("explanation rendering differs\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestStopReasonNames(t *testing.T) {
	names := []string{"none", "completed", "first-match", "first-fire", "condition-error", "action-error", "context-canceled", "context-deadline-exceeded", "panic"}
	for index, name := range names {
		if got := rulite.StopReason(index).String(); got != name {
			t.Errorf("reason %d = %q; want %q", index, got, name)
		}
	}
	if rulite.StopReason(255).String() != "unknown" {
		t.Fatal("invalid stop reason has a valid name")
	}
}
