# CEL conditions

`github.com/imbrooklyn/rulite/cel` compiles CEL expressions into ordinary `rulite.Condition[T]` values. The integration uses [cel.dev/cel-go v0.32.0](https://github.com/cel-expr/cel-go/releases/tag/v0.32.0). Go 1.27 remains the minimum. Importing only `github.com/imbrooklyn/rulite` still gives a runtime with only standard-library dependencies; the integration shares the same Go module.

## Native construction

```go
package main

import (
    "context"
    "fmt"

    "github.com/imbrooklyn/rulite"
    "github.com/imbrooklyn/rulite/cel"
)

type Price struct {
    VIP bool
    Total int64
    Discount int
}

func main() {
    compiler, err := cel.NewCompiler[Price]("input")
    if err != nil { panic(err) }
    eligible, err := compiler.Compile("input.VIP && input.Total >= 10000")
    if err != nil { panic(err) }
    rule := rulite.NewRule[Price]("pricing/vip").When(eligible).
        Then(func(_ context.Context, p *Price) error { p.Discount = 20; return nil })
    engine, err := rulite.NewEngine(rule)
    if err != nil { panic(err) }
    price := Price{VIP: true, Total: 12000}
    result, err := engine.Fire(context.Background(), &price)
    if err != nil { panic(err) }
    fmt.Println(price.Discount, result.Fired())
}
```

Specify the non-pointer business type in `NewCompiler[T]`. It binds the current `*T` directly, without converting fields to a map or cloning input. `Compile` produces a typed condition accepted by ordinary rule construction and condition combinators. Parse, type-check, exact boolean result checking, and program construction all finish before a condition exists. Non-boolean results, including a `dyn` result, fail with `ErrNonBool`.

Compilers are immutable and can compile concurrently. Options are consumed immediately without retaining the supplied slice; a zero `Option` is a no-op. A nil or zero compiler returns `ErrInvalidCompiler`. Each compile call builds a new program; there is no source cache or global registration store. Owners control compiler and condition lifetimes.

## Explicit typed bindings

Use `NewBuilder[T](options...)` when expressions need several named values, a projection, protobuf, or trusted functions. The concrete `Builder[T]` provides these methods, with type inference from callbacks:

```go
Bind[V any](name string, project func(context.Context, *T) (V, error)) error
BindProto[V proto.Message](name string, descriptor protoreflect.MessageDescriptor,
    project func(context.Context, *T) (V, error)) error
Function[A, R any](name string, callback func(A) (R, error)) error
Build() (*Compiler[T], error)
```

These are concrete generic methods supported by Go 1.27, not interface requirements. The [external-consumer examples](../cel/example_test.go) compile the inferred and explicit type-argument forms.

`Bind` exposes a supported native value or pointer. The business state `T` itself can contain unsupported types because only projected values are exposed. A native struct stays a native object; the activation stores named values, not a map of business fields. Use projections to restrict the visible business surface.

Projectors run synchronously once per condition call in registration order, including unused bindings and constant expressions. A failure stops projection and becomes a `RuntimeError` retaining the original cause. The caller's context is checked before and after each projector. A nil projected root pointer is `ErrNilBinding`; represent optional root data using an explicit presence boolean and a supported value, or a nested pointer field. Projectors must only read input and must not retain mutable per-request state in shared captures.

Registration is mutable and requires caller synchronization. `Build` freezes independent configuration into a compiler; later registrations cannot change it or previously compiled conditions. Invalid registration leaves the builder unchanged. Neither registration nor compilation invokes projectors or functions. The zero builder is invalid.

Binding and function names share one namespace. Names must be ASCII identifiers of 1-64 bytes; CEL keywords, built-in function and macro names, type names, and the `__` prefix are reserved. Duplicate names, nil callbacks, unsupported types, and mapping conflicts fail during construction with an environment `CompileError`. There are at most 64 bindings and 64 trusted functions. No mutable binding map, CEL program, activation, or CEL environment options are exported.

## Native field and value mapping

`NewCompiler` requires a named native struct. `Bind` also supports scalar and collection roots. Struct schemas contain at most 128 declared fields each and 256 named struct types. Embedded fields, anonymous struct types, multiple pointer indirections, pointers to collections, arrays, interfaces, channels, functions, complex numbers, `uintptr`, unsafe pointers, and custom CEL values require an explicit supported projection.

Only exported fields are exposed. Unsupported exposed fields reject the entire schema, even when unused by an expression. Unknown statically typed fields, variable names, and incompatible operations fail compilation; a Go field rename invalidates expressions using its former name. CEL's explicit dynamic expressions retain their normal runtime semantics.

Default field names are exact, case-sensitive Go names. A field `Total int64` tagged `json:"total_amount"` is read as `input.Total`; `input.total_amount` fails. Tags, including a dash, have no effect in the default mode.

`WithJSONFieldNames()` explicitly changes native naming to JSON tags. The same field then uses only `input.total_amount`. An absent or empty tag name, including `json:",omitempty"`, retains the Go name; `json:"-"` hides the field. Names must be ASCII identifiers of 1-64 bytes. Options such as `omitempty` and `string` do not affect values or presence. Duplicate resulting names fail construction. This is field-name mapping, not JSON serialization or embedded-field promotion. It never changes protobuf naming.

| Go value | CEL representation and contract |
| --- | --- |
| Boolean and string | `bool` and `string` |
| Signed integers, including defined scalar types | CEL `int`; no unit inference |
| Unsigned integers except `uintptr` | CEL `uint`; use unsigned literals such as `1u` |
| Floating-point values | CEL `double`; no decimal precision guarantee |
| `time.Time`, `time.Duration` | Timestamp and duration; zero time remains year 1, including native fields, pointers, and dynamic access |
| Named structs and nested struct pointers | Native typed objects; no Go method invocation |
| Slices, including named slices | Typed lists; slices with the exact element type `byte` / `uint8` are bytes |
| Maps, including named maps | Typed maps with string, signed integer, unsigned integer, or boolean keys |
| Nested supported containers | Recursive typed mapping; nil pointer elements are CEL null |
| Unsupported numeric custom representations | Explicit projection; no automatic decimal or money conversion |

Native field presence uses Go zero-value semantics. `has(input.Count)` is false for an integer zero; it does not track assignment history. A nil field pointer is absent and field access reads the pointed-to zero value. A non-nil pointer to zero is present. Nil slices/maps have size zero and are absent; non-nil empty collections also have size zero but are present. Missing map keys are runtime errors. Optional access such as `input.?Child.hasValue()` preserves these presence rules.

Native object equality compares exposed fields, including pointer and collection nil presence. Hidden fields cannot influence equality. Timestamps compare as CEL timestamps. Reading a value never proves that its field is present; use `has` when that distinction matters.

Integer money values remain exact integers with application-defined units. The [pricing example](../examples/cel_pricing) uses integer minor units. For a rational decimal, a projector can multiply by 100 and accept the result only when it is an integer fitting `int64`; fractional minor units and overflow must return an error. The [mapping tests](../cel/mapping_test.go) exercise this conversion without modifying the rational input or passing through floating point. Arbitrary decimal structs are not CEL numbers, and numeric operations on them fail checking.

## Protobuf descriptors

`BindProto` accepts generated or dynamic messages through a typed projector and an explicit `protoreflect.MessageDescriptor`. Native `Bind` does not guess protobuf types; protobuf inside a native struct requires a separate protobuf binding or native projection.

Use immutable descriptors from the protobuf library. Each projected message must use the exact registered descriptor instance; a different instance produces `ErrProtoDescriptor`, even with the same full name. Registrations for the same file path must reuse the same file descriptor instance. Conflicting native/protobuf type names or descriptors fail construction. Custom descriptor/message implementations are trusted caller code.

Names come only from descriptors. For a proto field `total_amount`, use `order.total_amount`; neither its JSON name `totalAmount` nor its Go field name `TotalAmount` is an alias. This remains true with JSON field mode enabled.

Enums retain descriptor names and numeric values, including unknown proto3 enum numbers. Oneof members support individual presence; the oneof container is not a field. An optional string set to empty remains present. Proto3 ordinary scalars use protobuf's implicit presence, while proto2 and proto3 optional fields retain explicit presence. Missing repeated/map fields have size zero. `has(order.coupon)` and `order.?coupon.hasValue()` distinguish a missing optional coupon from a present empty string. The [production protobuf tests](../cel/protobuf_test.go) cover exact names, generated proto2 fields, proto3 enums, oneofs, optionals, nested messages, lists, maps, and descriptor mismatches.

Dynamic protobuf payload types `google.protobuf.Any`, `Struct`, `Value`, and `ListValue` are rejected when present in the registered descriptor graph. Convert them explicitly into a supported typed view. There is no automatic payload unpacking or global type discovery.

## Trusted functions

`Function` registers one explicit unary scalar capability. Arguments and results support native scalar types, including named primitives, timestamps, and durations. Registration rejects nil functions, reserved/duplicate names, and non-scalar signatures. There is no overload registry, arbitrary symbol lookup, or Go method invocation.

Functions must be pure, read-only, and bounded; their providers synchronize captured state. CEL charges its default unit call cost after the function returns. That charge does not measure the callback's actual work. Strings passed to or returned from a function are limited to 65,536 bytes per value. The function body cannot be preempted by CEL cost or cancellation. For context-aware preprocessing, use a projector receiving the caller's context.

Returned errors preserve their original cause. A function panic becomes `ErrFunctionPanic` without retaining or formatting its value. This CEL boundary behaves the same under either root panic mode. Projector panics occur outside the CEL interpreter and follow the ordinary root panic policy. CEL's error propagation and short-circuit rules apply inside expressions; only the final error/unknown outcome becomes a condition failure.

## Execution and errors

Programs are reused concurrently with fresh activation, projected values, and cost state for each evaluation. No mutable input cache or input clone is used. The [runnable pricing example](../examples/cel_pricing) combines two typed bindings, optional coupon presence, a trusted amount check, CEL eligibility/cap conditions, Go actions, and a Go audit condition. Later conditions see earlier action mutations. Actions and groups retain the [root contracts](architecture.md).

True and false return `bool, nil`. A final runtime error or unknown returns `false, *RuntimeError`, never a silent miss. There is no public partial-activation API. Within Fire, the error becomes the cause of the canonical `Failure` with the rule's RuleID and `ConditionPhase`, inside `ExecutionError`. Stop/continue policy and failure order are unchanged. Trace uses ordinary condition outcomes without representing CEL AST nodes as Go condition children.

`CompileError.Stage()` is `environment`, `parse`, `check`, `output`, or `program`. Both error wrappers expose `ExpressionID()`, a SHA-256 source digest, and `Unwrap` for `errors.Is` / `errors.As`. Identity is empty for environment failures and sources rejected by the byte limit. Default text omits source, input, activation, underlying error text, and panic contents. Explicitly unwrapped causes can contain source positions, excerpts, or business values; callers decide whether to disclose them and treat them as read-only.

Runtime categories include `ErrUnknown`, `ErrCostLimit`, `ErrInputLimit`, `ErrNilBinding`, `ErrProtoDescriptor`, and `ErrFunctionPanic`. Cost failures also retain their original CEL error type. Direct condition calls reject nil context/input with wrapped root `ErrNilContext` / `ErrNilInput`.

## Resource and concurrency boundaries

| Boundary | Mechanism |
| --- | --- |
| Source | At most 4,096 bytes |
| Parser recursion / expression nesting | 64 / 64 |
| Parser error recovery | 16 |
| Comprehension nesting | At most 2 |
| Regex | Literal patterns only; 128 bytes, conservative expansion and compiled program size at most 512 |
| Projected strings and bytes | 65,536 aggregate bytes, including map keys, nested fields, and protobuf unknown wire bytes |
| Projected collections | 4,096 aggregate list elements and map entries; byte slices count as bytes |
| Input traversal | At most 65,536 visited values and depth 32, including pointer/container traversal; cycles exceeding these bounds fail |
| Native schema traversal | Depth 32, 256 named struct types, 128 fields per struct |
| Protobuf registration | At most 256 files; immutable descriptors are trusted configuration |
| CEL evaluation cost | 10,000 by default; a positive `WithCostLimit` overrides it |
| Cancellation | Before/after projectors and evaluation, every 16 input visits, and every 16 comprehension iterations |

Input checks cover all exposed projected values, including constant expressions and unused bindings. Multiple bindings share one input budget; aliasing does not exempt repeated values. The unprojected business state and hidden native fields are not traversed. Schema information is frozen at construction; input validation reads current lengths and values without converting business objects to maps.

Invalid constant regex syntax fails program construction; oversized or dynamic patterns fail checking. Counted repeats are supported within the bounds. Repeated valid cost options use the last value; zero is rejected even when followed by a valid option.

These limits are not a wall-time deadline or a total memory guarantee. They do not bound all CEL intermediate allocations or time spent constructing projected values. A single built-in operation such as regex matching is not interrupted midway. Input traversal uses cooperative context checks; descriptor/message callbacks and trusted functions can still block.

Cancellation preserves both `ctx.Err()` and `context.Cause`, including constant expressions and projector failures. Evaluation stays synchronous; no background evaluation continues after Fire returns. Internal CEL recovery may convert internal panics before the root panic policy sees them.

Shared compilers, conditions, and engines support independently owned inputs. Callers synchronize shared mutable input, map/slice storage, protobuf messages, projectors, and function captures around every access, including the entire Fire call. Conditions remain read-only; actions may have partial effects. There is no rollback, retry, or automatic synchronization.

## Scope

The adapter provides conditions only. It does not implement CEL actions, a CEL policy runtime, arbitrary Go symbol/script/database execution, dynamic definitions, YAML runtime rules, hot reload, telemetry, or inference. Dynamic definitions and a typed action registry remain planned. See the [roadmap](roadmap.md) and [compile, projection, and evaluation measurements](benchmarks.md#typed-cel-binding-measurements).
