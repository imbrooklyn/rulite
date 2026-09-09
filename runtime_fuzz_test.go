package rulite_test

import (
	"context"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func FuzzRuntimePublications(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 255})
	f.Add([]byte{1, 1, 1, 2, 2, 0, 3, 3})
	f.Fuzz(func(t *testing.T, operations []byte) {
		// At most 128 operations over four complete snapshots; no recursive input.
		if len(operations) > 128 {
			operations = operations[:128]
		}
		engines := make([]*rulite.Engine[runtimeInput], 4)
		for i := range engines {
			engines[i] = runtimeEngine(t, i, nil)
		}
		runtime, err := rulite.NewRuntime(engines[0])
		if err != nil {
			t.Fatal(err)
		}
		generation, revision := 0, rulite.SnapshotRevision(1)
		for _, operation := range operations {
			before := runtime.Snapshot()
			switch operation % 3 {
			case 0:
				generation = int(operation/3) % len(engines)
				info, err := runtime.Publish(engines[generation])
				revision++
				if err != nil || info.Revision() != revision {
					t.Fatal("successful publication sequence changed")
				}
			case 1:
				if _, err := runtime.Publish(&rulite.Engine[runtimeInput]{}); err != rulite.ErrInvalidEngine || runtime.Snapshot() != before {
					t.Fatal("failed publication replaced snapshot")
				}
			}
			input := runtimeInput{}
			var events []rulite.Event
			options := []rulite.FireOption{rulite.WithObserver(rulite.ObserverFunc(func(_ context.Context, event rulite.Event) error { events = append(events, event); return nil }))}
			if operation&128 != 0 {
				options = append(options, rulite.WithTrace())
			}
			result, err := runtime.Fire(context.Background(), &input, options...)
			if err != nil || input.Generation != generation || result.Snapshot().Revision() != revision {
				t.Fatal("execution differs from publication model")
			}
			checkRuntimeResult(t, result, input, runtime.Snapshot())
			for _, event := range events {
				if event.Snapshot() != result.Snapshot() {
					t.Fatal("mixed event identity")
				}
			}
		}
	})
}
