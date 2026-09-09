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

`StopOnFirstMatch` ends after the first matched rule's action attempt, including a continued action error. `StopOnFirstFire` ends only after a successful action; `ContinueOnError` on actions enables fallback. Both policies stop the entire execution, including when the selected rule belongs to a group.

Options apply left to right. `WithPolicy` replaces the complete policy, `WithPanicMode` replaces the panic mode, `WithObserver` replaces the observer, `WithTrace` is idempotent, and a zero `FireOption` does nothing. Each option is validated when reached; a later valid option cannot repair an earlier invalid one.

`NewEngineFromRuleSet` accepts these same options as immutable engine defaults. With no options, it uses the default policy, `RecoverPanics`, tracing off, and no observer. Each Fire applies its options to a value copy of the defaults; overrides never change another call or engine. Enabling tracing as an engine default applies to every call; there is no per-call option to turn an enabled trace off. Use separate engines sharing the set when different trace defaults are needed.

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
| `StopPanic` | A business callback panic was recovered |

`Result.Stopped()` is false for `StopNone` and `StopCompleted`, true for other reasons. Stopping leaves the remaining rules not evaluated, with `NotEvaluatedExecutionStopped`. A matched action prevented by context has `SkipContextDone`.

## Errors, context, and panics

Preflight checks nil context, nil input, invalid engine, then options. The first failure returns its direct sentinel (`ErrNilContext`, `ErrNilInput`, `ErrInvalidEngine`, `ErrInvalidPolicy`, or `ErrInvalidPanicMode`) and a zero Result. No callbacks run.

Once execution starts, every business execution error returns `*ExecutionError` with the partial Result, even for one failure or a completed fallback. `Failure` is the sole rule failure model, shared by `Result.Failures()`, `RuleExecution.Error()`, `RuleTrace.Error()`, events, and the aggregate error tree. It records RuleID, phase, original cause, and continued disposition. `Continued()` means the error policy permitted continuing; context, first-match, or the end may still prevent another call. Recovered business panics always have `Continued() == false`.

Use `errors.Is` for causes and `errors.As` for typed errors. Failures retain observation order. A context stop additionally preserves `ctx.Err()` and then a distinct `context.Cause(ctx)`. A callback returning `context.Canceled` while the supplied context is active is an ordinary callback error governed by its phase policy.

Context is checked before execution, before each condition, after each condition or before its action, and after each action. Even an empty engine observes an already-canceled context. Cancellation after a successful action preserves Fired. Rulite waits for callbacks to return; it does not run them in background goroutines to simulate timeouts. Callbacks must honor context to bound latency.

Trace may derive the context supplied to conditions while preserving values, deadline, Done, Err, and Cause. Context object identity is not guaranteed. Actions receive the original context.

The default `RecoverPanics` converts a business callback panic to `*PanicError`, captures the stack immediately, preserves partial effects, and stops. If cancellation is also observed, both errors survive and `StopPanic` takes precedence. `PanicError.Error()` includes the payload's Go type without formatting its contents; `Value()` returns the original payload and `Stack()` returns copied bytes. `WithPanicMode(PropagatePanics)` propagates the original panic without promising a Result. Runtime fatal errors, other goroutines' panics, process exit, and `runtime.Goexit` are outside callback recovery.

## Results and explainability

Result, Explanation, and Trace derive rule outcomes from the same immutable execution ledger. Queries never rerun callbacks. `Order()` is the zero-based flattened executable position; `RegistrationIndex()` is the original top-level construction argument position. Members also expose their local registration through `MemberIndex()` and their containing group's compiled position through `TopLevelOrder()`. `Rule(id)` handles unknown IDs with `false`. `Matched()`, `Fired()`, and `Failures()` preserve observation order.

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

The [100-rule checkout tests](../grouped_business_test.go) combine top-level rules with payment and pricing groups. `Explain().Rules()` gives the complete execution order; `Matched` and `ActionStarted` distinguish eligibility from action attempts; `Fired` and phase failures describe action outcomes. Local hole and untouched suffix reasons, together with the global StopReason, explain selection and termination. `Result.Failures()` and the error tree preserve every observed failure. This proves action execution outcomes, not which fields changed. A failed action can write a field, a successful action can write nothing, and a later action can overwrite a prior value. Record `AppliedBy RuleID` in business state when field attribution matters; the [pricing example](../examples/pricing) demonstrates it.

## Local selection groups

`FirstMatchGroup(id, members...)` and `FirstFireGroup(id, members...)` construct immutable `Group[T]` definitions from typed rules. Constructors copy the member slice; `Members()` returns a copy in local registration order. A nil member slice is an empty group. `WithPriority` returns a new group and defaults to zero independently of member priorities. No nested groups or local error policies are supported.

`Rule.Entry()` and `Group.Entry()` produce concrete `Entry[T]` values for `CompileEntries(entries...)`. The existing `Compile(rules...)` and `NewEngine(rules...)` signatures remain available. Both compilation paths use the same validation and ordering algorithm. Empty entry lists are valid. A zero Entry is an invalid zero Rule; a zero Group has an invalid empty ID. Definitions cannot contain nil rule or group pointers because their construction API uses concrete values.

GroupID follows RuleID's 1-128 byte syntax and exact comparison, in an independent namespace. GroupIDs are unique across the set. RuleIDs are unique across every top-level rule and all members, including across groups. Validation scans top-level registration order: group ID syntax and duplicate checks precede its members, whose rule issues retain the ordinary order. Invalid IDs are excluded from duplicate checks. `ValidationIssue.Index()` is the top-level registration position; `MemberIndex()` returns a local registration position and presence boolean; `GroupID()` returns a valid containing group ID when available. Member issues remain indexed by `IssuesForRule`.

Top-level entries sort by priority descending and registration ascending. Each group's members sort independently by the same criteria. Member priority never moves a group or competes with another top-level entry. Rule counts and `RuleSet.Len()` include all executable rules and members, excluding containers. `RuleSet.Rules()`, `Result.Rule`, Explain, Trace, and rule events all describe the same flattened executable order.

| Coordinate | Rule views | GroupResult |
| --- | --- | --- |
| `Order()` | Flattened executable position, excluding containers | Compiled top-level entry position |
| `TopLevelOrder()` | Compiled position of the rule or its containing group | Available as Order |
| `RegistrationIndex()` | Original top-level argument position; members use their group's position | Original CompileEntries argument position |
| `MemberIndex()` | Original local member position and true; top-level rules return zero and false | Not applicable |
| `GroupID()` | Containing group identity and true; top-level rules return empty and false | Identity available as ID |

First-match selects on the first condition returning true, nil. Its action is attempted once subject to the existing context and panic boundaries; a continued action error still resolves the group. First-fire selects only when an action returns nil. Continued action errors permit later providers to observe partial effects and try again. It guarantees at most one successful action, not exactly one external effect. Conditions remain read-only; no rollback, retry, or input clone is added.

Global condition/action error modes apply unchanged to members. Panic, observed context cancellation, phase error stop, and global selection all take precedence over advancing out of a resolved group. Local advancement continues to the next top-level entry, so a following audit can run under EvaluateAll. All continued errors remain in Failure and ExecutionError, even after a successful fallback or normal completion.

`Result.Group(id)` returns an immutable GroupResult and presence boolean; unknown IDs and zero Results return a zero view and false. `Result.Groups()` and `Explanation.Groups()` return copied views in top-level order. Views expose identity, kind, priority, order, registration, state, entry, resolution, and selected rule. State is `GroupNotEntered` before the initial group context boundary passes, `GroupResolved` when its criterion is observed, `GroupExhausted` after all members finish without selection (including an empty group), or `GroupInterrupted` when global termination stops an unresolved group. Exhaustion is not a business error by itself; all-failed first-fire candidates still retain their individual failures.

Resolution is a fact independent of the global stop: first-match selection survives an action skipped by cancellation or a failed/panicking action; first-fire selection survives cancellation after a successful action. A selected rule does not prove that an action started or succeeded; inspect its rule outcome. If global termination wins at that boundary, untouched members have `NotEvaluatedExecutionStopped`. Only members bypassed by actual local advancement use `NotEvaluatedGroupResolved`. Later global stops do not erase earlier local hole reasons. These members are not evaluated, never unmatched or matched-and-skipped.

The ledger records an evaluated frontier and one record per group, including any locally bypassed suffix and terminal decision. Later rules can run after a hole without being mistaken for untouched rules. Result, Explain, Trace, and events share these facts; queries never replay selection. Uncalled members emit no rule events and have zero callback durations. Rule events expose group coordinates through RuleInfo; see the [working consumer example](../group_example_test.go).

`GroupResult.EndReason()` separates the end decision from selection: `GroupEndResolved` means local advancement, `GroupEndExhausted` means every member finished without selection, and `GroupEndExecutionStopped` means global termination prevented entry or local completion. `GroupEndNone` is reserved for zero views and resolution events preceding the end decision. A resolved group may still end with execution-stopped. `GroupResult.StopReason()` supplies the global reason only for execution-stopped; it is StopNone otherwise. A later stop never rewrites an earlier group's local completion. Unentered groups retain their own metadata and the global stop, including empty groups.

`Explanation.Entries()` returns copied `EntryExplanation` views in compiled top-level order, including empty and unentered groups. `Order()` is the top-level position; `Rule()` and `Group()` return their respective outcome and a presence boolean, with exactly one present on a nonzero entry. `Members()` returns copied member outcomes in compiled local order; it is empty for top-level rules and zero entries. Member views keep flattened Order and original registration coordinates. The zero entry has neither definition. The existing flat `Rules()` and `Groups()` accessors remain available. Text rendering follows the same top-level order and indents members below their group. Entry spans are shared immutable metadata already used by execution, with no callbacks retained.

`Trace.Group(id)` and `Trace.Groups()` expose the same group outcomes as Result, with zero/unknown queries returning empty views and false where applicable. Trace still includes every rule, with independent reasons for a locally uncalled member and a short-circuited combinator child. No group timing or duplicate condition evaluation is introduced. The [diagnostics example](../group_diagnostics_example_test.go) demonstrates the real hierarchy, group event snapshots, and Trace queries using only the root package.

## Payment and pricing decisions

The [payment routing example](../examples/payment_routing) puts Stripe, Adyen, PayPal, and bank transfer in a `FirstFireGroup`. Its deterministic local stubs record each attempt before reporting success or failure; they make no network calls. `DefaultPolicy().WithActionErrors(ContinueOnError)` enables provider fallback while keeping condition errors fail-safe. Under `EvaluateAll`, the selected provider's mutation is visible to the following audit rule. The example reports audit execution from its rule outcome and keeps earlier provider failures in the returned error. Global `StopOnFirstFire` still stops before audit. Applications must account for partial external effects when a real provider returns an error.

The [pricing example](../examples/pricing) uses a `FirstMatchGroup` for VIP, new-customer, and high-value offers. Priority chooses the first eligible offer, which need not have the largest numerical discount. A later cap rule can reduce that discount, and the audit rule reads the final business state. The selected offer remains the group's selection fact while `AppliedBy` names the rule that last wrote the discount. An eligible offer can also fire without replacing a stronger existing discount, preserving its existing attribution.

## Observation and diagnostics

`Observer` implements `Observe(context.Context, Event) error`; `ObserverFunc` adapts a function. `WithObserver` configures the same sink for an engine default or one Fire. `WithObserver(nil)` disables it. A nil ObserverFunc is a no-op; other typed-nil implementations are invoked normally and follow the panic policy. No logging dependency, middleware, retry, or continuation interface is involved.

Events are immutable by-value tagged facts emitted from the same transitions as Result, Explain, and Trace. `Kind()` identifies the event. `Rule()` returns a callback-free `RuleInfo` and presence boolean for rule events only. RuleID retains exact identity; order is the zero-based compiled position, and registration index is the original construction position. Rule metadata accessors preserve defensive copies.

| Event kind | Delivery point | Phase, condition outcome, and failure |
| --- | --- | --- |
| `EventExecutionStarted` | Successful preflight, before the initial context check | No rule, phase, outcome, or failure |
| `EventRuleEvaluated` | Condition returned or its panic was recovered | Condition phase; false, true, or error outcome; canonical Failure for error/recovery |
| `EventRuleMatched` | Immediately after evaluated(true), before the action context boundary | Condition phase, true outcome, no failure |
| `EventRuleFired` | Successful action return and ledger update | Action phase, no condition outcome or failure |
| `EventRuleFailed` | Recorded condition or action error/recovered panic | Canonical Failure and its phase; no condition outcome |
| `EventGroupResolved` | After the selecting matched event for first-match, or fired event for first-fire | Group selection snapshot; no rule, phase, condition outcome, or failure |
| `EventGroupFinished` | An entered group's local or global end decision, before the next entry or execution finish | Final group snapshot; no rule, phase, condition outcome, or failure |
| `EventExecutionFinished` | Final business counts and stop reason, before Fire returns | No rule, phase, outcome, or failure |

`Phase()`, `ConditionOutcome()`, `Failure()`, and `Counts()` return presence booleans. Absent fields return zero values. Counts exist only on execution start and finish: start has Total equal to NotEvaluated and all other counts zero; finish has final Result counts. `Event.StopReason()` is the final business reason only on execution finish, otherwise StopNone. A zero Event has `EventNone` and no facts. Events contain no input, context, arbitrary map, action parameters, duration fields, or direct panic payload. Explicit Failure inspection can expose the existing business error chain, including PanicError and its caller-owned payload.

`Event.Group()` returns a copied GroupResult and true only for group events; other events return a zero view and false. Resolution snapshots have resolved state, selected rule, GroupEndNone and StopNone. They remain unchanged after the group finishes. Finished snapshots equal the final Result group, including EndReason and its associated StopReason. Group events carry neither summary counts nor Rule/Phase/ConditionOutcome/Failure fields. Existing event kind values and pure-rule streams are unchanged; the two group kinds are appended. Consumers handling grouped definitions must handle or explicitly ignore them. Event and diagnostic accessors remain provisional during v0.x.

A first-match group emits resolved after matched, before the action context boundary; a first-fire group emits resolved after fired, before the post-action context check. There is no additional context check between these events. Global stop precedence then determines whether the selected group completes locally or ends with execution-stopped. An entered group emits finished after that decision, also on exhaustion or interruption; an empty entered group emits only finished. Unentered groups emit neither event. Group finished precedes any later entry's callbacks and execution-finished. Delivery may stop earlier on an observer diagnostic or propagated panic.

Cancellation during group-resolved delivery is observed at the next existing action or post-action boundary, retaining selection and the actual rule outcome. Cancellation during group-finished delivery cannot revise the completed group's facts: it is checked at the next existing boundary, if any. If that group was the final entry, or execution already had a terminal stop, there is no later boundary. Observer errors and recovered panics at either group event remain diagnostics and never select another member or change a group's end reason.

A miss emits evaluated(false). A condition error or recovered panic emits evaluated(error), then failed, sharing one canonical Failure; the boolean returned with an error is ignored. A match emits evaluated(true), then matched. Its action emits fired on success or failed on error/recovery. A context-skipped action emits neither; an uncalled rule emits no events. Matched proves eligibility, not action start. Global selection resolves after the action attempt and ordinary context/error checks, followed by finish. Preflight failures emit nothing. Empty and already-canceled executions still emit start and finish if observation remains enabled.

The first observer error appends a `Diagnostic` to `Result.Diagnostics()` and disables observation for the rest of that execution. The next execution starts enabled. A recovered observer panic does the same with an `ObserverPanicError` cause; it does not create a business Failure or StopPanic. At most one diagnostic exists for the single observer, including a fault on finish. Diagnostics never join ExecutionError, change ErrorMode or selection, retry business callbacks, or change matched/fired counts. `Cause()` and `Unwrap()` preserve error identity for `errors.Is` and `errors.As` on diagnostics. ObserverPanicError retains the original read-only value and an immediate stack; its Error formats only the payload type, and Stack returns copied bytes. `PropagatePanics` propagates an observer panic unchanged, with no promise of a Result or finish event.

Delivery is synchronous on the Fire call and receives the caller's context. Implementations manage latency, backpressure, and synchronization across concurrent executions. Observer latency is part of Fire wall time and overall Trace duration, including finish delivery, but never Condition/Action durations. Events do not consume timing; with Trace off, observation reads no duration clock. Observation failure isolation assumes the observer does not change context or external business state and excludes timing-sensitive external behavior. Real cancellation is checked at the existing next context boundary; no extra check separates evaluated(true) and matched. Cancellation during finish occurs after terminal business facts and cannot change them retroactively. Fire waits for a blocked observer to return even after cancellation. See the [read-only collector example](../observer_example_test.go).

## Ownership and concurrency

Rules, rule sets, and engines own immutable definitions, metadata, and indexes. Builders own their tag slices; Compile owns registration and ordering storage. Engines share a RuleSet's executable snapshot while owning independent configuration values, without retaining the caller's option slice. Descriptive metadata and lookup indexes form a separate block with no path back to executable callbacks. A completed Result retains that block, outcomes, and diagnostics without retaining executable callbacks, observers, input, context, or options. Events can outlive delivery with the same ownership boundary. All returned slices and stack bytes are defensive copies; empty collections may be nil. Value views remain immutable through their accessors.

A shared RuleSet supports concurrent metadata reads and engine construction. A shared Engine supports concurrent Fire calls with independently owned inputs; each execution owns its ledger, trace, and observation state. Completed Result, Explanation, Trace, Event, and Diagnostic views support concurrent reads. Input, callback and observer captures, user error objects, and panic payloads are application-owned. Rulite cannot deep-freeze those objects. Treat returned errors and payloads as read-only and synchronize any shared mutable captures.

Sharing one mutable input requires caller synchronization around the whole Fire call, including conditions and actions, and around other accesses to that input. Engine does not lock by input address, clone input, or serialize separate executions automatically.

## Performance and scope

Validation, ordering, normalization, and indexing belong to construction. Fire uses the frozen order. With Trace off, Fire reads no duration clock. Ordinary all-miss executions use a sparse ledger without a heap object per rule; matched and failed outcomes retain the records needed for correctness. Explain rendering is on demand. Results are not backed by buffers that will be reused by another execution. See the [measured baseline](benchmarks.md).

CI retains Go 1.27 and checks allocation and immutable ownership guarantees independently of timing. Benchmark sampling builds each compared revision outside measurement, runs repeated samples on the same host, and preserves raw output and environment metadata as artifacts. Timing comparisons use an explicit tolerance and paired statistical interval; they are advisory. The [benchmark method](benchmarks.md#repeatable-regression-sampling) describes the limits. Performance tooling uses the Python standard library and adds no Go runtime dependency.

Dynamic definitions, hot reload, runtime version/revision metadata, and OpenTelemetry are not implemented. See the [roadmap](roadmap.md) for later goals.

The [CEL Condition adapter](cel.md) compiles expressions before execution, requires an exact boolean result type, and maps evaluation errors and unknown outcomes to ordinary Condition errors. It binds an explicit native struct variable using exported Go field names and reuses the compiled program with independent evaluation state. Its dependency, reflection, and resource limits stay in the integration package; root callback signatures and the standard-library runtime boundary remain unchanged. Complex mapping and projectors remain planned. CEL actions are outside the scope.

Rulite does not provide forward chaining in Fire, incremental evaluation, Agenda, Activation, Working Memory, Dynamic Facts, Rete, Phreak, truth maintenance, a DSL or YAML rule language, workflow orchestration, BRMS, distributed execution, a global registry, reflection-based mutation attribution, or automatic rollback. Rules execute once per pass; external nondeterminism in I/O, time, randomness, shared state, or cancellation arrival remains the application's responsibility.
