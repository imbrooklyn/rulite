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

Specify the non-pointer business type once in `NewCompiler[T]`. The explicit variable name binds the current `*T` directly, without converting its fields to a map or cloning input. Names are ASCII identifiers of 1-64 bytes, excluding CEL keywords. `Compile` returns a typed condition accepted by `When`, `All`, `Any`, and `Not`; ordinary rule and engine construction infer the remaining type parameters.

The compiler is immutable and reusable. Its options are consumed immediately without retaining the supplied slice. A zero `Option` is a no-op. A nil or zero compiler returns `ErrInvalidCompiler`. Construction takes no sample input, and compilation never evaluates input.

Each source follows parse, type-check, exact boolean result check, and program construction. All must succeed before a condition exists. Integers and `dyn` results are rejected with `ErrNonBool`; undeclared variables, unknown statically typed fields, syntax errors, and type mismatches fail compilation. Renaming a Go field requires recompiling its expressions. CEL's explicit dynamic expressions retain CEL semantics; an exact checked boolean result is still required.

## Supported native mapping

`T` must be a named struct with at most 128 declared fields and no embedded fields. Only its direct exported fields are exposed. Field names are case-sensitive Go names: `input.Total` works for a field tagged `json:"total_amount"`; `input.total_amount` fails. Tags, including `json:"-"`, do not hide or rename exported fields. Keep sensitive data out of the bound schema when expressions must not read it.

| Go field | CEL type and behavior |
| --- | --- |
| `bool`, `string` | `bool`, `string` |
| `int`, `int8`, `int16`, `int32`, `int64` | Signed CEL `int` |
| `uint`, `uint8`, `uint16`, `uint32`, `uint64` | CEL `uint`; use literals such as `1u` |
| `float32`, `float64` | CEL `double`; no decimal precision promise |
| Defined types with these scalar underlying types | Same scalar mapping, including integer minor units and `rulite.RuleID`; no unit inference |
| Unnamed slices of supported scalars | Typed CEL lists; `[]byte` / `[]uint8` are CEL `bytes` |
| Nil and empty slices | Both have size zero; `has` distinguishes nil from non-nil empty slices |
| Scalar presence | `has(input.Field)` means the field is nonzero, not that it was explicitly assigned |

Exported unsupported fields reject the entire schema at construction, even when an expression would not access them. Nested structs, pointers, maps, arrays, interfaces, named collection types, `time.Time`, `time.Duration`, custom CEL values, protobuf, and arbitrary custom conversions are unsupported. Unexported fields are unavailable. Go methods and custom function registration are not exposed.

## Execution and errors

A program is built once per `Compile` call and reused concurrently. Each evaluation creates independent activation and cost state using the current input. There is no mutable input cache. The [runnable pricing example](../examples/cel_pricing) combines CEL eligibility and cap conditions with Go actions and a Go audit condition: the cap sees the earlier discount mutation. Actions and groups retain the [ordinary root contracts](architecture.md).

True and false return `bool, nil`. Runtime errors and unknown outcomes return `false, *RuntimeError`, never a silent miss. Native complete inputs do not offer a partial-activation API; unknown mapping is defensive. Within Fire, the error is the cause of the existing `Failure` with the rule's RuleID and `ConditionPhase`, inside `ExecutionError`. Default condition errors stop execution; `ContinueOnError` continues and retains all failures in order. Trace does not change these outcomes; a CEL expression is an opaque condition without a CEL child tree.

`CompileError.Stage()` is `environment`, `parse`, `check`, `output`, or `program`. Both error types expose `ExpressionID()`, a SHA-256 source digest. It is empty for environment failures and sources rejected by the byte limit before parsing. Default error text includes stage or evaluation failure and the digest, without source, activation, input, underlying error text, or panic contents. Digests identify source, not a business rule or secret protection mechanism.

Both types support `Unwrap` for `errors.Is` / `errors.As`. Compile diagnostics can include positions and source excerpts; upstream runtime causes can contain business values. Inspect or disclose those causes explicitly. `ErrUnknown`, `ErrCostLimit`, and `ErrInputLimit` identify runtime categories. Cost failures also preserve their original CEL error type. Direct condition calls reject nil context/input with wrapped root `ErrNilContext` / `ErrNilInput`.

## Resource and concurrency boundaries

| Boundary | Limit |
| --- | --- |
| Source | 4,096 bytes |
| Parser recursion / expression nesting | 64 / 64 |
| Parser error recovery | 16 |
| Comprehension nesting | 2 |
| Regex | Literal patterns only, 128 bytes, conservative expansion and compiled program size at most 512 |
| Native input strings and bytes | 65,536 aggregate bytes across exported fields, including strings inside lists |
| Native input lists | 4,096 aggregate elements, excluding byte slices |
| Evaluation cost | 10,000 by default |
| Cancellation polling | Every 16 comprehension iterations |

Input limits apply on every condition call, including constant expressions and unused exported fields. Length checks use frozen schema indexes and do not copy values. Invalid constant regex syntax fails program construction; oversized or dynamic patterns fail checking. Counted repeats are supported within the bounds.

Set a positive custom budget with `cel.NewCompiler[Price]("input", cel.WithCostLimit(5000))`. Repeated valid cost options use the last value; zero is rejected even if followed by a valid option. Cost measures CEL work, not wall time or total memory. Input bounds do not bound all intermediate allocations. A single built-in operation such as regex matching is not preempted midway; input-size validation, cost accounting, and context polling serve different purposes.

The condition checks the caller's context before and after evaluation, including constant expressions, and preserves both `ctx.Err()` and `context.Cause`. Evaluation remains synchronous; no background goroutine continues work after Fire returns. Internal CEL recovery may convert an internal panic to an ordinary error before the root panic policy sees it. Root `PropagatePanics` cannot undo third-party recovery.

Shared compilers, programs, and engines support independently owned inputs. Callers synchronize a shared mutable input, its slice storage, or callback captures around the entire Fire call. Conditions are read-only; Go actions may have partial effects. There is no rollback, retry, or automatic synchronization.

## Scope

This adapter provides conditions only. It does not implement CEL actions, a CEL policy runtime, arbitrary Go symbol/script/database execution, dynamic definitions, YAML runtime rules, hot reload, telemetry, or inference. Additional native mapping, protobuf, and explicit typed projectors are planned extensions; they are not current APIs. See the [roadmap](roadmap.md) and [compile/evaluation measurements](benchmarks.md#cel-condition-measurements).
