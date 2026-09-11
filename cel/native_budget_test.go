package cel

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

type unmaterializedList struct {
	traits.Lister
	size types.Int
}

func (l unmaterializedList) Size() ref.Val { return l.size }
func (unmaterializedList) Value() any      { panic("collection Value must not be called") }

type unmaterializedMap struct {
	traits.Mapper
	size types.Int
}

func (m unmaterializedMap) Size() ref.Val { return m.size }
func (unmaterializedMap) Value() any      { panic("collection Value must not be called") }

func TestNativeCollectionBoundsBeforeMaterialization(t *testing.T) {
	for _, size := range []types.Int{-1, 4097, 1 << 30, math.MaxInt64} {
		for _, tc := range []struct {
			value  ref.Val
			target reflect.Type
		}{
			{unmaterializedList{size: size}, reflect.TypeFor[[]int64]()},
			{unmaterializedMap{size: size}, reflect.TypeFor[map[string]int64]()},
			{types.NewRefValList(types.DefaultTypeAdapter, []ref.Val{unmaterializedList{size: size}}), reflect.TypeFor[[][]int64]()},
		} {
			if _, err := convertToNative(tc.value, tc.target); !errors.Is(err, ErrNativeLimit) {
				t.Fatalf("size %d was not rejected before allocation: %v", size, err)
			}
		}
	}
}
