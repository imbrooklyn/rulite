# Benchmarks

These measurements retain the v0.1 workloads in [benchmark_test.go](../benchmark_test.go), RuleSet/metadata measurements from [compile_benchmark_test.go](../compile_benchmark_test.go), and observation workloads from [observer_benchmark_test.go](../observer_benchmark_test.go). They are reproducible machine baselines, not a latency SLA or a prediction for business callbacks, network providers, or other hardware.

## Environment and reproduction

- Date: 2026-09-08.
- CPU: Apple M4 Pro; Go reports 12 logical CPUs and default GOMAXPROCS 12.
- OS: macOS 26.5.2 (25F84); GOOS/GOARCH: darwin/arm64.
- Toolchain: `go version go1.27.0 darwin/arm64`; module minimum: Go 1.27.
- Default compiler optimization and garbage collector settings; race instrumentation disabled for timing.
- Each workload ran three times with a 100 ms target duration. Tables report the median of each metric across those three samples. This short baseline does not estimate confidence intervals or tail latency.

From a checkout, reproduce with:

```sh
go version
go env GOOS GOARCH
go test -run '^$' -bench . -benchmem -benchtime=100ms -count=3
```

For less noise, repeat on an otherwise idle machine, use a longer benchtime, and compare distributions with identical toolchains and workloads. OS scheduling, CPU power state, garbage collection, and other applications affect results. The throughput column counts evaluated rules per second, not independent business decisions. A dash means the workload does not evaluate rules during timing.

## Method

Engine build timing includes validation, index creation, and mixed-priority sorting; rule definitions and closures are prepared outside timing. Fire timing uses a prebuilt engine and a reset input per iteration. Conditions perform simple typed reads and actions increment a counter. The 10% workload matches every tenth rule. Small count, stop, error-presence, and mutation assertions are included in Fire timings; structural ledger checks run outside timing. Every benchmark reports allocations and retains a result or counter.

Selection uses 1,000 rules, with the match at index 0, 500, or 999. Fallback has ten consecutive action errors before success. Error workloads use 100 rules; stop visits one and continue visits all. Combinator workloads use one rule with three leaves (or equivalent nesting), count child calls, and verify short-circuit behavior.

Summary and Trace use the same plain conditions at 10, 100, and 1,000 rules. These Trace numbers include per-rule timings but no nested child tree; complex combinator traces cost more. Explain benchmarks separately measure structural views and text rendering from an existing 100-rule Result with 10 matches. Lookup queries an existing 1,000-rule all-match Result.

Parallel benchmarks use exactly 1, 2, 4, 8, 16, or 32 worker goroutines, one shared engine, 1,000 rules with 10% matching, and a private reset input per worker. Work is divided across workers; GOMAXPROCS stays 12. Worker startup and synchronization are timed and amortized across operations. The reported ns/op is aggregate wall time divided by completed Fire calls, not individual request latency. Counters are combined only after workers finish.

## v0.1 baseline

Each table names its benchmark prefix; row labels are the exact sub-benchmark suffixes. Units are nanoseconds per operation, bytes per operation, allocations per operation, and evaluated rules per second.

### Construction

`BenchmarkBuildEngine`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `rules_10` | 435.20 | 1,056 | 9 | - |
| `rules_100` | 5,721.00 | 8,864 | 9 | - |
| `rules_1000` | 84,794.00 | 103,880 | 11 | - |
| `rules_10000` | 1,239,361.00 | 928,510 | 39 | - |

### Fire scale

`BenchmarkFireScale`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `all_miss/rules_10` | 116.80 | 0 | 0 | 85,581,054 |
| `all_match/rules_10` | 316.40 | 744 | 5 | 31,606,544 |
| `all_miss/rules_100` | 835.20 | 0 | 0 | 119,738,100 |
| `all_match/rules_100` | 2,264.00 | 6,120 | 8 | 44,175,649 |
| `ten_percent/rules_100` | 1,037.00 | 744 | 5 | 96,451,733 |
| `all_miss/rules_1000` | 8,057.00 | 0 | 0 | 124,109,205 |
| `all_match/rules_1000` | 23,874.00 | 77,800 | 12 | 41,886,197 |
| `ten_percent/rules_1000` | 9,493.00 | 6,120 | 8 | 105,340,948 |
| `all_miss/rules_10000` | 80,803.00 | 0 | 0 | 123,757,494 |
| `all_match/rules_10000` | 212,650.00 | 1,109,992 | 19 | 47,025,609 |
| `ten_percent/rules_10000` | 97,884.00 | 77,800 | 12 | 102,162,134 |

### Selection and fallback

`BenchmarkFireSelection`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `first_match/start` | 63.08 | 24 | 1 | 15,852,444 |
| `first_match/middle` | 4,079.00 | 24 | 1 | 122,829,866 |
| `first_match/end` | 8,217.00 | 24 | 1 | 121,700,105 |
| `first_fire/start` | 62.81 | 24 | 1 | 15,919,768 |
| `first_fire/middle` | 4,069.00 | 24 | 1 | 123,119,508 |
| `first_fire/end` | 8,140.00 | 24 | 1 | 122,848,945 |
| `first_fire/action_error_fallback` | 1,096.00 | 3,384 | 26 | 10,039,361 |

### Combinators

`BenchmarkFireCombinators`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `all_true` | 68.34 | 24 | 1 | 14,631,791 |
| `all_short_circuit` | 55.60 | 0 | 0 | 17,986,873 |
| `any_false` | 58.49 | 0 | 0 | 17,096,098 |
| `any_short_circuit` | 65.69 | 24 | 1 | 15,222,602 |
| `nested` | 73.85 | 24 | 1 | 13,540,662 |

### Errors and cancellation

`BenchmarkFireErrors`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `condition/stop` | 111.30 | 184 | 5 | 8,982,917 |
| `condition/continue` | 8,392.00 | 31,128 | 125 | 11,915,734 |
| `action/stop` | 115.30 | 184 | 5 | 8,670,552 |
| `action/continue` | 8,774.00 | 31,128 | 125 | 11,396,775 |
| `panic_recover` | 10,791.00 | 3,320 | 8 | 92,671 |
| `context_already_canceled` | 77.10 | 64 | 2 | - |

### Summary and Trace

`BenchmarkFireDiagnostics`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `summary/all_miss/rules_10` | 118.60 | 0 | 0 | 84,344,472 |
| `trace/all_miss/rules_10` | 1,126.00 | 2,656 | 22 | 8,882,406 |
| `summary/ten_percent/rules_10` | 133.10 | 24 | 1 | 75,125,919 |
| `trace/ten_percent/rules_10` | 1,177.00 | 2,680 | 23 | 8,498,537 |
| `summary/all_miss/rules_100` | 841.70 | 0 | 0 | 118,801,554 |
| `trace/all_miss/rules_100` | 10,203.00 | 25,760 | 202 | 9,801,151 |
| `summary/ten_percent/rules_100` | 1,060.00 | 744 | 5 | 94,379,160 |
| `trace/ten_percent/rules_100` | 10,678.00 | 26,504 | 207 | 9,364,662 |
| `summary/all_miss/rules_1000` | 8,162.00 | 0 | 0 | 122,512,028 |
| `trace/all_miss/rules_1000` | 95,864.00 | 258,336 | 2,002 | 10,431,480 |
| `summary/ten_percent/rules_1000` | 9,690.00 | 6,120 | 8 | 103,202,832 |
| `trace/ten_percent/rules_1000` | 100,233.00 | 264,457 | 2,010 | 9,976,764 |

### Explain on demand

`BenchmarkExplainOnDemand`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `rules_100/structural` | 3,093.00 | 10,880 | 1 | - |
| `rules_100/text` | 32,183.00 | 66,961 | 215 | - |

### Result.Rule lookup

`BenchmarkResultRuleLookup`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `rule/0` | 32.42 | 0 | 0 | - |
| `rule/500` | 31.09 | 0 | 0 | - |
| `rule/999` | 31.62 | 0 | 0 | - |
| `unknown` | 10.96 | 0 | 0 | - |

### Shared Engine concurrency

`BenchmarkFireParallel`

| Workload | ns/op | B/op | allocs/op | evaluated/s |
| --- | ---: | ---: | ---: | ---: |
| `workers_1` | 9,634.00 | 6,120 | 8 | 103,800,830 |
| `workers_2` | 5,446.00 | 6,120 | 8 | 183,635,300 |
| `workers_4` | 3,454.00 | 6,120 | 8 | 289,509,373 |
| `workers_8` | 2,658.00 | 6,120 | 8 | 376,232,174 |
| `workers_16` | 2,520.00 | 6,120 | 8 | 396,892,491 |
| `workers_32` | 2,483.00 | 6,120 | 8 | 402,819,339 |

## RuleSet and metadata measurements

Measured on 2026-09-08 with the same Apple M4 Pro, Go 1.27.0, macOS 26.5.2, darwin/arm64, and GOMAXPROCS 12 described above. The full suite ran 72 workloads, three samples each, using the reproduction command above. The following tables report medians; the v0.1 tables remain historical comparison points.

Compile prepares mixed priorities, names, descriptions, and two normalized tags per rule outside timing, then times validation, sorting, snapshot ownership, and index construction. Metadata normalization belongs to builder timing and is excluded here. Reusable engine construction starts from a compiled set and supplies a first-fire default; no executable nodes are copied. Counts and ledger checks run after construction timing, using fresh input. Lookup uses 1,000 rules; validation lookup selects two issues from an aggregate of 2,000. Tag and issue lookups include defensive-copy costs. Each benchmark uses `ReportAllocs`, an output sink, and correctness assertions.

### Construction

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkBuildEngine/rules_10` | 518.900 | 1,168 | 10 |
| `BenchmarkBuildEngine/rules_100` | 7,502.000 | 9,520 | 10 |
| `BenchmarkBuildEngine/rules_1000` | 118,388.000 | 112,088 | 12 |
| `BenchmarkBuildEngine/rules_10000` | 1,838,138.000 | 1,002,248 | 40 |
| `BenchmarkCompile/rules_10` | 668.200 | 1,152 | 9 |
| `BenchmarkCompile/rules_100` | 8,914.000 | 9,504 | 9 |
| `BenchmarkCompile/rules_1000` | 130,411.000 | 112,072 | 11 |
| `BenchmarkCompile/rules_10000` | 1,672,652.000 | 1,002,232 | 39 |
| `BenchmarkNewEngineFromRuleSet/rules_10` | 8.695 | 16 | 1 |
| `BenchmarkNewEngineFromRuleSet/rules_100` | 8.579 | 16 | 1 |
| `BenchmarkNewEngineFromRuleSet/rules_1000` | 8.584 | 16 | 1 |
| `BenchmarkNewEngineFromRuleSet/rules_10000` | 8.485 | 16 | 1 |

Full construction is more expensive than the historical v0.1 measurements, with larger metadata storage and one additional allocation for the convenience path. The short samples do not isolate every source of timing differences. Construction from an existing RuleSet uses one 16-byte allocation at every tested size; compile once when several engines need the same rules. BuildEngine and Compile use different callback and metadata fixtures, so their timing difference is not a measurement of constructor overhead alone.

### Metadata and validation lookup

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkRuleSetLookup/rule/0` | 14.750 | 0 | 0 |
| `BenchmarkRuleSetLookup/rule/500` | 14.790 | 0 | 0 |
| `BenchmarkRuleSetLookup/rule/999` | 14.790 | 0 | 0 |
| `BenchmarkRuleSetLookup/unknown` | 4.947 | 0 | 0 |
| `BenchmarkRuleSetLookup/tags_copy` | 16.780 | 32 | 1 |
| `BenchmarkValidationLookup/rule/500` | 29.930 | 80 | 1 |
| `BenchmarkValidationLookup/unknown` | 4.668 | 0 | 0 |

### Fire all-miss comparison

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkFireScale/all_miss/rules_10` | 118.300 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_100` | 844.400 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_1000` | 8,139.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_10000` | 82,470.000 | 0 | 0 |

The allocation test checks both construction paths at 1, 100, and 10,000 rules: no outcome records for misses, no trace storage, and zero allocations with the test callbacks. The existing Fire scale, selection, error, Trace, Explain, and concurrent workloads remain in the suite.

## Observation measurements

Measured on 2026-09-09: Apple M4 Pro, macOS 26.5.2 (25F84), Go 1.27.0, darwin/arm64, and default GOMAXPROCS 12. The complete suite ran 120 workloads with three 100 ms samples each. These tables show medians; earlier tables retain their dated historical measurements. Reproduce the full suite with the command above, or observation alone with:

```sh
go test -run '^$' -bench '^BenchmarkFireObserver$' -benchmem -benchtime=100ms -count=3
```

Each case uses a precompiled RuleSet and prebuilt engine, resetting input, delivery counter, and collection length per Fire. Modes cover no observer, a minimal observer with a counter and nil return, event collection, and error/panic at the first event followed by disabled observation. Trace combinations use the same callbacks and event sinks. All cases check business counts, stop reason, error absence, input mutations, event counts, and diagnostic counts. Diagnostic count checks include a defensive Diagnostics slice read in timed iterations. Collector storage is preallocated outside timing and reused by the consumer; allocation numbers exclude that buffer. No framework Result or event storage is recycled. Normal delivery counts are two execution events plus one evaluated event per rule and two additional events per match; first-fault cases deliver exactly once. Noop and collection do not read callback clocks unless Trace is enabled.

`BenchmarkFireObserver`, with 1,000 rules:

| Workload | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `none/all_miss/rules_1000` | 9,169 | 0 | 0 |
| `noop/all_miss/rules_1000` | 17,698 | 192 | 2 |
| `collect/all_miss/rules_1000` | 18,896 | 192 | 2 |
| `first_error/all_miss/rules_1000` | 9,261 | 224 | 3 |
| `first_panic/all_miss/rules_1000` | 31,075 | 7,440 | 7 |
| `trace_none/all_miss/rules_1000` | 92,624 | 258,336 | 2,002 |
| `trace_noop/all_miss/rules_1000` | 100,775 | 258,528 | 2,004 |
| `trace_collect/all_miss/rules_1000` | 105,507 | 258,528 | 2,004 |
| `none/ten_percent/rules_1000` | 11,220 | 6,120 | 8 |
| `noop/ten_percent/rules_1000` | 21,461 | 6,312 | 10 |
| `collect/ten_percent/rules_1000` | 23,187 | 6,312 | 10 |
| `first_error/ten_percent/rules_1000` | 11,313 | 6,344 | 11 |
| `first_panic/ten_percent/rules_1000` | 32,742 | 13,560 | 15 |
| `trace_none/ten_percent/rules_1000` | 100,227 | 264,456 | 2,010 |
| `trace_noop/ten_percent/rules_1000` | 109,449 | 264,648 | 2,012 |
| `trace_collect/ten_percent/rules_1000` | 114,308 | 264,648 | 2,012 |

The same modes run at 10 and 100 rules. Without Trace, normal all-miss observation uses two summary allocations totaling 192 bytes at all three sizes, with no per-rule event allocation. Panic recovery includes stack capture. Exporter I/O, queuing, synchronization, and callback work are absent from these fixtures; they add synchronous latency in a real application.

The retained Fire scale baseline and reusable construction also ran in the full suite:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkFireScale/all_miss/rules_10` | 137.0 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_100` | 935.5 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_1000` | 8,976 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_10000` | 89,997 | 0 | 0 |
| `BenchmarkNewEngineFromRuleSet/rules_1000` | 14.29 | 32 | 1 |

No-observer Fire preserves the zero-allocation gate; these timing samples are higher than the preceding dated baseline and do not establish unchanged CPU cost. Engine configuration now holds an observer interface, increasing reusable construction from the earlier 16-byte allocation to one 32-byte allocation at all four tested sizes. Short samples do not isolate all timing differences. Neither a universal throughput claim nor a machine-specific timing threshold follows from these numbers.

## Repeatable regression sampling

Use the Python standard-library [sampling tool](../scripts/benchmarks.py) with two checkouts on the same otherwise idle host. Supply an empty output directory:

```sh
python3 -B scripts/benchmarks.py "$REPORT_DIR" --baseline "$BASELINE_CHECKOUT" --samples 6 --benchtime 100ms
```

The tool builds one root test binary per revision outside measurement. Each of six paired samples starts fresh processes; baseline/candidate order alternates between pairs. Each process runs all benchmarks once with `-test.run=^$ -test.bench=. -test.benchmem -test.benchtime=100ms -test.count=1`. Go version, target settings, CPU headers, and worker counts must agree. Run without race or coverage instrumentation. Omit `--baseline` for candidate-only samples, or use `--bench` to narrow the workload expression.

Artifacts contain raw output, revision IDs and dirty flags, UTC time, OS/architecture, Go settings, GC/CPU environment settings, parsed samples, and JSON/Markdown comparisons. Missing metrics, failed benchmark assertions, changing workload sets within a revision, or incompatible environments fail collection. New and removed workloads are explicitly labeled.

For each common workload, the comparison uses candidate/baseline ns/op ratios paired by sample index. It estimates a 95% percentile bootstrap interval for their median with 10,000 resamples and fixed seed 271828. The default relative tolerance is 15% (`--tolerance 0.15`): a workload receives a review flag only when the interval's lower endpoint exceeds 1.15. Timing flags are advisory. Six short samples and uncorrected multiple comparisons do not prove equivalence, tail latency, or a universal performance guarantee; repeat with longer samples when investigating a flag. B/op and allocs/op medians are also retained.

Pull request CI samples the candidate and base revision sequentially on one runner. Push CI collects the candidate alone. CI uploads raw data and comparisons as `benchmark-results` artifacts for 14 days, including available evidence after failure. Allocation and ownership tests, tooling tests, and malformed or failed benchmark samples can block CI; timing flags do not. No comparison uses absolute ns/op thresholds across runners.

### v0.2 representative samples

Measured on 2026-09-09, Apple M4 Pro, 12 logical CPUs/default GOMAXPROCS 12, macOS 26.5.2 (Darwin 25.5.0), Go 1.27.0, darwin/arm64. Default optimization and GC settings; GOFLAGS and GOEXPERIMENT empty; race instrumentation disabled. The command above ran all 120 workloads six times per revision at 100 ms, comparing the preceding core revision with the current tooling/documentation changes. Core Go sources and workloads were identical. No workload crossed the advisory tolerance; this is a sampling check, not proof that future runtime changes have no cost. The tables contain candidate medians from these six samples.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkCompile/rules_10` | 680.400 | 1,152 | 9 |
| `BenchmarkCompile/rules_100` | 8,966.000 | 9,504 | 9 |
| `BenchmarkCompile/rules_1000` | 129,145.000 | 112,072 | 11 |
| `BenchmarkCompile/rules_10000` | 1,659,299.500 | 1,002,232 | 39 |
| `BenchmarkNewEngineFromRuleSet/rules_1000` | 11.600 | 32 | 1 |
| `BenchmarkRuleSetLookup/rule/500` | 13.945 | 0 | 0 |
| `BenchmarkValidationLookup/rule/500` | 29.055 | 80 | 1 |
| `BenchmarkFireScale/all_miss/rules_10` | 138.650 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_100` | 948.350 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_1000` | 9,051.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_10000` | 91,062.000 | 0 | 0 |

`BenchmarkFireObserver`, with 1,000 all-miss rules and the observation method described above:

| Workload | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `none/all_miss/rules_1000` | 9,161.000 | 0 | 0 |
| `noop/all_miss/rules_1000` | 17,832.500 | 192 | 2 |
| `collect/all_miss/rules_1000` | 18,951.500 | 192 | 2 |
| `first_error/all_miss/rules_1000` | 9,246.500 | 224 | 3 |
| `first_panic/all_miss/rules_1000` | 30,235.000 | 7,440 | 7 |
| `trace_noop/all_miss/rules_1000` | 97,381.000 | 258,528 | 2,004 |

## Local group measurements

Measured on 2026-09-09: Apple M4 Pro, 12 logical CPUs/default GOMAXPROCS 12, macOS 26.5.2 (25F84), Go 1.27.0, darwin/arm64. Default optimization and GC settings; no race instrumentation. The complete suite ran 143 workloads, three 100 ms samples each. The table reports metric medians. These short single-machine measurements are not an SLA or an estimate of tail latency.

```sh
go test . -run '^$' -bench . -benchmem -benchtime=100ms -count=3
go test . -run '^$' -bench '^(BenchmarkFireGroups|BenchmarkCompileEntries)$' -benchmem -benchtime=100ms -count=3
```

[Group workloads](../group_benchmark_test.go) prepare definitions and engines outside Fire timing, reset input each iteration, report allocations, and retain results and counters. Each Fire includes one group followed by a successful audit rule. Start, middle, and end select member positions 0, N/2, and N-1; all-miss evaluates every member before audit. The action-error case fails the first action: first-match resolves there, while first-fire tries the second member successfully. All use EvaluateAll with continued action errors. Counts, stop, group state, error presence, and action counters are checked inside timing; full ledger checks run outside it. Fire rows include the group lookup used for verification.

CompileEntries timing starts from frozen definitions, partitions 1,000 rules into 1, 10, or 100 groups, and includes validation, independent sorting, metadata, and indexes. Each group resolves once in the post-timing correctness check. Definition construction and callback execution are excluded from compile timing; these group partitions have different sorting work.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkCompile/rules_1000` | 205,061.000 | 144,904 | 12 |
| `BenchmarkNewEngineFromRuleSet/rules_1000` | 11.440 | 32 | 1 |
| `BenchmarkFireScale/all_miss/rules_10` | 153.200 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_1000` | 9,853.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_10000` | 98,666.000 | 0 | 0 |
| `BenchmarkCompileEntries/groups_1/rules_1000` | 207,130.000 | 191,416 | 31 |
| `BenchmarkCompileEntries/groups_10/rules_1000` | 157,483.000 | 192,600 | 34 |
| `BenchmarkCompileEntries/groups_100/rules_1000` | 117,567.000 | 204,728 | 34 |
| `BenchmarkFireGroups/first-match/start/members_1000` | 143.300 | 104 | 3 |
| `BenchmarkFireGroups/first-match/action_error/members_1000` | 191.300 | 264 | 7 |
| `BenchmarkFireGroups/first-fire/start/members_1000` | 142.600 | 104 | 3 |
| `BenchmarkFireGroups/first-fire/middle/members_1000` | 5,017.000 | 104 | 3 |
| `BenchmarkFireGroups/first-fire/end/members_1000` | 10,176.000 | 104 | 3 |
| `BenchmarkFireGroups/first-fire/all_miss/members_1000` | 10,090.000 | 56 | 2 |
| `BenchmarkFireGroups/first-fire/action_error/members_1000` | 225.100 | 360 | 8 |

No-group all-miss still allocates zero bytes across all tested scales and all three construction paths. Grouped all-miss keeps one group record; the benchmark's successful audit adds one sparse rule record. Resolving at the start retains one group record and two rule records regardless of the number of uncalled members. The structural test separately confirms bounded storage at 10, 100, and 10,000 members without per-member hole objects.

Compilation now stores explicit top-level and member coordinates and group boundaries. Its memory and timing costs are higher than the historical pure-rule baseline; reusable engine construction still shares the snapshot with one 32-byte allocation. No-group Fire timing is also higher in these samples. The dated measurements do not isolate all sources of timing variation or establish unchanged CPU cost. Correct results, zero-allocation all-miss execution without groups, and callback-free retained metadata remain structural gates.

## Group diagnostics measurements

Measured on 2026-09-09: Apple M4 Pro, default GOMAXPROCS 12, macOS 26.5.2 (25F84), Go 1.27.0, darwin/arm64. Default optimization and GC settings, empty GOFLAGS/GOEXPERIMENT, no race instrumentation. The full suite ran 272 workloads with three 100 ms samples each; the table reports medians. Earlier measurements remain dated comparison points. These samples do not establish tail latency or a universal service budget.

```sh
go test . -run '^$' -bench . -benchmem -benchtime=100ms -count=3
go test . -run '^$' -bench '^(BenchmarkGroupDiagnostics|BenchmarkGroupViews)$' -benchmem -benchtime=100ms -count=3
```

[Diagnostic workloads](../group_diagnostics_benchmark_test.go) add 120 Fire cases: both group kinds, 1 group with 10 or 1,000 members or 10 groups with 100 members each, five outcome patterns, and four Trace/Observer combinations. Each execution ends with one successful audit rule. Start, middle, and end resolve at positions 0, N/2, and N-1; all-miss exhausts each group. Fallback fails the first action: first-match resolves there, while first-fire succeeds on the second. Global policy is EvaluateAll with continued action errors. Definitions, compilation, and engine construction are outside Fire timing. Input and observer counters reset each iteration; counts, stop, error presence, mutations, total deliveries, resolutions, and completions are checked inside timing. Full structural checks run before timing. Each case reports allocations and retains a result or counter.

Nine view cases start from an existing traced first-fire fallback Result. Entries timing copies the top-level collection and every member collection. Text timing formats all rules, group ends, failures, and holes. Trace view timing copies group and rule collections. These are on-demand costs separate from Fire. The original compile and no-group workloads also ran unchanged; the retained FireGroups case includes its group lookup assertion, whereas GroupDiagnostics uses event counters.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkCompile/rules_1000` | 214,603.000 | 144,904 | 12 |
| `BenchmarkNewEngineFromRuleSet/rules_1000` | 11.680 | 32 | 1 |
| `BenchmarkFireScale/all_miss/rules_10` | 157.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_1000` | 10,046.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_10000` | 100,187.000 | 0 | 0 |
| `BenchmarkFireGroups/first-fire/start/members_1000` | 152.600 | 104 | 3 |
| `BenchmarkGroupDiagnostics/first-fire/start/summary/groups_1/members_1000` | 142.600 | 104 | 3 |
| `BenchmarkGroupDiagnostics/first-fire/start/trace/groups_1/members_1000` | 14,915.000 | 98,760 | 9 |
| `BenchmarkGroupDiagnostics/first-fire/start/observer/groups_1/members_1000` | 346.200 | 488 | 7 |
| `BenchmarkGroupDiagnostics/first-fire/start/trace_observer/groups_1/members_1000` | 14,958.000 | 99,144 | 13 |
| `BenchmarkGroupDiagnostics/first-fire/all_miss/summary/groups_1/members_1000` | 10,175.000 | 56 | 2 |
| `BenchmarkGroupDiagnostics/first-fire/all_miss/trace/groups_1/members_1000` | 97,731.000 | 258,552 | 2,006 |
| `BenchmarkGroupDiagnostics/first-fire/all_miss/observer/groups_1/members_1000` | 19,233.000 | 344 | 5 |
| `BenchmarkGroupDiagnostics/first-fire/all_miss/trace_observer/groups_1/members_1000` | 107,903.000 | 258,840 | 2,009 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/summary/groups_1/members_1000` | 237.700 | 360 | 8 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/trace/groups_1/members_1000` | 15,150.000 | 99,176 | 16 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/observer/groups_1/members_1000` | 472.400 | 744 | 12 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/trace_observer/groups_1/members_1000` | 15,010.000 | 99,560 | 20 |
| `BenchmarkGroupDiagnostics/first-fire/middle/summary/groups_1/members_1000` | 5,063.000 | 104 | 3 |
| `BenchmarkGroupDiagnostics/first-fire/end/summary/groups_1/members_1000` | 10,246.000 | 104 | 3 |
| `BenchmarkGroupDiagnostics/first-match/fallback/summary/groups_1/members_1000` | 196.300 | 264 | 7 |
| `BenchmarkGroupDiagnostics/first-fire/start/observer/groups_1/members_10` | 343.100 | 488 | 7 |
| `BenchmarkGroupDiagnostics/first-fire/start/observer/groups_10/members_100` | 1,786.000 | 3,176 | 28 |
| `BenchmarkGroupViews/entries/groups_1/members_1000` | 29,828.000 | 139,776 | 2 |
| `BenchmarkGroupViews/text/groups_1/members_1000` | 625,718.000 | 1,312,212 | 8,527 |
| `BenchmarkGroupViews/trace/groups_1/members_1000` | 43,296.000 | 237,664 | 2 |
| `BenchmarkGroupViews/entries/groups_10/members_100` | 30,893.000 | 146,432 | 11 |
| `BenchmarkGroupViews/text/groups_10/members_100` | 624,617.000 | 1,305,466 | 7,835 |
| `BenchmarkGroupViews/trace/groups_10/members_100` | 44,444.000 | 238,592 | 2 |

No-group all-miss still has zero allocations. Group end facts fit in the existing per-group record. Without Trace, resolving a group of 1,000 members immediately uses the same 104 bytes and three allocations as the retained group baseline. Normal observation adds two execution summaries and one independent snapshot per emitted group event: two for a resolved group, one for exhaustion. Increasing the uncalled suffix from 10 to 1,000 members does not add summary or event allocations. More entered groups create more group snapshots.

Trace retains a complete view of all definitions, including uncalled members; that storage can be substantial even when selection happens immediately. Text explanations allocate considerably more than structural views and are intended for on-demand inspection. Results and event snapshots retain their outcomes without pooling; execution reads duration clocks only when Trace is enabled. Short timing differences from the preceding samples do not prove equivalent CPU cost; the allocation and callback-free ownership gates remain the regression criteria.

## Group scale and business decision costs

A full sample on 2026-09-09 used the same Apple M4 Pro, macOS 26.5.2 (25F84), Go 1.27.0, darwin/arm64, and default GOMAXPROCS 12 described above. All 380 workloads ran three times at 100 ms each without race instrumentation. These are per-metric medians from that run:

```sh
go test . -run '^$' -bench . -benchmem -benchtime=100ms -count=3
```

[Group execution and compilation](../group_benchmark_test.go) cover 10, 100, 1,000, and 10,000 members, both group kinds, and success at the start, middle, or end, all misses, and a failed action. First-match resolves on that failed action; first-fire falls back to the next successful action. Compilation distributes 10-10,000 rules across 1, 10, or 100 groups where the total permits it.

[Group diagnostics](../group_diagnostics_benchmark_test.go) additionally cover 10 groups of 1,000 members and 100 groups of 100 members. Each group independently exercises the named scenario; the member count is per group. Summary, Trace, Observer, and Trace with Observer use the same definitions. Fire uses a prebuilt engine and resets input and event counters per iteration, with outcome checks and allocation reporting. CompileEntries is timed separately. GroupViews reads an already completed Result, copying all members for entries, rendering all rules for text, or copying Trace collections.

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkFireScale/all_miss/rules_10` | 155.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_100` | 1,022.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_1000` | 9,852.000 | 0 | 0 |
| `BenchmarkFireScale/all_miss/rules_10000` | 98,866.000 | 0 | 0 |
| `BenchmarkFireGroups/first-fire/start/members_10` | 147.800 | 104 | 3 |
| `BenchmarkFireGroups/first-fire/start/members_100` | 146.400 | 104 | 3 |
| `BenchmarkFireGroups/first-fire/start/members_1000` | 146.700 | 104 | 3 |
| `BenchmarkFireGroups/first-fire/start/members_10000` | 146.700 | 104 | 3 |
| `BenchmarkCompileEntries/groups_1/rules_10` | 1,102.000 | 2,056 | 14 |
| `BenchmarkCompileEntries/groups_10/rules_100` | 10,058.000 | 16,936 | 23 |
| `BenchmarkCompileEntries/groups_10/rules_1000` | 151,678.000 | 192,600 | 34 |
| `BenchmarkCompileEntries/groups_100/rules_10000` | 1,474,410.000 | 1,698,329 | 93 |
| `BenchmarkGroupDiagnostics/first-fire/start/summary/groups_10/members_1000` | 516.000 | 1,064 | 6 |
| `BenchmarkGroupDiagnostics/first-fire/middle/summary/groups_10/members_1000` | 51,246.000 | 1,064 | 6 |
| `BenchmarkGroupDiagnostics/first-fire/end/summary/groups_10/members_1000` | 101,657.000 | 1,064 | 6 |
| `BenchmarkGroupDiagnostics/first-fire/all_miss/summary/groups_10/members_1000` | 101,137.000 | 344 | 2 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/summary/groups_10/members_1000` | 1,431.000 | 4,472 | 28 |
| `BenchmarkGroupDiagnostics/first-fire/start/trace/groups_10/members_1000` | 72,392.000 | 969,512 | 30 |
| `BenchmarkGroupDiagnostics/first-fire/start/observer/groups_10/members_1000` | 1,647.000 | 3,176 | 28 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/trace_observer/groups_10/members_1000` | 71,636.000 | 976,632 | 94 |
| `BenchmarkGroupDiagnostics/first-fire/start/summary/groups_100/members_100` | 3,546.000 | 9,576 | 9 |
| `BenchmarkGroupDiagnostics/first-fire/all_miss/summary/groups_100/members_100` | 102,571.000 | 3,480 | 2 |
| `BenchmarkGroupDiagnostics/first-fire/fallback/summary/groups_100/members_100` | 10,560.000 | 40,728 | 127 |
| `BenchmarkGroupViews/entries/groups_10/members_100` | 26,633.000 | 146,432 | 11 |
| `BenchmarkGroupViews/text/groups_10/members_100` | 562,994.000 | 1,305,106 | 7,834 |
| `BenchmarkGroupViews/trace/groups_10/members_100` | 36,103.000 | 238,592 | 2 |

A single immediately resolved group still costs 104 B/op and three allocations across 10-10,000 members. Summary stores group facts and sparse evaluated outcomes without a record per uncalled member. More groups require more records: ten immediately resolved groups use 1,064 bytes; 100 use 9,576 bytes. These outcomes still explain every local hole. First-fire fallback retains the failed attempt as well as the successful action.

Trace retains all 10,000 member definitions even when ten successful actions resolve ten groups immediately: the sampled start case uses 969,512 B/op. Normal observation adds group snapshots and execution summaries; the corresponding observer-only case uses 3,176 B/op. Full text formatting remains an on-demand cost. None of these measurements represents payment provider latency or a business service budget.

A separate six-pair comparison with the preceding grouped runtime covered 36 common workloads and explicitly recorded 28 new scale workloads. It alternated baseline/candidate order using the method above:

```sh
python3 scripts/benchmarks.py /tmp/rulite-group-comparison \
  --baseline /path/to/baseline-checkout --samples 6 --benchtime 100ms \
  --bench '^(BenchmarkFireScale|BenchmarkFireGroups|BenchmarkCompileEntries|BenchmarkExplainOnDemand)$'
```

No common workload crossed the advisory threshold. The three common CompileEntries workloads had paired median ratios of 1.096-1.146, so the result does not establish equal CPU cost. Timing remains advisory, and this focused comparison does not cover every diagnostic workload. The full allocation and ownership gates also passed; no-group all-miss remained at zero allocations at all four scales.

## Structural performance guarantees

Ordinary no-trace, no-observer all-miss Fire measured 0 B/op and 0 allocs/op at every tested scale. The allocation regression test also checks that allocation does not grow with rule count and that no per-rule outcome records are created for misses. This guarantee concerns framework execution storage; caller callbacks and contexts may allocate.

Sorting, validation, normalization, and indexes belong to construction, and Trace-off execution reads no duration clock. Successful and failed outcomes retain the records required for Result/Explain correctness. Full tracing and text formatting intentionally cost more; enable tracing per execution and render explanations when needed. No hardware-specific ns/op threshold is enforced.

All-match and error-heavy workloads allocate for retained outcomes and failures. A successful fallback still retains its earlier errors. Shared-engine throughput is subject to CPU count, allocator pressure, and callback behavior; these results do not promise linear scaling. Measure your own rule mix and input ownership pattern before setting a service budget.
