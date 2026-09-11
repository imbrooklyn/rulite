# Changelog

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
