package cel_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imbrooklyn/rulite/cel"
)

type nativeGraph struct {
	A, B, C *nativeGraph
	Value   float64
	List    []*nativeGraph
	Mapping map[string]*nativeGraph
}

type nativeGraphPair struct{ Left, Right nativeGraph }

func sharedGraph(depth int, containers bool, tail string) string {
	constructor := "cel_test.nativeGraph{A: n, B: n, C: n}"
	if containers {
		constructor = "cel_test.nativeGraph{List: [n, n], Mapping: {'n': n}}"
	}
	return "optional.of(input.Left)" + strings.Repeat(".optMap(n, "+constructor+")", depth) +
		".optMap(n, " + tail + ").orValue(false)"
}

func TestNativeEqualitySharedGraphs(t *testing.T) {
	c := compiler[nativeGraphPair](t)
	for _, containers := range []bool{false, true} {
		// Fourteen shared layers would revisit millions of fields without a
		// completed-pair cache. Collection edges must share that same cache.
		depth := 14
		if containers {
			depth = 10 // A collection edge adds another level of traversal.
		}
		checkTrue(t, c, &nativeGraphPair{}, sharedGraph(depth, containers, "n == n"))
		checkTrue(t, c, &nativeGraphPair{Left: nativeGraph{Value: math.NaN()}}, sharedGraph(depth, containers, "n != n"))
	}
	// Compare distinct graphs too: caching must identify both pointers.
	source := "optional.of([input.Left, input.Right])" + strings.Repeat(
		".optMap(p, [cel_test.nativeGraph{A: p[0], B: p[0], C: p[0]}, cel_test.nativeGraph{A: p[1], B: p[1], C: p[1]}])", 10) +
		".optMap(p, p[0] == p[1]).orValue(false)"
	f := condition(t, c, source)
	for _, tc := range []struct {
		left, right float64
		want        bool
	}{{1, 1, true}, {1, 2, false}, {math.NaN(), math.NaN(), false}} {
		input := nativeGraphPair{Left: nativeGraph{Value: tc.left}, Right: nativeGraph{Value: tc.right}}
		if got, err := f(context.Background(), &input); got != tc.want || err != nil {
			t.Fatalf("distinct graphs: got=%t, want=%t, error=%v: %v", got, tc.want, err, errors.Unwrap(err))
		}
	}
}

func TestNativeEqualityLimitsCannotBecomeMatches(t *testing.T) {
	c := compiler[nativeGraphPair](t)
	for _, tail := range []string{
		"n == n", "n != n", "[n] == [n]", "{'n': n} == {'n': n}",
		"n in [n]", "!(n in [n])", "(n == n) || true",
	} {
		matched, err := condition(t, c, sharedGraph(30, false, tail))(context.Background(), &nativeGraphPair{})
		var runtimeErr *cel.RuntimeError
		if matched || !errors.As(err, &runtimeErr) || !errors.Is(err, cel.ErrNativeLimit) {
			t.Fatalf("limit suppressed by %s: matched=%t, error=%v: %v", tail, matched, err, errors.Unwrap(err))
		}
	}
}

func TestNativeEqualityPresenceAndScalars(t *testing.T) {
	type equalValue struct {
		Lists   [][]int64
		Maps    []map[string]int64
		Bytes   [][]byte
		ByKey   map[string][]int64
		Pointer *float64
		At      time.Time
		Hidden  string `json:"-"`
	}
	type equalPair struct{ Left, Right equalValue }
	left := equalValue{Lists: [][]int64{nil}, Maps: []map[string]int64{nil}, Bytes: [][]byte{nil},
		ByKey: map[string][]int64{"n": nil}, At: time.Unix(0, 0).UTC(), Hidden: "left"}
	right := equalValue{Lists: [][]int64{{}}, Maps: []map[string]int64{{}}, Bytes: [][]byte{{}},
		ByKey: map[string][]int64{"n": {}}, At: time.Unix(0, 0).In(time.FixedZone("offset", 3600)), Hidden: "right"}
	input := equalPair{left, right}
	c := compiler[equalPair](t, cel.WithJSONFieldNames())
	checkTrue(t, c, &input, "input.Left == input.Right")
	zero := 0.0
	input.Right.Pointer = &zero
	checkTrue(t, c, &input, "input.Left != input.Right")
	nan := math.NaN()
	input.Left.Pointer, input.Right.Pointer = &nan, &nan
	checkTrue(t, c, &input, "input.Left != input.Right")
	input.Left.Pointer, input.Right.Pointer = nil, nil
	input.Left.Lists, input.Right.Lists = nil, [][]int64{}
	checkTrue(t, c, &input, "input.Left != input.Right")
}

func TestNativeOperationsConcurrent(t *testing.T) {
	c := compiler[nativeGraphPair](t)
	f := condition(t, c, sharedGraph(10, true, "n == n"))
	limited := condition(t, c, sharedGraph(30, false, "n == n"))
	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			input := nativeGraphPair{Left: nativeGraph{Value: float64(worker)}}
			for range 10 {
				if got, err := f(context.Background(), &input); !got || err != nil {
					t.Errorf("shared program: got=%t, error=%v", got, err)
					return
				}
				if got, err := limited(context.Background(), &input); got || !errors.Is(err, cel.ErrNativeLimit) {
					t.Errorf("shared program limit: got=%t, error=%v", got, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func FuzzNativeEquality(f *testing.F) {
	for _, depth := range []uint8{0, 1, 10, 14, 30} {
		for _, containers := range []bool{false, true} {
			f.Add(depth, containers, false)
			f.Add(depth, containers, true)
		}
	}
	c := compiler[nativeGraphPair](f)
	f.Fuzz(func(t *testing.T, depth uint8, containers, nan bool) {
		depth %= 31
		input := nativeGraphPair{}
		if nan {
			input.Left.Value = math.NaN()
		}
		got, err := condition(t, c, sharedGraph(int(depth), containers, "n == n"))(context.Background(), &input)
		if errors.Is(err, cel.ErrNativeLimit) {
			if got || depth <= 10 || !containers && depth <= 14 {
				t.Fatalf("unexpected native limit: got=%t, depth=%d, containers=%t", got, depth, containers)
			}
			return
		}
		if err != nil || got == nan {
			t.Fatalf("shared graph equality: got=%t, NaN=%t, error=%v: %v", got, nan, err, errors.Unwrap(err))
		}
	})
}
