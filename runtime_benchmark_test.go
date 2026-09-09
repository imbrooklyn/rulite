package rulite_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/imbrooklyn/rulite"
)

var benchmarkSnapshot rulite.SnapshotInfo

func BenchmarkSnapshotFire(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
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
