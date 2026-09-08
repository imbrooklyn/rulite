# Architecture and semantics

Rulite executes typed business rules in one ordered pass. Consumers import `github.com/imbrooklyn/rulite`; the runtime uses only the Go standard library.

## Definitions and construction

A `Rule[T]` contains a stable `RuleID`, `Priority`, descriptive metadata, `Condition[T]`, and `Action[T]`. The staged builder guides completion: `NewRule[T](id)`, optional `Priority`, `Name`, `Description`, and `Tags`, then `When` and `Then`. Builders return independent values; `Rule`, `RuleSet`, and `Engine` expose no mutable rule collections.

Use a non-pointer business type for `T`. Conditions and actions receive `*T`:

```go
type Condition[T any] func(context.Context, *T) (bool, error)
type Action[T any] func(context.Context, *T) error
```

`Compile` is the rule validation boundary; `NewEngine` calls it and constructs an engine with default configuration. A `RuleID` must contain 1-128 bytes matching `^[a-z0-9][a-z0-9._/-]{0,127}$`. There is no trimming, normalization, or case conversion. Duplicate valid IDs and nil callbacks are rejected. Builders defer validation and do not panic for invalid definitions.

Construction returns all issues as `*ValidationError`, scanning registration order. Within each rule, issue order is ID syntax, duplicate valid ID, nil condition, nil action. Invalid IDs do not participate in duplicate detection. The second and later occurrences of a valid ID refer to their own registration index and identify the first occurrence. `Issues`, `errors.Is`, and `errors.As` expose these details. `IssuesForRule(id)` uses a compile-time index to return a defensive copy of that valid ID's issues in the same order. Unknown or invalid IDs return an empty collection; issues lacking valid IDs remain in the aggregate. Nil and zero validation errors are safely queryable.

Construction never calls callbacks, accepts no sample input, and does not inspect business fields. It copies the definitions and freezes descending priority, with ascending registration index breaking ties. Every `int32` priority is valid; the default is zero. Both constructors share this single compilation path, so the same rule sequence gives the same validation issues, order, registration indexes, and execution results under the same options.

```go
func Compile[T any](rules ...Rule[T]) (*RuleSet[T], error)
func NewEngine[T any](rules ...Rule[T]) (*Engine[T], error)
func NewEngineFromRuleSet[T any](set *RuleSet[T], options ...FireOption) (*Engine[T], error)
```

`NewEngineFromRuleSet` shares the executable snapshot without revalidating, renormalizing, sorting, or copying executable nodes. It rejects nil and zero sets with `ErrInvalidRuleSet` before inspecting options. Invalid options return `ErrInvalidPolicy` or `ErrInvalidPanicMode`. Every construction failure returns a nil pointer; compilation never returns a partial set.

`Compile[T]()` and `NewEngine[T]()` create valid empty snapshots. No-argument calls need explicit type arguments; nonempty calls and construction from a typed set infer `T`. `RuleSet.Valid()` distinguishes a compiled empty set from nil and zero sets. `Len`, `Rules`, and `Rule` are safe on nil and zero sets, returning zero, an empty collection, and a zero view with false respectively. A zero `Rule`, zero `Engine`, or nil engine is invalid.

## Metadata and identity

RuleID is the sole identity; equal display names and tags are allowed. Name and description setters trim surrounding Unicode whitespace using `strings.TrimSpace`. Tags use the same trimming, discard empty values, remove exact case-sensitive duplicates, and preserve first-occurrence order. Internal whitespace and all remaining bytes are preserved; there is no case folding or Unicode normalization. Empty metadata is valid. Repeated setters replace their own value, and `Tags()` clears the collection.

Normalization and defensive ownership happen immediately in the immutable builder, before `When` and `Then`; changing the original tag slice even before compilation cannot alter a completed rule. Reusing a builder changes neither existing builders nor rules. Compilation shares the already-frozen descriptive metadata.

`RuleSet.Rule(id)` performs exact ID lookup and returns `(RuleInfo, bool)`; unknown IDs are ordinary misses. `RuleSet.Rules()` returns a copied slice in compiled order. `RuleInfo` exposes ID, priority, order, registration index, name, description, and copied tags without executable callbacks. `Rule`, `RuleExecution`, and `RuleTrace` also expose `Name()`, `Description()`, and `Tags()`. Result and Explain obtain these values from the captured metadata block; text explanations continue to describe execution outcomes without printing descriptive metadata.

## Ordered execution

`Fire` evaluates a rule's condition, immediately attempts its action if matched, then proceeds according to policy. Every callback runs sequentially within that execution. Later conditions observe earlier action mutations, including partial changes made before failure.

Conditions must not mutate input; Go cannot enforce this read-only contract. A condition error takes precedence over its boolean return. Actions may mutate state and perform external effects. Rulite provides no transaction, rollback, retry, or compensation. Repeating `Fire` can repeat effects; idempotency belongs to the application.

`All` and `Any` copy their child slices and evaluate from left to right. `All` stops at the first false result or error; `Any` stops at the first true result or error. Empty `All[T]()` is true; empty `Any[T]()` is false. `Not` negates a successful result. Every combinator propagates errors unchanged with a false result. A nil child produces `ErrInvalidCondition` only if reached; short-circuited children are never called.

| Observed outcome | Matched | Action started | Fired | State |
| --- | --- | --- | --- | --- |
| Condition not called | false | false | false | `RuleNotEvaluated` |
| Condition returns false, nil | false | false | false | `RuleUnmatched` |
| Condition returns an error or panics | false | false | false | `RuleFailed` |
| Condition returns true, nil; context prevents action | true | false | false | `RuleSkipped` |
| Action returns nil | true | true | true | `RuleFired` |
| Action returns an error or panics | true | true | false | `RuleFailed` |

## Policies and stopping

The zero `ExecutionPolicy` equals `DefaultPolicy()`: `EvaluateAll` with `StopOnError` for both phases. Value methods select one of three stop modes and independent condition/action error modes:

```go
policy := rulite.DefaultPolicy().
    WithStop(rulite.StopOnFirstFire).
    WithActionErrors(rulite.ContinueOnError)
result, err := engine.Fire(ctx, &input, rulite.WithPolicy(policy))
```

`StopOnFirstMatch` ends after the first matched rule's action attempt, including a continued action error. `StopOnFirstFire` ends only after a successful action; `ContinueOnError` on actions enables fallback. Both policies stop the entire execution. Local selection groups are not implemented.

Options apply left to right. `WithPolicy` replaces the complete policy, `WithPanicMode` replaces the panic mode, `WithTrace` is idempotent, and a zero `FireOption` does nothing. Each option is validated when reached; a later valid option cannot repair an earlier invalid one.

`NewEngineFromRuleSet` accepts these same options as immutable engine defaults. With no options, it uses the default policy, `RecoverPanics`, and tracing off. Each Fire applies its options to a value copy of the defaults; overrides never change another call or engine. Enabling tracing as an engine default applies to every call; there is no per-call option to turn an enabled trace off. Use separate engines sharing the set when different trace defaults are needed.

Stop precedence is **panic, observed context cancellation, phase error stop, first-match/first-fire, completion**.

| StopReason | Meaning |
| --- | --- |
| `StopNone` | Execution never started; zero Result |
| `StopCompleted` | Reached the end under the selected policy, possibly with continued errors |
| `StopFirstMatch` | First matched action attempt ended execution |
| `StopFirstFire` | First successful action ended execution |
| `StopConditionError` | Condition error with StopOnError |
| `StopActionError` | Action error with StopOnError |
| `StopContextCanceled` | A callback boundary observed cancellation |
| `StopContextDeadlineExceeded` | A callback boundary observed an expired deadline |
| `StopPanic` | A callback panic was recovered |

`Result.Stopped()` is false for `StopNone` and `StopCompleted`, true for other reasons. Stopping leaves the remaining rules not evaluated, with `NotEvaluatedExecutionStopped`. A matched action prevented by context has `SkipContextDone`.

## Errors, context, and panics

Preflight checks nil context, nil input, invalid engine, then options. The first failure returns its direct sentinel (`ErrNilContext`, `ErrNilInput`, `ErrInvalidEngine`, `ErrInvalidPolicy`, or `ErrInvalidPanicMode`) and a zero Result. No callbacks run.

Once execution starts, every observed error returns `*ExecutionError` with the partial Result, even for one failure or a completed fallback. `Failure` is the sole rule failure model, shared by `Result.Failures()`, `RuleExecution.Error()`, `RuleTrace.Error()`, and the aggregate error tree. It records RuleID, phase, original cause, and continued disposition. `Continued()` means the error policy permitted continuing; context, first-match, or the end may still prevent another call. Recovered panics always have `Continued() == false`.

Use `errors.Is` for causes and `errors.As` for typed errors. Failures retain observation order. A context stop additionally preserves `ctx.Err()` and then a distinct `context.Cause(ctx)`. A callback returning `context.Canceled` while the supplied context is active is an ordinary callback error governed by its phase policy.

Context is checked before execution, before each condition, after each condition or before its action, and after each action. Even an empty engine observes an already-canceled context. Cancellation after a successful action preserves Fired. Rulite waits for callbacks to return; it does not run them in background goroutines to simulate timeouts. Callbacks must honor context to bound latency.

Trace may derive the context supplied to conditions while preserving values, deadline, Done, Err, and Cause. Context object identity is not guaranteed. Actions receive the original context.

The default `RecoverPanics` converts a callback panic to `*PanicError`, captures the stack immediately, preserves partial effects, and stops. If cancellation is also observed, both errors survive and `StopPanic` takes precedence. `PanicError.Error()` includes the payload's Go type without formatting its contents; `Value()` returns the original payload and `Stack()` returns copied bytes. `WithPanicMode(PropagatePanics)` propagates the original panic without promising a Result. Runtime fatal errors, other goroutines' panics, process exit, and `runtime.Goexit` are outside callback recovery.

## Results and explainability

Result, Explanation, and Trace derive rule outcomes from the same immutable execution ledger. Queries never rerun callbacks. `Order()` is the zero-based execution position; `RegistrationIndex()` is the original zero-based `Compile` or `NewEngine` argument position. `Rule(id)` handles unknown IDs with `false`. `Matched()`, `Fired()`, and `Failures()` preserve observation order.

Counts obey these partitions; phase failures include recovered panics:

```text
Total = Evaluated + NotEvaluated
Evaluated = Unmatched + Fired + Skipped + ConditionFailed + ActionFailed
Matched = Fired + Skipped + ActionFailed
Failed = ConditionFailed + ActionFailed
PanicRecovered <= Failed
```

A zero Result is safely queryable, with `Executed() == false`, zero counts, `StopNone`, empty collections, and an explanation of not-started execution. A valid empty execution has `Executed() == true` and, with an active context, `StopCompleted`.

`Explain()` is always available. Structured rule views include all rules, states, phase failures, and reasons for skipped or untouched rules. `Explanation.String()` is English text for people; its exact format is not a v0.x compatibility promise. It omits input, action parameters, panic contents, and stacks. Business error messages appear as supplied, so applications control their sensitivity.

`WithTrace()` adds monotonic durations and observed `All`/`Any`/`Not` child outcomes. Trace covers every engine rule and does not truncate. A tree root has index -1; children use argument indexes. Short-circuited children explicitly report not evaluated and a short-circuit reason. Uncalled functions have opaque leaf identity; their internal operator cannot be known without calling them. Plain Go callbacks have no child tree. A callback that invokes multiple combinators or changes a combinator's outcome is opaque. Trace cannot explain arbitrary Go control flow.

For 100 pricing rules, `Explain().Rules()` gives the complete execution order; `Matched` and `ActionStarted` distinguish eligibility from action attempts; `Fired` and phase failures describe action outcomes. Untouched suffix reasons and the global StopReason explain termination. `Result.Failures()` and the error tree preserve every observed failure. This proves action execution outcomes, not which fields changed. A failed action can write a field, a successful action can write nothing, and a later action can overwrite a prior value. Record `AppliedBy RuleID` in business state when field attribution matters; the [pricing example](../examples/pricing) demonstrates it.

## Ownership and concurrency

Rules, rule sets, and engines own immutable definitions, metadata, and indexes. Builders own their tag slices; Compile owns registration and ordering storage. Engines share a RuleSet's executable snapshot while owning independent configuration values, without retaining the caller's option slice. Descriptive metadata and lookup indexes form a separate block with no path back to executable callbacks. A completed Result retains that block and outcomes without retaining executable callbacks, input, context, or options. All returned slices and stack bytes are defensive copies; empty collections may be nil. Value views remain immutable through their accessors.

A shared RuleSet supports concurrent metadata reads and engine construction. A shared Engine supports concurrent Fire calls with independently owned inputs; each execution owns its ledger and trace. Completed Result, Explanation, and Trace views support concurrent reads. Input, callback captures, user error objects, and panic payloads are application-owned. Rulite cannot deep-freeze those objects. Treat returned errors and payloads as read-only and synchronize any shared mutable captures.

Sharing one mutable input requires caller synchronization around the whole Fire call, including conditions and actions, and around other accesses to that input. Engine does not lock by input address, clone input, or serialize separate executions automatically.

## Performance and scope

Validation, ordering, normalization, and indexing belong to construction. Fire uses the frozen order. With Trace off, Fire reads no duration clock. Ordinary all-miss executions use a sparse ledger without a heap object per rule; matched and failed outcomes retain the records needed for correctness. Explain rendering is on demand. Results are not backed by buffers that will be reused by another execution. See the [measured baseline](benchmarks.md).

Observer, RuleGroup, CEL, dynamic definitions, hot reload, runtime version/revision metadata, and OpenTelemetry are not implemented. See the [roadmap](roadmap.md) for later goals.

Rulite does not provide forward chaining in Fire, incremental evaluation, Agenda, Activation, Working Memory, Dynamic Facts, Rete, Phreak, truth maintenance, a DSL or YAML rule language, workflow orchestration, BRMS, distributed execution, a global registry, reflection-based field inspection, or automatic rollback. Rules execute once per pass; external nondeterminism in I/O, time, randomness, shared state, or cancellation arrival remains the application's responsibility.
