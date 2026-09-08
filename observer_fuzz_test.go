package rulite_test

import (
	"context"
	"errors"
	"testing"

	"github.com/imbrooklyn/rulite"
)

func FuzzObservationStream(f *testing.F) {
	f.Add([]byte{}, byte(0), byte(0), byte(0))
	f.Add([]byte{0, 2, 3, 1, 1}, byte(11), byte(7), byte(3))
	f.Add([]byte{1, 4}, byte(2), byte(4), byte(2))
	f.Add([]byte{0, 5}, byte(4), byte(6), byte(1))
	f.Add([]byte{2}, byte(0), byte(1), byte(1))
	f.Add([]byte{3}, byte(0), byte(3), byte(2))
	f.Add([]byte{4}, byte(0), byte(2), byte(5))
	f.Add([]byte{1}, byte(0), byte(2), byte(2))
	f.Fuzz(func(t *testing.T, outcomes []byte, modes, eventIndex, behavior byte) {
		// Bound rule count, event count, and panic stack collection per execution.
		if len(outcomes) > 32 {
			outcomes = outcomes[:32]
		}
		policy := rulite.DefaultPolicy().WithStop(rulite.StopMode(modes % 3)).WithConditionErrors(rulite.ErrorMode(modes / 3 % 2)).WithActionErrors(rulite.ErrorMode(modes / 6 % 2))
		baseline, _ := mustEngine(t, observationRules(outcomes, errors.New("business unavailable"))...).Fire(context.Background(), &executionInput{}, rulite.WithPolicy(policy))
		failAt := int(eventIndex) % len(expectedEvents(baseline))
		if behavior%3 == 0 {
			failAt = -1
		}
		// The oracle uses the business ledger without observation. It verifies
		// exact event prefixes, cause identity/order, policy, effects, and restart.
		checkObservationStream(t, outcomes, policy, failAt, behavior%3 == 2, behavior&4 != 0)
	})
}
