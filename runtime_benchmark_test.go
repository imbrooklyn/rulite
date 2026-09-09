package rulite_test

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/imbrooklyn/rulite"
)

var benchmarkSnapshot rulite.SnapshotInfo

var benchmarkRetained []rulite.Result
var benchmarkChurnRuntime *rulite.Runtime[swapInput]

func BenchmarkRuntimeChurn(b *testing.B) {
	for _, keep := range []int{0, 32} {
		b.Run(fmt.Sprintf("retained_results_%d", keep), func(b *testing.B) {
			var target rulite.Runtime[swapInput]
			retained := make([]rulite.Result, keep)
			iteration := 0
			var input swapInput
			b.ReportAllocs()
			for b.Loop() {
				// This workload deliberately times construction, publication, and
				// execution together to measure churn, separately from Fire costs.
				data := make([]byte, 1<<20)
				data[len(data)-1] = 7
				rules := make([]rulite.Rule[swapInput], 16)
				for i := range rules {
					rules[i] = rulite.NewRule[swapInput](rulite.RuleID(fmt.Sprintf("rule/%d", i))).
						Description(fmt.Sprintf("snapshot/%d/rule/%d ", iteration, i) + strings.Repeat("m", 2048)).
						When(func(context.Context, *swapInput) (bool, error) { return i == 0 && data[len(data)-1] == 7, nil }).
						Then(func(_ context.Context, p *swapInput) error { p.actions++; return nil })
				}
				set, err := rulite.Compile(rules...)
				if err != nil {
					b.Fatal(err)
				}
				version := rulite.RuleSetVersion(fmt.Sprintf("version/%d", iteration))
				set, err = set.WithIdentity(version, "")
				if err != nil {
					b.Fatal(err)
				}
				e, err := rulite.NewEngineFromRuleSet(set)
				if err != nil {
					b.Fatal(err)
				}
				identity, err := target.Publish(e)
				if err != nil || identity.Revision() != rulite.SnapshotRevision(iteration+1) {
					b.Fatal("churn publication changed")
				}
				input = swapInput{}
				r, err := target.Fire(context.Background(), &input)
				if err != nil || r.Snapshot() != identity || r.Snapshot().Version() != version || r.Counts() != (rulite.Counts{Total: 16, Evaluated: 16, Unmatched: 15, Matched: 1, Fired: 1}) || r.StopReason() != rulite.StopCompleted || input.actions != 1 {
					b.Fatal("churn execution changed")
				}
				if keep != 0 {
					retained[iteration%keep] = r
				}
				iteration++
			}
			benchmarkChurnRuntime, benchmarkRetained = &target, retained
			benchmarkCounter = iteration
			runtime.KeepAlive(retained)
		})
	}
}

func BenchmarkSnapshotFire(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		for _, matched := range []bool{false, true} {
			for _, path := range []string{"engine", "runtime"} {
				b.Run(fmt.Sprintf("%s/matched_%t/rules_%d", path, matched, size), func(b *testing.B) {
					set := mustCompile(b, benchmarkRules(size, func(i int) byte {
						if matched && i%10 == 0 {
							return 1
						}
						return 0
					})...)
					set, err := set.WithIdentity("pricing/v1", "source/v1")
					if err != nil {
						b.Fatal(err)
					}
					engine := mustReuse(b, set)
					fire := engine.Fire
					var revision rulite.SnapshotRevision
					if path == "runtime" {
						runtime, err := rulite.NewRuntime(engine)
						if err != nil {
							b.Fatal(err)
						}
						fire, revision = runtime.Fire, 1
					}
					matches := 0
					if matched {
						matches = size / 10
					}
					want := benchmarkCompleted(size, matches)
					var result rulite.Result
					var input benchmarkInput
					b.ReportAllocs()
					for b.Loop() {
						input = benchmarkInput{}
						result, err = fire(context.Background(), &input)
						if err != nil || result.Counts() != want.counts || result.StopReason() != want.stop || input.actions != want.actions || result.Snapshot().Revision() != revision || result.Snapshot().Version() != "pricing/v1" || result.Snapshot().SourceDigest() != "source/v1" {
							b.Fatal("snapshot workload changed")
						}
					}
					benchmarkResult, benchmarkCounter = result, input.actions
					b.ReportMetric(float64(b.N)*float64(size)/b.Elapsed().Seconds(), "evaluated/s")
				})
			}
		}
	}
}

type swapInput struct{ generation, actions int }

func swapEngine(b *testing.B, size, generation int) *rulite.Engine[swapInput] {
	b.Helper()
	rules := make([]rulite.Rule[swapInput], size)
	for i := range rules {
		rules[i] = rulite.NewRule[swapInput](rulite.RuleID(fmt.Sprintf("rule/%d", i))).
			When(func(_ context.Context, input *swapInput) (bool, error) {
				if i > 0 && (input.generation != generation || input.actions != (i+9)/10) {
					return false, benchmarkError
				}
				return i%10 == 0, nil
			}).Then(func(_ context.Context, input *swapInput) error {
			input.generation = generation
			input.actions++
			return nil
		})
	}
	set, err := rulite.Compile(rules...)
	if err != nil {
		b.Fatal(err)
	}
	set, err = set.WithIdentity(rulite.RuleSetVersion(fmt.Sprintf("version/%d", generation)), rulite.SourceDigest(fmt.Sprintf("digest/%d", generation)))
	if err != nil {
		b.Fatal(err)
	}
	e, err := rulite.NewEngineFromRuleSet(set)
	if err != nil {
		b.Fatal(err)
	}
	return e
}

func BenchmarkRuntimeSwapScale(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		for _, workers := range []int{1, 2, 4, 8, 16, 32} {
			for _, every := range []int{-1, 0, 1, 32, 1024} {
				path := fmt.Sprintf("runtime/publish_every_%d", every)
				if every == -1 {
					path = "engine"
				}
				b.Run(fmt.Sprintf("rules_%d/workers_%d/%s", size, workers, path), func(b *testing.B) {
					engines := [2]*rulite.Engine[swapInput]{swapEngine(b, size, 0), swapEngine(b, size, 1)}
					runtime, err := rulite.NewRuntime(engines[0])
					if err != nil {
						b.Fatal(err)
					}
					fire := runtime.Fire
					if every == -1 {
						fire = engines[0].Fire
					}
					completed, published := make([]int, workers), make([]int, workers)
					want := benchmarkCompleted(size, size/10).counts
					var wg sync.WaitGroup
					start := make(chan struct{})
					b.ReportAllocs()
					b.ResetTimer()
					for worker := range workers {
						wg.Go(func() {
							<-start
							var input swapInput
							for operation := worker; operation < b.N; operation += workers {
								if every > 0 && operation%every == 0 {
									e := engines[(operation/every)%2]
									info, err := runtime.Publish(e)
									if err != nil || info.Version() != e.Snapshot().Version() || info.SourceDigest() != e.Snapshot().SourceDigest() {
										b.Error("publication identity changed")
										return
									}
									published[worker]++
								}
								input = swapInput{}
								r, err := fire(context.Background(), &input)
								if err != nil || input.generation < 0 || input.generation > 1 || input.actions != size/10 || r.Counts() != want || r.StopReason() != rulite.StopCompleted || (r.Snapshot().Revision() == 0) != (every == -1) || r.Snapshot().Version() != engines[input.generation].Snapshot().Version() || r.Snapshot().SourceDigest() != engines[input.generation].Snapshot().SourceDigest() {
									b.Error("execution mixed complete versions")
									return
								}
								completed[worker]++
							}
						})
					}
					close(start)
					wg.Wait()
					b.StopTimer()
					fires, swaps := 0, 0
					for i := range workers {
						fires += completed[i]
						swaps += published[i]
					}
					wantSwaps := 0
					if every > 0 {
						wantSwaps = (b.N + every - 1) / every
					}
					if fires != b.N || swaps != wantSwaps || runtime.Snapshot().Revision() != rulite.SnapshotRevision(swaps+1) {
						b.Fatal("operation counts changed")
					}
					benchmarkCounter, benchmarkSnapshot = fires, runtime.Snapshot()
					b.ReportMetric(float64(swaps)/float64(fires), "publish/fire")
					b.ReportMetric(float64(fires)*float64(size)/b.Elapsed().Seconds(), "evaluated/s")
				})
			}
		}
	}
}

func BenchmarkRuntimePublish(b *testing.B) {
	engines := []*rulite.Engine[runtimeInput]{runtimeEngine(b, 0, nil), runtimeEngine(b, 1, nil)}
	runtime, err := rulite.NewRuntime(engines[0])
	if err != nil {
		b.Fatal(err)
	}
	revision := rulite.SnapshotRevision(1)
	var info rulite.SnapshotInfo
	b.ReportAllocs()
	for b.Loop() {
		engine := engines[int(revision)%2]
		info, err = runtime.Publish(engine)
		revision++
		if err != nil || info.Revision() != revision || info.Version() != engine.Snapshot().Version() || info.SourceDigest() != engine.Snapshot().SourceDigest() {
			b.Fatal("publication sequence changed")
		}
	}
	benchmarkSnapshot = info
	input := runtimeInput{}
	result, err := runtime.Fire(context.Background(), &input)
	if err != nil {
		b.Fatal(err)
	}
	checkRuntimeResult(b, result, input, info)
}

func BenchmarkRuntimeConcurrentSwap(b *testing.B) {
	engines := []*rulite.Engine[runtimeInput]{runtimeEngine(b, 0, nil), runtimeEngine(b, 1, nil)}
	runtime, err := rulite.NewRuntime(engines[0])
	if err != nil {
		b.Fatal(err)
	}
	var fires, publications atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		local := uint64(0)
		for pb.Next() {
			if local%32 == 0 {
				if _, err := runtime.Publish(engines[(local/32)%2]); err != nil {
					b.Error(err)
					return
				}
				publications.Add(1)
			}
			input := runtimeInput{}
			result, err := runtime.Fire(context.Background(), &input)
			info := result.Snapshot()
			if err != nil || input.Calls != 2 || input.Stage != 2 || input.AppliedBy != "pricing/offer" || result.Counts() != (rulite.Counts{Total: 2, Evaluated: 2, Matched: 2, Fired: 2}) || result.StopReason() != rulite.StopCompleted || info.Revision() == 0 || input.Generation < 0 || input.Generation > 1 || info.Version() != engines[input.Generation].Snapshot().Version() || info.SourceDigest() != engines[input.Generation].Snapshot().SourceDigest() {
				b.Error("concurrent snapshot mixed")
				return
			}
			local++
		}
		fires.Add(local)
	})
	b.StopTimer()
	if fires.Load() != uint64(b.N) || runtime.Snapshot().Revision() != rulite.SnapshotRevision(publications.Load()+1) {
		b.Fatal("concurrent operation counts changed")
	}
	benchmarkCounter, benchmarkSnapshot = int(fires.Load()), runtime.Snapshot()
}
