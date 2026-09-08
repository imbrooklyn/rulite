# Contributing

Use Go 1.27 or newer. Keep the root runtime dependent only on the standard library and keep changes focused on the [documented scope](docs/architecture.md).

## Checks

Before proposing a change, format Go files and run:

```sh
gofmt -w *.go examples/*/*.go
go vet ./...
go test ./...
go test -race ./...
```

Run each fuzz target independently for a short smoke check; increase the duration when changing its semantics:

```sh
for target in FuzzCompileMetadata FuzzRuleValidation FuzzExecutionOutcomes FuzzCombinatorNesting FuzzExecutionErrorTree FuzzObservationStream; do
    go test -run '^$' -fuzz "^${target}$" -fuzztime=5s -parallel=2 || exit 1
done
```

Keep fuzz inputs bounded, seed useful edge cases, and retain reproducible failures as regression cases. Concurrent engine tests must use distinct inputs or explicit caller synchronization. Result and Explain assertions should share facts rather than duplicate execution logic.

## Performance

```sh
go test -run '^$' -bench . -benchmem -benchtime=100ms -count=3
```

Build engines outside Fire timing, reset mutable input between executions, verify outcome counts, and report allocations. Record hardware, Go version, and methodology when comparing results; do not enforce machine-specific nanosecond thresholds. See [benchmarks](docs/benchmarks.md).

## API and documentation

Explain the concrete behavior changed and how it was verified. Public API changes need a compiling external-consumer example, focused semantic tests, English Go doc, and updated public documentation and examples. Preserve deterministic order, error visibility, context boundaries, result ownership, and match/fire distinctions. Never equate Fired with a field mutation; use business provenance when required.

Keep public documentation, comments, messages, and examples in clear English. Roadmap entries describe future goals without presenting them as implemented. Avoid adding dependencies or abstractions for features outside the current version.
