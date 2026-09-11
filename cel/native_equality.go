package cel

import (
	"reflect"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
)

type nativePointerPair struct {
	left, right uintptr
	typeOf      reflect.Type
}

type nativeEquality struct {
	budget  nativeBudget
	schema  *schema
	adapter types.Adapter
	equaled map[nativePointerPair]bool
}

// Share one budget and completed-pair cache through fields, lists, and maps.
// Pointer identity alone is not equality: a shared object may contain NaN.
// Cycles cannot populate the cache until compared and hit the depth bound.
func (e *nativeEquality) equal(left, right reflect.Value, depth int, field bool) ref.Val {
	if err := e.budget.visit(depth); err != nil {
		return types.WrapErr(err)
	}
	switch left.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map:
		if (field || left.Kind() == reflect.Pointer) && left.IsNil() != right.IsNil() {
			return types.False
		}
		if left.IsNil() && right.IsNil() {
			return types.True
		}
	}
	switch left.Kind() {
	case reflect.Pointer:
		pair := nativePointerPair{left.Pointer(), right.Pointer(), left.Type()}
		if e.equaled[pair] {
			return types.True
		}
		result := e.equal(left.Elem(), right.Elem(), depth+1, false)
		if result == types.True {
			if e.equaled == nil {
				e.equaled = make(map[nativePointerPair]bool)
			}
			e.equaled[pair] = true
		}
		return result
	case reflect.Struct:
		if left.Type() != reflect.TypeFor[time.Time]() {
			for _, index := range e.schema.natives[left.Type()].indexes {
				if result := e.equal(left.Field(index), right.Field(index), depth+1, true); result != types.True {
					return result
				}
			}
			return types.True
		}
	case reflect.String:
		if left.Len() != right.Len() {
			return types.False
		}
		if err := e.budget.takeBytes(left.Len()); err != nil {
			return types.WrapErr(err)
		}
	case reflect.Slice:
		if left.Len() != right.Len() {
			return types.False
		}
		if left.Type().Elem() == reflect.TypeFor[byte]() {
			if err := e.budget.takeBytes(left.Len()); err != nil {
				return types.WrapErr(err)
			}
			break
		}
		if err := e.budget.takeItems(int64(left.Len())); err != nil {
			return types.WrapErr(err)
		}
		for i := 0; i < left.Len(); i++ {
			if result := e.equal(left.Index(i), right.Index(i), depth+1, false); result != types.True {
				return result
			}
		}
		return types.True
	case reflect.Map:
		if left.Len() != right.Len() {
			return types.False
		}
		if err := e.budget.takeItems(int64(left.Len())); err != nil {
			return types.WrapErr(err)
		}
		iter := left.MapRange()
		for iter.Next() {
			key := iter.Key()
			value := right.MapIndex(key)
			if !value.IsValid() {
				return types.False
			}
			// Account for key visits and string comparison without allocating a
			// second CEL map or bypassing native integer key widths.
			if result := e.equal(key, key, depth+1, false); result != types.True {
				return result
			}
			if result := e.equal(iter.Value(), value, depth+1, false); result != types.True {
				return result
			}
		}
		return types.True
	}
	return e.adapter.NativeToValue(left.Interface()).Equal(e.adapter.NativeToValue(right.Interface()))
}
