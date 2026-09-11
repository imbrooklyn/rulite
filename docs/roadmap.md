# Roadmap

Rulite is a typed, deterministic, single-pass business rules engine. The current published version is **v0.1.0-alpha.3**. All implemented capabilities below first shipped in alpha.1; their implementation order does not imply separate v0.2–v0.5 releases. Alpha.2 hardens trace recording and native CEL conversions; alpha.3 bounds native CEL construction and equality and preserves null pointer elements. Release-specific changes are tracked in the [changelog](../CHANGELOG.md).

## Capability milestones

These milestones describe development scope and status, not release versions or promised dates. The former v0.1–v0.5 roadmap labels have been replaced with capability names.

| Milestone | Status | Scope and boundaries |
| --- | --- | --- |
| Core execution | Implemented; alpha.1 | Typed Rule/Condition/Action, deterministic ordering, policies, Result/Explain/Trace, errors, context, panic handling, concurrency tests, benchmarks, and examples |
| Reusable compilation and observation | Implemented; alpha.1 | Public RuleSet/Compile, metadata, immutable engine reuse, indexed validation, synchronous Observer/events, isolated diagnostics, and benchmark regression automation |
| Local selection groups | Implemented; alpha.1 | First-match/first-fire groups, mixed entry compilation, hierarchical explanations, ordered diagnostics, and payment/pricing examples; no Agenda or activation model |
| CEL and dynamic definitions | Implemented; alpha.1 | Typed CEL conditions and strict definitions with an explicit Go action registry; no CEL actions, arbitrary scripts, or BRMS; root runtime remains independent of CEL |
| Runtime and telemetry | Implemented; alpha.1 | Atomic snapshot replacement, version/revision metadata, and optional OpenTelemetry; no distributed control plane or mutation of an active snapshot |
| Integration hardening | In progress | Native conversion correctness, platform and dependency compatibility tests, clear release notes, and controlled business trials |
| Stable public contracts | Planned for v1.0 | Stabilization requires compatibility tests and production feedback; inference, Rete, and workflow orchestration are not prerequisites |

The [`rulite/cel` Condition adapter](cel.md) provides native and protobuf typed bindings, explicit JSON field mode, typed projectors, trusted unary scalar functions, compile-time boolean checks, bounded evaluation, context cancellation, and concurrent program reuse. Unknown and error outcomes remain ordinary Condition errors. The separate package keeps CEL dependencies outside the root runtime. Unsupported mappings require explicit conversion or fail construction; field names and presence follow the documented native or protobuf mode. [Dynamic definitions](dynamic-rules.md) add strict JSON and a frozen registry of explicitly trusted typed Go actions, producing ordinary rule sets without runtime decoding or arbitrary code discovery.

[Runtime atomic publication](runtime.md), captured version/revision metadata, and the [OpenTelemetry adapter](observability.md) are available. Every Fire uses one complete snapshot; direct Engine execution remains immutable. The adapter uses explicit providers, bounded metric attributes, and one execution span with limited ordered rule events. It preserves core business outcomes and leaves export resources with the provider owner. The [combined payment reload example](../examples/runtime_reload) includes strict configuration validation, CEL provider eligibility, typed groups/audit, concurrent publication, partial outcomes, and independent telemetry diagnostics. `dynamic.CompileRules` enables construction with code-owned groups without expanding the JSON schema.

Separate bounded inference may be considered only for demonstrated workloads. Incremental evaluation would additionally require supporting performance evidence. Neither is implemented, scheduled for a release, or part of `Fire`; Fire remains single-pass. Rete and Phreak have no planned version.

## Compatibility

Rule identity, priority order, Condition/Action signatures, match/fire distinctions, immutable engine ownership, and context/panic fundamentals are core stability candidates. During v0.x, builder names, policy constructors, Result accessors, error wrapper shapes, and Explanation/Trace structures may evolve. Exact explanation text is experimental; use structural accessors instead of parsing it.

Observer/Event, diagnostic, group, CEL, dynamic definition/registry, Runtime, snapshot identity, and OpenTelemetry adapter APIs are available but provisional during v0.x. Internal representations are not public contracts.

The v1.0 compatibility review covers the actual exported surface, including the following families. These remain v0.x contracts until that review and production feedback justify stabilization; internal storage and exact explanation text are excluded.

| Package | Compatibility surface |
| --- | --- |
| Root | Rule/builders, Condition/Action/combinators, Compile/CompileEntries, RuleSet/Engine/Runtime, policy/options, RuleID/GroupID and ordering, Result/Explanation/Trace, group views, Failure/ExecutionError and context/panic semantics, Observer/Event/Diagnostic, SnapshotInfo/version/revision/digest |
| CEL | Builder/Compiler, typed Bind/BindProto/Function methods, mapping options and limits, compile/runtime errors, cooperative cancellation and callback ownership |
| Dynamic | Strict Definition/Decode, Compile/CompileJSON/CompileRules, typed Registry/Register/Freeze, parameter ownership, validation order, limits and compile errors |
| OpenTelemetry | New/Adapter.Fire, immutable options and allowlists, metric names/units/attributes, span/event projection and limits, diagnostic isolation, Observer replacement and provider ownership |

Before stabilizing APIs, controlled business trials should check that real rule sets compile against the intended mappings, earlier failures remain visible after fallback, and callers handle partial side effects without assuming rollback. Reload trials should validate the next snapshot before publication and record the identity from each Result. Measure CEL evaluation, projections, callbacks, and telemetry with representative data; simple core benchmarks are not business throughput estimates.

No production-readiness claim follows from unit coverage or short benchmarks alone. v1.0 does not require bounded inference or incremental evaluation, and neither would change the single-pass Fire contract.

## Non-goals

Rulite does not aim to replace every `if`, implement a custom rule language or YAML runtime, manage facts through Working Memory, or provide Agenda, Activation, TMS, workflow/BPMN, BRMS, arbitrary script execution, automatic rollback, or distributed rule execution. Details are in [architecture and semantics](architecture.md).
