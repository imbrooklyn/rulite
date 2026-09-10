# Changelog

## Unreleased

- Fix CEL-to-Go `int` / `uint` conversion to respect the destination platform's width, including defined integer types, trusted function parameters, native literal fields, pointers, and nested containers. Actual overflow still fails without truncation.
- Fix large native integer map keys across indexing, membership, optional access, and equality. On 64-bit targets, alpha.1 could report an existing key as absent, including a silent false result from `in` and optional access.
- Add integer boundary regression tests, Go-oracle fuzzing, and a 32-bit CEL check in CI.
- Separate development milestones from release versions in the roadmap. Public APIs remain provisional; these fixes do not change public signatures or the core execution contract.

## v0.1.0-alpha.1

Initial prerelease, commit `9f939c5`.

- Typed deterministic execution, reusable compilation, groups, partial results, explanations, traces, and isolated observer diagnostics.
- Typed CEL conditions and strict dynamic JSON definitions with explicitly registered Go actions.
- Atomic Runtime publication with snapshot identity and an optional OpenTelemetry adapter.

Requires Go 1.27 or later. For the native integer conversion limitation in this tag and temporary explicit-width mappings, see the [CEL guide](docs/cel.md).
