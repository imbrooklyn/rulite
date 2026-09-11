# Changelog

## v0.1.0-alpha.3

- Bound CEL-to-native literal construction before allocating destination storage. Lazy concatenations, nested collections, optional payloads, and all fields of a literal share finite conversion budgets; oversized intermediate values fail with the new `cel.ErrNativeLimit` sentinel.
- Bound native object equality and memoize completed pointer pairs within each comparison. Shared subgraphs no longer cause exponential traversal; hidden-field, nil-presence, timestamp, and NaN semantics are preserved. Budget exhaustion aborts evaluation even inside collection equality or membership.
- Preserve CEL null in native pointer fields and container elements, including identity-mapped lists and nested maps. Non-pointer destinations still reject null.
- Add structured construction/equality fuzz targets, pre-allocation boundary tests, shared-graph and concurrent-program regressions, and an external error-handling example. CI now runs the complete CEL suite on 32-bit Go.

Compatibility: native literal conversion and equality now enforce the documented per-operation limits. Implicit `dyn(map)` conversion to Go structs is rejected because it bypasses native mapping and budgets; use a typed native object literal instead. Public function signatures and core execution contracts are unchanged. Requires Go 1.27 or later.

## v0.1.0-alpha.2

- Fix traced condition combinators when a callback reuses its context recursively or concurrently. Each invocation owns its child recordings, shared recorder state is synchronized, and trace snapshots remain immutable. User callbacks run without recorder locks.
- Fix native CEL literal construction for scalar pointers, including narrow integers, defined string/bool types, timestamps, durations, and nested containers. Preserve exact Go types and reject integer overflow.
- Fix CEL-to-Go `int` / `uint` conversion to respect the destination platform's width, including defined integer types, trusted function parameters, native literal fields, pointers, and nested containers. Actual overflow still fails without truncation.
- Fix large native integer map keys across indexing, membership, optional access, and equality. On 64-bit targets, alpha.1 could report an existing key as absent, including a silent false result from `in` and optional access.
- Add trace reentrancy and concurrency regressions, scalar pointer boundary tests, Go-oracle integer fuzzing, and 32-bit CEL integer and pointer checks in CI.
- Separate development milestones from release versions in the roadmap. Public APIs remain provisional; these fixes do not change public signatures or the core execution contract.

## v0.1.0-alpha.1

Initial prerelease, commit `9f939c5`.

- Typed deterministic execution, reusable compilation, groups, partial results, explanations, traces, and isolated observer diagnostics.
- Typed CEL conditions and strict dynamic JSON definitions with explicitly registered Go actions.
- Atomic Runtime publication with snapshot identity and an optional OpenTelemetry adapter.

Requires Go 1.27 or later. For the native integer conversion limitation in this tag and temporary explicit-width mappings, see the [CEL guide](docs/cel.md).
