# Rulite

Rulite is a lightweight, type-safe rules engine for deterministic, explainable business decisions in Go.

- **Ordinary Go types:** conditions and actions share your typed business state.
- **Deterministic execution:** priority first, registration order for ties.
- **Explainable outcomes:** inspect matches, successful actions, failures, and stops.
- **Code-first rules:** compose ordinary functions with `All`, `Any`, and `Not`.
- **Production semantics:** explicit errors, context boundaries, panic handling, and concurrency guarantees.

```go
package main

import (
    "context"
    "fmt"

    "github.com/imbrooklyn/rulite"
)

type Price struct {
    VIP      bool
    Discount int
}

func main() {
    vip := rulite.NewRule[Price]("pricing/vip").Priority(100).
        When(func(_ context.Context, p *Price) (bool, error) {
            return p.VIP, nil
        }).Then(func(_ context.Context, p *Price) error {
            p.Discount = 20
            return nil
        })
    engine, err := rulite.NewEngine(vip)
    if err != nil { panic(err) }
    price := Price{VIP: true}
    result, err := engine.Fire(context.Background(), &price)
    if err != nil { panic(err) }
    fmt.Printf("Discount: %d%%\n", price.Discount)
    fmt.Print(result.Explain())
}
```

## Install

Requires **Go 1.27+**. The root runtime depends only on the Go standard library.

```sh
go get github.com/imbrooklyn/rulite
```

## Why not if/else?

A few simple, stable `if` statements are fine. Rulite helps when dozens of business rules need priorities, first-match or first-fire selection, consistent error handling, and an explanation of what happened. It organizes those decisions while keeping business logic in Go.

## Execution essentials

`NewRule[T](id).Priority(n).When(condition).Then(action)` builds an immutable rule. `Priority` is optional and defaults to zero. `NewEngine(rules...)` validates IDs and callbacks, then freezes priority descending and registration order ascending. IDs are unique, 1-128 bytes, and match `^[a-z0-9][a-z0-9._/-]{0,127}$`.

Each condition runs immediately before its matched action. **Conditions must only read input.** This is a contract, not a Go type-system restriction. **Actions may mutate input or perform side effects.** Later conditions see earlier action changes. The engine does not roll back, retry, or compensate, including after an error or panic.

A rule is **Matched** only when its condition returns `true, nil`, and **Fired** only when its action returns `nil`. Fired does not prove a field was changed. For field attribution, record business provenance such as `AppliedBy rulite.RuleID`; see [pricing](examples/pricing).

The default policy evaluates all rules and stops on either phase's first error. For provider fallback:

```go
policy := rulite.DefaultPolicy().
    WithStop(rulite.StopOnFirstFire).
    WithActionErrors(rulite.ContinueOnError)
result, err := engine.Fire(ctx, &input, rulite.WithPolicy(policy))
```

`StopOnFirstMatch` stops after the first matched action attempt, even when that action fails under `ContinueOnError`. `StopOnFirstFire` can try the next rule after a continued action error. Both stop the entire execution. Earlier errors remain in `err` even when fallback succeeds; always inspect both `Result` and `error`.

`result.Explain()` is always available. `result.Rule(id)` exposes one rule's outcome, order, and skip or non-evaluation reason. Add `rulite.WithTrace()` to `Fire` for timings and built-in combinator child outcomes. Text explanations are for people; use structured accessors for integration.

Business panics are recovered and terminal by default. Context cancellation is checked at callback boundaries; callbacks must cooperate to return promptly. Engine and completed Result values support concurrent use. Each Fire needs independently owned input, or caller synchronization around the entire execution. Callers also synchronize shared callback captures and treat returned error objects and panic payloads as read-only.

## Reusable rule sets

`NewEngine(rules...)` remains the shortest path. Use `Compile` when several engines should share one immutable rule set:

```go
set, err := rulite.Compile(vip)
if err != nil { panic(err) }
engine, err := rulite.NewEngineFromRuleSet(set,
    rulite.WithPolicy(rulite.DefaultPolicy().WithStop(rulite.StopOnFirstFire)))
if err != nil { panic(err) }
```

Both construction paths produce identical validation issues, priority order, registration indexes, and execution results with the same options. Each engine has independent defaults; per-call `Fire` options apply to a copy. Reusing a set does not repeat rule validation or sorting. `Compile[T]()` creates a valid empty set; nil and zero sets are rejected by the engine constructor.

Before `When`, optionally add `Name`, `Description`, or `Tags` to the builder. Names and descriptions have surrounding whitespace removed. Tags are trimmed, empty values dropped, and exact duplicates removed in first-occurrence order. Metadata never changes RuleID identity. `set.Rule(id)` returns immutable `RuleInfo` and a presence boolean; `set.Rules()` lists metadata in execution order. Tags and view slices are defensive copies. Result, Explain, and Trace rule views expose the same descriptive metadata. See the compiling [reusable pricing example](ruleset_example_test.go), which uses only the root package and demonstrates both construction paths.

## Local selection groups

Use `FirstMatchGroup(id, members...)` for one eligible rule or `FirstFireGroup(id, members...)` for provider fallback. Both return immutable `Group[T]` values. Assemble mixed definitions with `CompileEntries(providers.Entry(), audit.Entry())`, then use `NewEngineFromRuleSet(set)`. The [compiling payment example](group_example_test.go) selects a provider and runs the following audit rule in the same Fire.

Top-level rules and groups sort by priority descending, then registration order. Members sort independently inside each group. Group priority defaults to zero; `WithPriority` sets it explicitly without inspecting member priorities. RuleID is unique across the whole set; GroupID follows the same syntax in an independent namespace. Empty groups are valid and exhaust without selecting a rule.

First-match attempts the selected action once; even a continued action error resolves that group. First-fire resolves only after an action returns nil; `ContinueOnError` permits fallback after partial effects. Both inherit global condition/action error modes. The default still stops the entire execution on error. Global first-match/first-fire policies also stop the entire execution, with precedence **panic, observed context cancellation, error stop, global selection, local advancement, completion**.

`result.Group(id)` and `result.Explain().Groups()` report local selection facts independently of the global stop. Remaining members bypassed by local advancement are not evaluated with `NotEvaluatedGroupResolved`; they are never reported as unmatched or matched-and-skipped. If global termination wins at the selection boundary, untouched members use `NotEvaluatedExecutionStopped`, while the selection fact remains visible. Rule views distinguish flattened `Order`, `TopLevelOrder`, top-level `RegistrationIndex`, and optional local `MemberIndex`. See the [group contract](docs/architecture.md#local-selection-groups).

## Observation and diagnostics

Use `WithObserver` on `Fire` or `NewEngineFromRuleSet` to receive synchronous ordered events through `Observer` or `ObserverFunc`. Events expose metadata and execution facts without input or executable callbacks. An observer error or recovered panic adds a separate `result.Diagnostics()` entry and disables observation for that execution; business evaluation continues under the same policy. The next execution enables the observer again. `PropagatePanics` also applies to observers.

Observers manage their own latency, backpressure, and synchronization when shared by concurrent Fire calls. Cancellation of the caller's context and captured-state side effects still take effect. The [observation example](observer_example_test.go) collects read-only facts and inspects an export diagnostic after a successful business result. See the [event contract](docs/architecture.md#observation-and-diagnostics) for ordering, field validity, and timing.

## Runnable examples

From a checkout:

```sh
go run ./examples/pricing
go run ./examples/payment_routing
go run ./examples/risk_decision
go test ./...
```

- [Pricing](examples/pricing): VIP, new-customer, and high-value offers with field provenance.
- [Payment routing](examples/payment_routing): Stripe, Adyen, PayPal, and bank-transfer fallback using local provider stubs.
- [Risk decision](examples/risk_decision): allow, review, reject, nested conditions, Trace, and a conservative default when evidence fails.

## Scope and documentation

Rulite provides typed rules, immutable RuleSet/Compile and engines, descriptive metadata, indexed validation issues, deterministic single-pass execution, policies, Result, Explain, opt-in Trace, synchronous observation, and isolated diagnostics. Basic v0.3 first-match/first-fire groups, mixed entry compilation, and structured group results are available. Broader group diagnostics and business examples remain planned. CI checks structural performance guarantees and records repeatable benchmark comparisons. CEL, dynamic definitions, hot reload, and telemetry integration are not implemented. A separate CEL Condition adapter is a required v0.4 deliverable.

Read [architecture and non-goals](docs/architecture.md), the [roadmap](docs/roadmap.md), [benchmark methodology and baseline](docs/benchmarks.md), and [contributing](CONTRIBUTING.md). Later version goals are plans, not available APIs. Rulite does not replace all conditionals or provide inference, a rule language, workflow orchestration, or automatic rollback.

Licensed under [Apache-2.0](LICENSE).
