# Atomic runtime snapshots

`Runtime[T]` publishes complete immutable engines through the root package, which depends only on the standard library. A Fire call loads one publication at entry and uses its rules, defaults, and identity until it returns. Publication during a callback cannot mix versions within that execution. Calls that capture after a publication use the new snapshot; overlapping calls can finish in either order.

## Construct and reload

```go
package main

import (
    "context"
    "fmt"

    "github.com/imbrooklyn/rulite"
)

type Price struct { Discount int }

func build(version rulite.RuleSetVersion, discount int) (*rulite.Engine[Price], error) {
    rule := rulite.NewRule[Price]("pricing/offer").
        When(func(context.Context, *Price) (bool, error) { return true, nil }).
        Then(func(_ context.Context, p *Price) error { p.Discount = discount; return nil })
    set, err := rulite.Compile(rule)
    if err != nil { return nil, err }
    set, err = set.WithIdentity(version, "")
    if err != nil { return nil, err }
    return rulite.NewEngineFromRuleSet(set)
}

func main() {
    initial, err := build("pricing/v1", 10)
    if err != nil { panic(err) }
    runtime, err := rulite.NewRuntime(initial)
    if err != nil { panic(err) }

    next, err := build("pricing/v2", 20)
    if err != nil { panic(err) }
    if _, err := runtime.Publish(next); err != nil { panic(err) }

    price := Price{}
    result, err := runtime.Fire(context.Background(), &price)
    if err != nil { panic(err) }
    fmt.Println(price.Discount, result.Snapshot().Version(), result.Snapshot().Revision())
}
```

Output is `20 pricing/v2 2`. The [external-package example](../runtime_example_test.go) also checks type inference. `NewEngine` remains the shortest route when business version metadata is unnecessary. `NewRuntime(engine)` infers `T`; the empty constructor `NewEngine[T]()` needs an explicit type.

For [dynamic definitions](dynamic-rules.md), the caller acquires source bytes, calls `dynamic.CompileJSON`, assigns identity with `set.WithIdentity`, constructs an engine with `NewEngineFromRuleSet`, then calls `Publish`. Return any acquisition, decoding, compilation, validation, or engine-option error before Publish. The current engine remains usable throughout this work and after failure. Runtime accepts only a complete engine, never source bytes, individual rules, or a mutable configuration DTO. Source authenticity, digest calculation, acquisition limits, retry policy, and deciding when to reload belong to the caller.

## Reloading grouped definitions

The [payment reload example](../examples/runtime_reload) loads local JSON through `dynamic.Decode` and `dynamic.CompileRules`, combines the returned rules with a typed fallback in a provider group and a subsequent typed audit, then calls `CompileEntries`, `WithIdentity`, `NewEngineFromRuleSet`, and `Publish`. Group layout and typed behavior are application code; the strict Definition schema remains flat. CEL conditions read preceding action mutations within the same pass. No configuration acquisition service or watcher is involved.

```sh
go run ./examples/runtime_reload
go test -race ./examples/runtime_reload -run '^(TestReloadOldActionOutcomes|TestReloadConcurrentPublicationsAndViews|TestCallerSynchronizesReloadInput)$' -count=10 -timeout=120s
```

| Boundary | Available behavior |
| --- | --- |
| JSON syntax/schema, CEL syntax/type, unknown action, duplicate ID, or parameter validation failure | No engine is published; the complete current snapshot remains available and its revision is unchanged. |
| Old action returns nil after publication | Old Result retains its fired outcome, old version/revision, and any following audit from the old snapshot. |
| Old action returns an error or panics | Its partial mutations and canonical failure remain associated with the old snapshot; policy determines continuation, with recovered panic always terminal. |
| Context is canceled during an old callback | Fire waits for the callback boundary, retains the actual action outcome and context cause, and reports the captured old identity. |
| Observer or telemetry delivery fails | Independent diagnostics can truncate observation; they cannot turn a telemetry fault into a rule failure or change selection. |

The example uses local provider stubs. A failed provider attempt remains visible when fallback succeeds; it could already have performed an external effect in a real application. There is no automatic transaction, retry, rollback, or compensation. `AppliedBy` identifies the final field writer, while snapshot identity identifies execution source. A successful action may preserve an existing field value and its earlier attribution.

## API and initial state

```go
func NewRuntime[T any](initial *Engine[T]) (*Runtime[T], error)
func (r *Runtime[T]) Publish(next *Engine[T]) (SnapshotInfo, error)
func (r *Runtime[T]) Fire(ctx context.Context, input *T, options ...FireOption) (Result, error)
func (r *Runtime[T]) Snapshot() SnapshotInfo
func (s *RuleSet[T]) WithIdentity(version RuleSetVersion, digest SourceDigest) (*RuleSet[T], error)
```

`NewRuntime` publishes the initial engine at revision one. A successfully compiled empty RuleSet or empty engine is valid. With an active context, firing it returns an executed, completed Result with zero counts and the captured identity.

A zero Runtime is unpublished: `Snapshot()` returns zero metadata, `Fire` returns `ErrInvalidRuntime`, and its first successful `Publish` assigns revision one. A nil Runtime rejects Publish with `ErrInvalidRuntime`. Nil or zero engines are rejected with `ErrInvalidEngine`; failed construction returns a nil Runtime. Publish failures return zero metadata and leave the previous publication unchanged. Nil and zero RuleSets reject `WithIdentity` with `ErrInvalidRuleSet`.

Do not copy Runtime after first use. Share its pointer. RuleSet and Engine remain immutable and may still be shared or copied as before. Each publication includes all engine defaults: policy, panic mode, trace mode, and Observer. Per-Fire options override a captured value copy under the existing left-to-right validation rules.

Fire preflight checks nil context, nil input, unpublished/nil Runtime, then options. Failure returns a zero Result with zero identity and no events. Once started, the ordinary [execution semantics](architecture.md) apply, including empty or already-canceled calls, partial Results, canonical `Failure`/`ExecutionError`, and context causes. Panic propagation makes no promise of a Result or finish event.

## Identity and publication order

| Value | Contract |
| --- | --- |
| `RuleSetVersion` | Caller-supplied business version, compared byte for byte; empty means unspecified. Rulite neither assigns nor orders business versions. |
| `SnapshotRevision` | `uint64` publication sequence within one Runtime, starting at one. Direct Engine execution and unpublished/zero views use zero. |
| `SourceDigest` | Optional caller-supplied digest with caller-defined format and algorithm; empty means unspecified. Rulite does not verify it. |

`WithIdentity` returns a new RuleSet wrapper sharing the already compiled executable nodes. It copies the two identity strings, preserving every byte without normalization; substring arguments do not keep a larger source buffer alive. It does not change the original set or invoke callbacks. Identity values impose no syntax, uniqueness, authenticity, or digest verification requirement.

`SnapshotInfo` has private fields and value getters `Version()`, `Revision()`, and `SourceDigest()`. `RuleSet.Snapshot()` and `Engine.Snapshot()` return business identity with revision zero. `Result.Snapshot()`, `Trace.Snapshot()`, `Explanation.Snapshot()`, and every delivered `Event.Snapshot()` report the actual captured publication, including start/finish, rule, group, failure, and diagnostic events. Zero views return zero metadata. These values can be retained and read concurrently without retaining executable code.

Publishers serialize revision assignment and the atomic store together. That store is the publication linearization point. Every successful Publish increments the revision, including publishing the same engine or business version again. Failed Publish consumes no revision. At the maximum `uint64`, Publish returns `ErrRevisionExhausted` and the current engine stays available. Revisions never wrap or decrease within that Runtime.

Overlapping publishers can return in a different order from their stores; their returned revisions identify the actual publication order. A slower source acquisition can publish older business content later with a larger revision. Applications enforce source freshness if needed. Independent Runtime instances and process restarts have independent sequences; revisions are not distributed ordering or global identity. Rulite does not hash Go closures, and a digest does not replace a business version.

`Runtime.Snapshot()` reads the identity current at that accessor's atomic load. Another publication may precede a later Fire. Use `Result.Snapshot()` to attribute an execution, and combine version, revision, and any application runtime identity as needed. No mutable current engine or callback handle is exposed.

## Concurrency and resource ownership

Fire reuses the Engine execution state machine: one pass, sequential condition then action, priority descending and registration order ascending. Conditions remain read-only; actions can mutate state and have partial effects. Publication adds no rollback, retries, input cloning, or automatic parallel evaluation.

Use independently owned inputs for concurrent Fire calls. The caller synchronizes a shared mutable input around the entire call, and synchronizes shared callback captures, Observer state, and integration callbacks. Publication never waits for an old Fire to finish. Synchronous callbacks and observers may themselves publish or start a nested Fire; nested calls capture independently.

Runtime holds only its current executable publication. Active executions and other engine/RuleSet owners may retain older callbacks. Once those references are gone, old executable snapshots can be collected. Result, Explanation, Trace, Event, and SnapshotInfo retain only independent metadata and facts, with no references to inputs, contexts, registries, observers, or executable callbacks. Caller-supplied errors and panic values remain read-only references and can themselves retain application objects.

Runtime starts no goroutines, watchers, or external resources, so it has no Close method. It does not close resources captured by callbacks after publication. Callers keep those resources usable for active executions and coordinate their shutdown themselves; publication is not a drain notification. Garbage collection timing is not an API guarantee.

The core reads no duration clock with Trace disabled, and ordinary no-trace/no-observer all-miss Fire keeps the sparse allocation contract. An explicitly configured Observer or enabled OTel duration histogram can measure its own interval. See [scale, swap and retention measurements](benchmarks.md#snapshot-scale-and-retention) for direct Engine, Runtime, publication, and resource costs.

Retaining Results from many distinct snapshots normally retains their descriptive metadata and execution facts. It does not keep their executable closures alive. Bound application history and trace retention separately from publication rate; Core Trace is complete and can retain every rule's timing storage. Root identity strings are opaque and unbounded, so callers should choose reasonable sizes. The provider owner separately bounds telemetry buffering and export storage. Collection of Go objects never closes a socket, drains a provider, or cancels an active callback on the caller's behalf.

## Scope

Runtime provides atomic replacement of complete snapshots. It does not implement in-place Engine mutation, per-rule Add/Remove, remote configuration, a file watcher platform, distributed control planes, automatic rollout or rollback orchestration, telemetry export, Infer, or incremental evaluation. The separate [OpenTelemetry adapter](observability.md) accepts a Runtime in its Fire wrapper and associates telemetry with captured event identity. Its provider lifecycle and bounded export configuration remain outside Runtime.
