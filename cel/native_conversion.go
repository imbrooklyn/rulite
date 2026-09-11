package cel

import (
	"fmt"
	"reflect"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// convertToNative uses Go's platform width for int/uint, including defined types
// and nested literal fields. cel-go v0.32 treats these kinds as int32/uint32.
// Other scalar conversions retain CEL's checked conversion semantics.
func convertToNative(value ref.Val, target reflect.Type) (any, error) {
	switch target.Kind() {
	case reflect.Int:
		if integer, ok := value.(types.Int); ok {
			out := reflect.New(target).Elem()
			if out.OverflowInt(int64(integer)) {
				return nil, fmt.Errorf("integer overflow converting to %v", target)
			}
			out.SetInt(int64(integer))
			return out.Interface(), nil
		}
	case reflect.Uint:
		if integer, ok := value.(types.Uint); ok {
			out := reflect.New(target).Elem()
			if out.OverflowUint(uint64(integer)) {
				return nil, fmt.Errorf("unsigned integer overflow converting to %v", target)
			}
			out.SetUint(uint64(integer))
			return out.Interface(), nil
		}
	case reflect.Pointer:
		if value == types.NullValue {
			return reflect.Zero(target).Interface(), nil
		}
		// Convert to the exact scalar type before allocating its pointer.
		// cel-go's pointer conversions cover only a subset of native scalars.
		if scalarType(target.Elem()) != nil {
			elem, err := convertToNative(value, target.Elem())
			if err != nil {
				return nil, err
			}
			out := reflect.New(target.Elem())
			out.Elem().Set(reflect.ValueOf(elem))
			return out.Interface(), nil
		}
	case reflect.Slice:
		if list, ok := value.(traits.Lister); ok {
			// Preserve caller-owned storage and nil presence when already typed.
			if native := reflect.ValueOf(value.Value()); native.IsValid() && native.Type().AssignableTo(target) {
				return native.Interface(), nil
			}
			size := int(list.Size().(types.Int))
			out := reflect.MakeSlice(target, size, size)
			for i := 0; i < size; i++ {
				elem, err := convertToNative(list.Get(types.Int(i)), target.Elem())
				if err != nil {
					return nil, err
				}
				out.Index(i).Set(reflect.ValueOf(elem))
			}
			return out.Interface(), nil
		}
	case reflect.Map:
		if mapping, ok := value.(traits.Mapper); ok {
			return convertNativeMap(mapping, target)
		}
	}
	return value.ConvertToNative(target)
}

func convertNativeMap(mapping traits.Mapper, target reflect.Type) (any, error) {
	if native := reflect.ValueOf(mapping.Value()); native.IsValid() && native.Type().AssignableTo(target) {
		return native.Interface(), nil
	}
	out := reflect.MakeMapWithSize(target, int(mapping.Size().(types.Int)))
	iterator := mapping.Iterator()
	for iterator.HasNext() == types.True {
		key := iterator.Next()
		nativeKey, err := convertToNative(key, target.Key())
		if err != nil {
			return nil, err
		}
		nativeValue, err := convertToNative(mapping.Get(key), target.Elem())
		if err != nil {
			return nil, err
		}
		out.SetMapIndex(reflect.ValueOf(nativeKey), reflect.ValueOf(nativeValue))
	}
	return out.Interface(), nil
}

// NewValue applies the same checked conversion to every native literal field.
func (t *nativeType) NewValue(adapter types.Adapter, fields map[string]ref.Val) ref.Val {
	out := reflect.New(t.ReflectType())
	for name, value := range fields {
		index, ok := t.fields[name]
		if !ok {
			return types.NewErr("no such field: %s", name)
		}
		field := out.Elem().Field(index)
		native, err := convertToNative(value, field.Type())
		if err != nil {
			return types.WrapErr(err)
		}
		field.Set(reflect.ValueOf(native))
	}
	return adapter.NativeToValue(out.Interface())
}

// nativeIntegerMap keeps caller-owned storage and CEL iteration, but routes
// every lookup through platform-width conversion. Overriding only Find would
// leave the embedded map's Get, Contains and Equal using its original Find.
type nativeIntegerMap struct {
	traits.Mapper
	adapter types.Adapter
	value   reflect.Value
}

func (m *nativeIntegerMap) Find(key ref.Val) (ref.Val, bool) {
	switch key.(type) {
	case types.Int, types.Uint, types.Double:
		// CEL permits lossless numeric key equivalence across int/uint/double.
		// ConvertToType alone can truncate doubles; equality rejects that case.
		target := m.value.Type().Key()
		integer := key.ConvertToType(scalarType(target))
		if types.Equal(key, integer) != types.True {
			return nil, false
		}
		native, err := convertToNative(integer, target)
		if err != nil {
			return nil, false
		}
		value := m.value.MapIndex(reflect.ValueOf(native))
		if value.IsValid() {
			return m.adapter.NativeToValue(value.Interface()), true
		}
	default:
		if types.IsUnknownOrError(key) {
			return key, false
		}
	}
	return nil, false
}

func (m *nativeIntegerMap) Get(key ref.Val) ref.Val {
	value, found := m.Find(key)
	if !found {
		return types.ValOrErr(value, "no such key: %v", key)
	}
	return value
}

func (m *nativeIntegerMap) Contains(key ref.Val) ref.Val {
	_, found := m.Find(key)
	return types.Bool(found)
}

func (m *nativeIntegerMap) Equal(other ref.Val) ref.Val {
	mapping, ok := other.(traits.Mapper)
	if !ok || m.Size() != mapping.Size() {
		return types.False
	}
	iterator := m.Iterator()
	for iterator.HasNext() == types.True {
		key := iterator.Next()
		value, found := mapping.Find(key)
		if !found {
			return types.False
		}
		if equal := types.Equal(m.Get(key), value); equal != types.True {
			return equal
		}
	}
	return types.True
}

func (m *nativeIntegerMap) ConvertToType(target ref.Type) ref.Val {
	if target == types.MapType {
		return m
	}
	return m.Mapper.ConvertToType(target)
}

func (m *nativeIntegerMap) ConvertToNative(target reflect.Type) (any, error) {
	if target.Kind() == reflect.Map {
		return convertNativeMap(m, target)
	}
	return m.Mapper.ConvertToNative(target)
}
