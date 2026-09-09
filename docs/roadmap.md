# Roadmap

Rulite is a typed, deterministic, single-pass business rules engine. The v0.1 core and v0.2 RuleSet/Compile, metadata, reusable engine construction, indexed validation queries, synchronous Observer/events, isolated Result diagnostics, and benchmark regression automation are implemented. v0.3 first-match/first-fire groups, mixed entry compilation, hierarchical explanations, group end reasons, ordered resolution/completion events, and payment/pricing examples are implemented. The CEL Condition adapter, strict dynamic definitions and typed action registry are available. Later versions describe future goals, not release dates or available APIs.

| Version | Goals | Explicit boundaries |
| --- | --- | --- |
| v0.1 | Typed Rule/Condition/Action, staged builder, immutable Engine, priority and registration order, All/Any/Not, execution policies, Result/Explain, opt-in Trace, errors, context, panic handling, concurrency tests, benchmarks, and examples | No public RuleSet/Compile, Observer, groups, CEL, dynamic definitions, reload, or telemetry adapter |
| v0.2 | Public RuleSet/Compile, metadata, observation-only Observer/events, isolated diagnostics, structural performance gates, and repeatable benchmark artifacts | No public CEL adapter, groups, or reload |
| v0.3 | First-match and first-fire groups, local resolution, ordered diagnostics, and payment/pricing examples with subsequent audit rules | No Agenda or activation model; global policy still controls execution |
| v0.4 | `rulite/cel` Condition adapter and `rulite/dynamic` strict definitions with an explicit typed action registry | No CEL actions, arbitrary scripts, or BRMS; root runtime remains independent of CEL |
| v0.5 | Atomic snapshot replacement, version/revision metadata, diagnostics, and an optional OpenTelemetry adapter | No distributed control plane or mutation of an active engine snapshot |
| v1.0 | Stable core and integration contracts supported by compatibility tests and production feedback | Forward chaining, Rete, and workflow orchestration are not prerequisites |

The [`rulite/cel` Condition adapter](cel.md) provides native and protobuf typed bindings, explicit JSON field mode, typed projectors, trusted unary scalar functions, compile-time boolean checks, bounded evaluation, context cancellation, and concurrent program reuse. Unknown and error outcomes remain ordinary Condition errors. The separate package keeps CEL dependencies outside the root runtime. Unsupported mappings require explicit conversion or fail construction; field names and presence follow the documented native or protobuf mode. [Dynamic definitions](dynamic-rules.md) add strict JSON and a frozen registry of explicitly trusted typed Go actions, producing ordinary rule sets without runtime decoding or arbitrary code discovery.

[Runtime atomic publication](runtime.md) and captured version/revision metadata are available. Every Fire uses one complete snapshot; direct Engine execution remains immutable. The OpenTelemetry adapter is still planned, so the complete v0.5 operations scope is not yet available.

Separate bounded inference may be considered from v0.6 only for demonstrated workloads. Incremental evaluation may be considered from v0.7 only if inference benchmarks justify it. Neither is implemented or part of `Fire`; Fire remains single-pass. Rete and Phreak have no planned version.

## Compatibility

Rule identity, priority order, Condition/Action signatures, match/fire distinctions, immutable engine ownership, and context/panic fundamentals are core stability candidates. During v0.x, builder names, policy constructors, Result accessors, error wrapper shapes, and Explanation/Trace structures may evolve. Exact explanation text is experimental; use structural accessors instead of parsing it.

Observer/Event, diagnostic, group, CEL, dynamic definition/registry, Runtime, and snapshot identity APIs are available but provisional during v0.x. Telemetry APIs remain planned. Internal representations are not public contracts.

## Non-goals

Rulite does not aim to replace every `if`, implement a custom rule language or YAML runtime, manage facts through Working Memory, or provide Agenda, Activation, TMS, workflow/BPMN, BRMS, arbitrary script execution, automatic rollback, or distributed rule execution. Details are in [architecture and semantics](architecture.md).
