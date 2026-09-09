# OpenTelemetry observation

`github.com/imbrooklyn/rulite/otel` records execution metrics and bounded traces through the existing synchronous Observer contract. The root package still imports only the standard library. Providers are explicit: the adapter creates no global provider, logger, exporter, background worker, network connection, or secret lookup.

The integration uses [OpenTelemetry Go v1.46.0](https://github.com/open-telemetry/opentelemetry-go/releases/tag/v1.46.0), with Go 1.27 retained as Rulite's minimum. Instrument behavior follows the [official metric API](https://pkg.go.dev/go.opentelemetry.io/otel/metric@v1.46.0). The adapter API remains provisional during v0.x.

## Record in memory

```go
package main

import (
    "context"
    "fmt"

    "github.com/imbrooklyn/rulite"
    ruliteotel "github.com/imbrooklyn/rulite/otel"
    sdkmetric "go.opentelemetry.io/otel/sdk/metric"
    "go.opentelemetry.io/otel/sdk/metric/metricdata"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    "go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func main() {
    ctx := context.Background()
    reader := sdkmetric.NewManualReader()
    exporter := tracetest.NewInMemoryExporter()
    metrics := sdkmetric.NewMeterProvider(sdkmetric.WithResource(resource.Empty()), sdkmetric.WithReader(reader))
    traces := sdktrace.NewTracerProvider(sdktrace.WithResource(resource.Empty()), sdktrace.WithSyncer(exporter))
    defer func() {
        if err := traces.Shutdown(ctx); err != nil { panic(err) }
        if err := metrics.Shutdown(ctx); err != nil { panic(err) }
    }()
    adapter, err := ruliteotel.New(traces, metrics, ruliteotel.WithEventLimit(16))
    if err != nil { panic(err) }
    rule := rulite.NewRule[int]("example/increment").
        When(func(context.Context, *int) (bool, error) { return true, nil }).
        Then(func(_ context.Context, n *int) error { *n++; return nil })
    engine, err := rulite.NewEngine(rule)
    if err != nil { panic(err) }
    input := 0
    result, err := adapter.Fire(ctx, engine, &input)
    if err != nil { panic(err) }
    var data metricdata.ResourceMetrics
    if err := reader.Collect(ctx, &data); err != nil { panic(err) }
    fmt.Println(input, result.Counts().Fired, len(result.Diagnostics()), len(exporter.GetSpans()))
}
```

Output is `1 1 0 1`. Both signals stay in memory. The [runnable observability example](../examples/observability) additionally uses Runtime identity, reads the execution counter, and keeps field attribution in business input. Run it with `go run ./examples/observability`.

## API and execution scope

```go
func New(traces trace.TracerProvider, metrics metric.MeterProvider, options ...Option) (*Adapter, error)
func (a *Adapter) Fire[T any](ctx context.Context, executor interface {
    Fire(context.Context, *T, ...rulite.FireOption) (rulite.Result, error)
}, input *T, options ...rulite.FireOption) (rulite.Result, error)
```

Both `*rulite.Engine[T]` and `*rulite.Runtime[T]` satisfy the execution interface; `T` is inferred from the input. Each call creates a private Observer and delegates exactly one core Fire. Concurrent or nested calls may reuse the same context without sharing observation state. There is no context-identity map, retained execution registry, or second evaluation algorithm. Custom executors must delegate exactly one core Fire with the supplied options.

The adapter installs its Observer last. It replaces the captured engine default and any per-call Observer, including `WithObserver(nil)`, under the existing single-observer contract. Other Fire options retain their validation and ordering. To disable observation for a call, use Engine or Runtime Fire directly. To disable just one telemetry signal, provide the corresponding official no-op provider. Construct the adapter once and share it; it owns immutable configuration and concurrency-safe OTel instruments.

The caller's business callback context is unchanged. The execution span uses its parent span, but the adapter does not install the new span in condition or action contexts. Downstream callback spans therefore use the caller's parent unless the application arranges another parent explicitly. Metrics receive a derived context containing the execution span for SDK exemplar correlation. Context values are not turned into attributes by the adapter.

Nil providers or invalid options return `ErrInvalidConfig`. A nil or zero Adapter returns `ErrInvalidAdapter`; a nil execution interface returns `ErrInvalidExecutor`. Typed-nil Engine/Runtime values follow their normal core preflight errors. Provider construction errors preserve their causes through Unwrap and use sanitized text; provider construction panics propagate. Custom providers, including typed-nil implementations, remain responsible for honoring the OTel API.

Preflight failures emit no telemetry. Empty and already-canceled executions still deliver start/finish facts; the supplied providers decide whether to record with a canceled context. Runtime version/revision association comes from captured events, never from a later read of the current publication. Existing sequential mutation visibility, group selection, context boundaries, partial Results, and canonical Failure/ExecutionError semantics remain intact.

## Metrics and cardinality

All instruments use the scope name `github.com/imbrooklyn/rulite/otel`.

| Instrument | Kind | Unit | Measurement |
| --- | --- | --- | --- |
| `rulite.execution.count` | Int64Counter | `{execution}` | One per observed execution finish, including business errors and cancellation |
| `rulite.execution.duration` | Float64Histogram | `s` | Elapsed start-to-finish observation time, excluding finish recording/export |
| `rulite.rule.evaluated` | Int64Counter | `{rule}` | Final Counts.Evaluated |
| `rulite.rule.matched` | Int64Counter | `{rule}` | Final Counts.Matched |
| `rulite.rule.fired` | Int64Counter | `{rule}` | Final Counts.Fired |
| `rulite.rule.failed` | Int64Counter | `{rule}` | Final Counts.Failed, including recovered business panics |

Counters add nonnegative values and are monotonic sums. Zero rule counts do not create measurements. Counts overlap: a failed action can also be evaluated and matched; matched does not prove an action started, and fired proves only a nil action return. All summary values come directly from the final core ledger. A propagated panic or an observer disabled before finish produces no fabricated completion summary. A provider fault during finish can leave partially recorded telemetry; recording is not transactional.

The default summary attribute is `rulite.stop.reason`, with eight possible values: completed, first-match, first-fire, condition-error, action-error, context-canceled, context-deadline-exceeded, and panic. Default metrics have no RuleID, version, revision, input, or error attributes.

`WithMetricVersions(versions...)` adds `rulite.ruleset.version` only through an exact allowlist of at most 16 entries. Each entry is 1 to 128 UTF-8 bytes; `other` is reserved for unspecified or unlisted versions. An empty list enables only the fallback. This bounds each summary instrument to at most 136 attribute combinations per adapter configuration. `SnapshotRevision` never appears in metrics.

`WithRuleMetrics(ids...)` enables the additional Int64Counter `rulite.rule.events`, unit `{event}`, for at most 64 exact RuleIDs. Its attributes are `rulite.rule.id` and `rulite.rule.event`, whose values are evaluated, matched, fired, or failed. It records delivered rule events, independently of trace filtering or truncation. This permits at most 256 combinations; no version or revision is attached. Unknown IDs produce no series, and an empty list disables the instrument. Lists accept at most 128 UTF-8 bytes per nonempty entry, copy their arguments, and are not populated from observed traffic.

Bounds apply to one fixed configuration. Repeatedly constructing adapters with new allowlists against the same provider can accumulate their union; the provider owner must bound that union, resource attributes, and other instrumentation. Options replace earlier lists and validate in order; a later valid option cannot repair an earlier invalid one. A zero Option is a no-op.

The duration histogram measures seconds using the monotonic clock from start delivery to entry into finish delivery. It includes condition/action time and intervening synchronous telemetry work. It excludes finish metrics, span termination, and cleanup. The adapter reads no duration clock when the histogram is disabled at start. OTel controls its own span timestamps; no per-rule callback durations are inferred from event arrival times.

The histogram advises explicit boundaries at 0.0001, 0.001, 0.01, 0.1, 1, and 10 seconds. SDK Views and Readers own aggregation and temporality; the in-memory example uses cumulative counter sums and a cumulative histogram. Provider owners can change buckets or aggregation without changing business execution. See the [OTel instrumentation guide](https://opentelemetry.io/docs/languages/go/instrumentation/) and [measured costs](benchmarks.md#opentelemetry-observation-measurements).

## Spans, filtering, and limits

The default trace is one internal span named `rulite.fire`. Rule events preserve delivery order: rule-evaluated, rule-matched, rule-fired, or rule-failed. Condition failure emits evaluated(error) then failed; matched is eligibility, not action success. Uncalled rules emit nothing. Group lifecycle events are not exported; the rule stream and final counts retain the actual selected/bypassed behavior, while full group facts remain in Result and core Trace.

Rule event attributes are `rulite.rule.id`, `rulite.rule.priority`, `rulite.rule.order`, `rulite.rule.phase`, and, when present, `rulite.condition.outcome` (false, true, or error). Event names describe the observed outcome. The execution span includes `rulite.snapshot.revision` as a decimal string, avoiding unsigned integer overflow. Direct Engine execution uses zero. Revisions are local to a Runtime, not globally unique identifiers.

`WithTraceVersions()` enables `rulite.ruleset.version` on the span. Empty, invalid UTF-8, or values over 128 bytes are omitted with `rulite.ruleset.version.omitted=true`. This option does not affect metrics. No rule names, descriptions, tags, source digest, input, CEL activation, action parameters, arbitrary error text, error type, stack, or panic payload are exported. Business failures/cancellation set Error status with fixed text; successful/selected executions keep Unset status. Continued business failures still set Error status even when stop reason is completed.

`WithoutTraceRuleIDs()` removes IDs from rule events. `WithTraceRules(ids...)` filters by a fixed allowlist of at most 64 IDs using the same string bounds as rule metrics; an empty list filters every rule event. Priority, order, phase, outcome, final counts, and identity follow their independent settings. There are no user callbacks for serialization or redaction. Applications choose safe IDs and versions before opting into their export; SDK-added resources, links, attributes, and exemplars remain provider-owned.

`WithEventLimit(n)` retains the first eligible events, with default 128 and allowed range 0 through 1024. Filtering happens first. On finish, span attributes report `rulite.events.recorded`, `rulite.events.dropped`, and `rulite.events.filtered`, alongside stop reason and total/evaluated/matched/fired/failed counts. Exceeding the adapter limit returns `ErrEventLimit` from finish observation, after metrics and span completion. It appears only in Result.Diagnostics, never in the business error. Intentional filtering and non-recording spans do not produce limit diagnostics.

The SDK may independently sample out spans or drop events/attributes. Configure its limits to accommodate the adapter limit when the complete retained prefix is required. The adapter's recorded count means AddEvent calls, not confirmed SDK retention or delivery. SDK dropped-event counts and exporter diagnostics are separate. Adapter truncation never changes the complete core Result or opt-in Trace.

## Combined execution and operational diagnostics

The [payment reload example](../examples/runtime_reload) records Runtime executions compiled from strict JSON and CEL, with a code-owned first-fire provider group, typed fallback, and subsequent audit. It uses explicit in-memory providers and four bounded concurrent calls. A failed provider attempt remains a canonical business failure even when another provider and audit succeed. The deliberately limited span produces an independent `ErrEventLimit` diagnostic while the core Result and Trace remain complete.

For operational attribution, read the captured `Result.Snapshot()` or span revision, then inspect business counts, stop reason, and failures. Use `Result.Diagnostics()` for observer delivery failures. Read exporter/provider diagnostics separately: an export error handled within the SDK cannot retroactively change a Result. Raw business error and panic values are never adapter attributes; explicit application logging must decide whether those values are safe to disclose.

Concurrent executions share immutable adapter configuration and instruments, with a separate span scope per call. The [joint tests](../examples/runtime_reload/main_test.go) exercise old-version action success, error, panic and cancellation across publication, observer error/panic, event limits, and provider panic. They compare telemetry with the core ledger and successful publication identities. Provider panics can leave incomplete observation, but never add rule failures. The adapter still replaces the one Observer slot; direct Observer collection and OTel observation are separate execution choices.

Use one fixed, bounded metric configuration for a provider lifetime. Changing publication revisions does not add metric series. Version allowlists and rule allowlists remain explicit, and changing those configurations repeatedly can accumulate their union. Bound provider retention and application Result history independently. Sampling or event truncation is not a substitute for managing retained Results, whose complete metadata and Trace may outlive the executable snapshot. See [ownership and measurements](benchmarks.md#snapshot-scale-and-retention).

## Failure and resource ownership

Observer work stays synchronous. Fire waits for provider calls and inherits their latency/backpressure; no goroutine is started to simulate a timeout. A real context cancellation is still checked only at the next existing callback boundary. Cancellation during finish cannot revise already-final business facts. Complete core Trace duration includes synchronous observation, whereas its condition/action durations exclude observer work.

Under the default recovery mode, a provider panic during observation becomes an independent ObserverPanicError diagnostic and disables further observation for that call. Business policy, selection, Failure/ExecutionError, and stop reason stay unchanged. A later Fire starts with fresh observation state. The original read-only panic payload remains available through the diagnostic; the adapter does not format or export it. `PropagatePanics` preserves the original business or provider panic, with no promised Result or finish event.

When a span handle exists and finish was not reached, cleanup attempts an incomplete marker and ends that handle once. Secondary cleanup panics are suppressed so they cannot replace a business return or the original propagated panic. Provider failures can prevent the marker or export; a provider that panics before returning a span handle owns any resources it created. Completed spans are never retried.

OTel recording methods do not return export errors. Errors handled inside a provider, including asynchronous exporter failures, are reported through that provider's own diagnostics, not retroactively through Result.Diagnostics. Configure those diagnostics explicitly with the provider; the adapter does not replace the global OTel error handler. This follows the [OTel error-handling boundary](https://opentelemetry.io/docs/specs/otel/error-handling/).

Provider owners manage sampling, SDK limits, buffering, exporter memory/network use, flush, shutdown, and the lifetime of calls already in progress. The in-memory exporter in the example retains spans until reset/shutdown; production owners must choose a bounded retention policy. Engine, Runtime, and Result do not own those resources, and Adapter has no Close or shutdown method. A returned Result has no reference to the adapter, span, provider, callback context, or executable snapshot through this integration; caller-owned error/panic payloads may themselves retain application objects.

Share an Adapter and immutable Engine/Runtime across independent inputs. Synchronize a shared mutable input around the entire Fire and all other accesses, and synchronize callback captures. No per-rule spans, logging framework, automatic source acquisition, watcher, rollout/rollback service, distributed control plane, inference, or incremental evaluation is added.
