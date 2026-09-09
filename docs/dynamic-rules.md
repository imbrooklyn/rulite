# Dynamic rule definitions

`github.com/imbrooklyn/rulite/dynamic` compiles JSON configuration into ordinary immutable `rulite.RuleSet[T]` values. It uses the [CEL Condition adapter](cel.md) for eligibility and an explicit registry of typed Go actions for execution. Go 1.27 remains the minimum. Root-only consumers need neither this package nor a registry, and the root runtime still depends only on the standard library.

## Construction

```go
package main

import (
    "context"
    "fmt"

    "github.com/imbrooklyn/rulite"
    "github.com/imbrooklyn/rulite/cel"
    "github.com/imbrooklyn/rulite/dynamic"
)

type Price struct { VIP bool; Discount int }
type Params struct { Percent int `json:"percent"` }

func main() {
    conditions, err := cel.NewCompiler[Price]("input")
    if err != nil { panic(err) }
    actions := dynamic.NewRegistry[Price]()
    err = actions.Register("pricing.apply_discount/v1",
        func(_ context.Context, p *Price, v Params) error {
            p.Discount = v.Percent
            return nil
        },
        func(v Params) error {
            if v.Percent < 1 || v.Percent > 100 {
                return fmt.Errorf("percent must be between 1 and 100")
            }
            return nil
        })
    if err != nil { panic(err) }
    if err := actions.Freeze(); err != nil { panic(err) }
    source := []byte(`[
        {"id":"pricing/vip","priority":100,"when":"input.VIP",
         "action":"pricing.apply_discount/v1","params":{"percent":20}}
    ]`)
    set, err := dynamic.CompileJSON(source, conditions, actions)
    if err != nil { panic(err) }
    engine, err := rulite.NewEngineFromRuleSet(set)
    if err != nil { panic(err) }
    price := Price{VIP:true}
    result, err := engine.Fire(context.Background(), &price)
    if err != nil { panic(err) }
    fmt.Println(price.Discount, result.Fired())
}
```

`Register[P]` is a concrete generic method, with `P` inferred from the callback. The [external-consumer tests](../dynamic/example_test.go) and [pricing example](../examples/dynamic_pricing) compile inferred and explicit type arguments. There are no generic interface methods or exposed CEL programs.

The construction path is:

```text
JSON bytes -> strict Decode -> schema and root ID validation
           -> compile all CEL conditions -> resolve typed actions and parameters
           -> ordinary rules -> root Compile -> RuleSet -> Engine
```

`CompileJSON(source, conditions, actions)` returns a complete set or nil with an error. `Decode(source)` returns independent mutable `[]Definition` configuration without compiling CEL or invoking callbacks. `Compile(definitions, conditions, actions)` accepts DTOs directly, validates them, and snapshots their slices and raw parameters before invoking validators. Both compilation paths require a frozen registry and a valid CEL compiler, including for an empty set. Registry preflight precedes definition decoding or validation.

After field and limit checks, ID syntax and duplicates use the root's canonical validation. All conditions compile in original registration order before any action parameter validator runs. Actions then resolve and validate in that order; the first condition or action error stops construction. No partial set or action execution is published. Earlier validators may already have run when a later action fails; validators must be read-only.

## Canonical JSON schema

The transport is one JSON array. The empty array is valid. Each object has only these exact, case-sensitive names:

| JSON field | Go DTO field | Contract |
| --- | --- | --- |
| `id` | `ID rulite.RuleID` | Required; root ID syntax and uniqueness |
| `priority` | `Priority rulite.Priority` | Optional; zero default, full signed int32 range |
| `description` | `Description string` | Optional; root whitespace normalization |
| `tags` | `Tags []string` | Optional; root trimming, deduplication and defensive ownership |
| `when` | `When string` | Required nonblank CEL source with an exact bool result |
| `action` | `Action string` | Required explicit versioned registry name |
| `params` | `Params json.RawMessage` | Required JSON object, including `{}` for an empty parameter struct |

Unknown fields, duplicate object keys at any depth, trailing values or text, malformed numbers, incompatible types, integer overflow, invalid UTF-8 and unpaired Unicode surrogates are rejected. Duplicate keys are compared after JSON string escape decoding: `"rate"` and `"\u0072ate"` are duplicates. There is no last-wins behavior, case alias, or name guessing. These syntax checks use Go's [JSON v2 decoder](https://pkg.go.dev/encoding/json/v2@go1.27.0), with explicit bounded token traversal before semantic decoding.

JSON null is rejected everywhere, including nested parameters. Omit optional fields to retain their zero values; use `[]` or `{}` for explicitly empty collections. Missing parameter fields keep their typed zero values, so a validator must enforce required business values. A missing `params` object is an error. `Definition` is a DTO; using another decoder does not replace `Compile` validation, and only `Decode` or `CompileJSON` establishes strict transport semantics.

CEL native names remain exact exported Go field names by default: the example uses `input.VIP`. JSON field mode for business input requires the CEL compiler's explicit option and is independent of configuration field naming. Protobuf uses descriptor names. Native pointer/presence behavior, runtime unknowns, and mapping limitations follow the [CEL guide](cel.md).

## Typed action capabilities

`NewRegistry[T]()` creates an explicit mutable instance. `Register[P](name, action, validators...)` accepts `func(context.Context, *T, P) error` and zero or one `func(P) error` validator. A supplied nil validator is invalid; omit the argument to omit validation. Registration rejects nil actions, duplicate names, unsupported schemas and multiple validators without changing the registry.

Names contain at most 128 bytes and match `^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*/v[1-9][0-9]*$`. For example, `pricing.apply_discount/v1` is a capability identity, not a Go symbol or package path. No function is discovered or invoked from its name. A name must already be registered by trusted application code.

`Freeze()` permanently closes registration and is idempotent, including for an empty registry. All registry methods are synchronized; value copies share the same registration/freeze state. A nil or zero registry is invalid. Frozen registries support concurrent compilation, with synchronization of validator captures still owned by the provider. There is no global registry, source cache, or program cache. Fire captures only the compiled condition and ordinary typed action closure; it does not retain or consult the registry.

`P` must be a non-pointer struct. Its exposed fields support booleans, strings, signed integers, unsigned integers except `uintptr`, floating-point values, named primitive types, nested structs, pointers, slices, and maps with string keys. Byte slices use base64 strings. JSON numbers decode directly into the declared integer type, preserving values beyond the exact float64 integer range. Money units belong to the application; use integer units or validated decimal strings when precision matters.

Fields use their exact Go name or explicit `json` tag name. Names are ASCII identifiers of 1-64 bytes. Empty tag names retain the Go name; `json:"-"` hides a field. Only `omitempty` and `omitzero` tag options are accepted. Hidden and unexported fields stay zero and are not JSON destinations. Nested unknown fields and naming conflicts are rejected. Parameter schemas reject embedding, recursive types, arrays, interfaces, channels, functions, complex numbers, unsafe pointers, custom JSON/text decoding methods, and options that could change strict naming or representation. `time.Time` and `time.Duration` require an explicit string or integer-unit DTO. There is no arbitrary custom object conversion or reflection invocation of business methods.

Compilation decodes independently allocated typed data, applies the validator, then decodes a separate execution value when a validator is present. Retaining or later editing the validator's private data cannot alter execution parameters. A validator must not normalize parameters: its edits do not become execution values. Changes to the supplied validator slice, definition slice, tags, or raw bytes after compilation cannot affect the set.

Each action receives `P` by value. Nested maps, slices, and pointers in execution parameters are shared **read-only** across calls; this is a callback contract, not enforced deep immutability. Actions must copy nested data before placing it in mutable input or retaining it for modification. The pricing audit action demonstrates copying labels. Providers synchronize shared callback captures. No parameter decoding, cloning, validation or reflection is added to Fire.

## Errors and execution

Registration and registry preflight expose `ErrInvalidRegistry`, `ErrRegistryFrozen`, `ErrRegistryNotFrozen`, `ErrInvalidAction`, `ErrDuplicateAction`, and `ErrLimit`. Definition construction errors use `*dynamic.CompileError`, whose `Stage()` is `decode`, `validate`, `condition`, or `action`. `Index()` is the original zero-based definition index, or -1 for a document-wide failure. Root `ValidationError` retains all ID issues and their indexes. Default error text includes only stage and index.

`Unwrap` preserves `errors.Is` and `errors.As`, including root validation issues, CEL compilation diagnostics, JSON syntax/type errors, validator errors, `ErrInvalidDefinition`, `ErrInvalidParams`, `ErrUnknownAction`, and `ErrLimit`. Explicit causes may contain configuration values or source positions; callers decide whether to disclose them. Source, parameters and underlying diagnostic text are not automatically formatted by the outer error. Trusted validator panics propagate from construction; they do not publish a set or create a business execution failure.

CEL runtime error, unknown, cancellation and exceeded cost remain ordinary Condition errors with canonical root `Failure` and `ExecutionError`. RuleID and ConditionPhase are retained. A missed rule never invokes an action. Action success, error, panic, partial effects and context boundaries use the [root semantics](architecture.md) without extra wrappers or policies. Later conditions observe earlier changes. Matched proves eligibility; Fired proves a nil action error, not a field write. Business actions record field provenance explicitly, as the pricing example does with `AppliedBy`.

Trace observes the same ordinary condition outcome. It does not invent CEL AST child nodes or expose action parameters. Changing configuration does not change the sequential execution model, default error policy, priority order, or registration tie-break.

## Limits and concurrency

| Boundary | Enforced limit |
| --- | --- |
| JSON document | 1,048,576 bytes including whitespace; depth 18; 131,072 tokens |
| Direct DTO list | 256 definitions; 1,048,576 aggregate bytes across ID, description, tags, source, action name and raw params |
| Decoded document | The same definition and aggregate DTO limits |
| Description / tags | 4,096 bytes per description; 32 tags per rule, 128 bytes each, before normalization |
| Parameters | 16,384 raw bytes, depth 16 and 4,096 JSON tokens per object |
| Parameter schema | Depth 16, 128 distinct types, 64 declared fields per struct, at most 16,384 bytes of inline storage per type |
| Registry | 256 registered actions |
| CEL source / evaluation | The CEL compiler's source, parser, regex, input and cost limits; default cost 10,000, overridable with `WithCostLimit` |

JSON depth counts open arrays and objects, with the root container at depth one; tokens include keys and opening/closing delimiters. Checks happen before typed decoding. These limits bound accepted configuration, not all compiler memory or wall time. The caller controls how bytes are acquired before compilation. CEL budgets do not preempt a blocking projector, custom function, validator, or Go action. Validators have no context parameter and must finish their bounded validation synchronously. No goroutine implements a fake timeout.

Frozen registries, compiled CEL programs and sets can serve several engines concurrently. Use independent inputs and synchronize shared mutable input, nested storage, protobuf messages and callback captures. Shared input requires synchronization around the entire Fire call. Single executions remain sequential. Compiled program and set lifetimes belong to their owners.

## Example and scope

```sh
go run ./examples/dynamic_pricing
```

The [pricing configuration](../examples/dynamic_pricing/rules.json) uses only three explicit capabilities: premium discount, cap, and audit. Its [Go implementation](../examples/dynamic_pricing/main.go) validates integer percentages, preserves a stronger existing offer, records the final writer, and copies audit labels. Compilation failures prevent engine construction.

Applications can load different configuration, compile another immutable set, and publish its engine through [Runtime](runtime.md). All decoding, validation, and compilation must succeed before publication; failure leaves the current snapshot available. The dynamic package itself owns no source acquisition or reload loop. This capability does not execute arbitrary Go symbols, methods, scripts, or database queries. It adds no dynamic groups, workflow schema, CEL actions, CEL policy runtime, YAML runtime, telemetry, or inference. See the [roadmap](roadmap.md) and [dynamic construction and runtime measurements](benchmarks.md#dynamic-rule-measurements).
