package dynamic_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/imbrooklyn/rulite"
	"github.com/imbrooklyn/rulite/dynamic"
)

func TestRuntimeRejectsIncompleteDynamicReloads(t *testing.T) {
	c, registry := compiler(t), registry(t)
	initial, err := rulite.NewEngine[price]()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := rulite.NewRuntime(initial)
	if err != nil {
		t.Fatal(err)
	}
	reload := func(source []byte) error {
		set, err := dynamic.CompileJSON(source, c, registry)
		if err != nil {
			return err
		}
		set, err = set.WithIdentity("pricing/v1", "")
		if err != nil {
			return err
		}
		engine, err := rulite.NewEngineFromRuleSet(set)
		if err != nil {
			return err
		}
		_, err = runtime.Publish(engine)
		return err
	}
	valid := []byte(`[{"id":"discount","when":"true","action":"pricing.apply/v1","params":{"rate":3}}]`)
	if err := reload(valid); err != nil {
		t.Fatal(err)
	}
	identity := runtime.Snapshot()
	invalid := [][]byte{
		[]byte(`[{`),
		[]byte(`[{"id":"discount","when":"input.Unknown","action":"pricing.apply/v1","params":{}}]`),
		[]byte(`[{"id":"discount","when":"true","action":"missing.apply/v1","params":{}}]`),
		[]byte(`[{"id":"discount","when":"true","action":"pricing.apply/v1","params":{"unknown":1}}]`),
		[]byte(`[{"id":"discount","when":"true","action":"pricing.apply/v1","params":{"rate":101}}]`),
	}
	for _, workers := range []int{1, 2, 4, 8, 16, 32} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			start := make(chan struct{})
			var wg sync.WaitGroup
			for range 3 {
				wg.Go(func() {
					<-start
					for range 8 {
						for _, source := range invalid {
							if err := reload(source); err == nil {
								t.Error("invalid reload accepted")
							}
						}
					}
				})
			}
			for range workers {
				wg.Go(func() {
					<-start
					for range 30 {
						input := price{}
						result, err := runtime.Fire(context.Background(), &input, rulite.WithTrace(), rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error {
							if event.Snapshot() != identity {
								t.Error("invalid reload changed event identity")
							}
							return nil
						})))
						if err != nil || result.Snapshot() != identity || result.Counts().Fired != 1 || input.Discount != 3 {
							t.Error("failed reload changed available rules", err, input)
						}
					}
				})
			}
			close(start)
			wg.Wait()
		})
	}
	if runtime.Snapshot() != identity {
		t.Fatal("compilation failure consumed revision")
	}
}
