# Roadmap

Rulite v0.1 is a typed, deterministic, single-pass business rules engine. Later versions below are planned goals, not available APIs or delivery commitments.

| Version | Goals | Explicit boundaries |
| --- | --- | --- |
| v0.1 | Typed Rule/Condition/Action, staged builder, immutable Engine, priority and registration order, All/Any/Not, execution policies, Result/Explain, opt-in Trace, errors, context, panic handling, concurrency tests, benchmarks, and examples | No public RuleSet/Compile, Observer, groups, CEL, dynamic definitions, reload, or telemetry adapter |
| v0.2 | Public RuleSet/Compile, metadata, observation-only Observer/events, benchmark regression automation, and an internal CEL feasibility study | No public CEL adapter, groups, or reload |
| v0.3 | First-match and first-fire groups with local resolution and group explanations | No Agenda or activation model; global policy still controls execution |
| v0.4 | Optional CEL Condition adapter; later, dynamic definitions and an explicit typed action registry | No CEL actions, arbitrary scripts, or BRMS; root runtime remains independent of CEL |
| v0.5 | Atomic snapshot replacement, version/revision metadata, diagnostics, and an optional OpenTelemetry adapter | No distributed control plane or mutation of an active engine snapshot |
| v1.0 | Stable core and integration contracts supported by compatibility tests and production feedback | Forward chaining, Rete, and workflow orchestration are not prerequisites |

Separate bounded inference may be considered from v0.6 only for demonstrated workloads. Incremental evaluation may be considered from v0.7 only if inference benchmarks justify it. Neither is implemented or part of `Fire`; Fire remains single-pass. Rete and Phreak have no planned version.

## Compatibility

Rule identity, priority order, Condition/Action signatures, match/fire distinctions, immutable engine ownership, and context/panic fundamentals are core stability candidates. During v0.x, builder names, policy constructors, Result accessors, error wrapper shapes, and Explanation/Trace structures may evolve. Exact explanation text is experimental; use structural accessors instead of parsing it.

Future Observer, groups, CEL, dynamic definitions, runtime replacement, and telemetry APIs remain experimental until their owning releases. Internal representations are not public contracts.

## Non-goals

Rulite does not aim to replace every `if`, implement a custom rule language or YAML runtime, manage facts through Working Memory, or provide Agenda, Activation, TMS, workflow/BPMN, BRMS, arbitrary script execution, automatic rollback, or distributed rule execution. Details are in [architecture and semantics](architecture.md).
