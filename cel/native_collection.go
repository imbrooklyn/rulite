package cel

import (
	"reflect"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"cel.dev/cel-go/common/types/traits"
)

// Only adapter-created wrappers expose native storage. CEL's general Value
// method is not a storage accessor: a lazy collection can allocate recursively.
type nativeStorage struct{ value reflect.Value }

func (s nativeStorage) storageValue() reflect.Value { return s.value }

func collectionStorage(value ref.Val) reflect.Value {
	if native, ok := value.(interface{ storageValue() reflect.Value }); ok {
		return native.storageValue()
	}
	return reflect.Value{}
}

type nativeList struct {
	traits.Lister
	nativeStorage
}

func (l *nativeList) ConvertToType(target ref.Type) ref.Val {
	if target == types.ListType {
		return l
	}
	return l.Lister.ConvertToType(target)
}

type nativeMap struct {
	traits.Mapper
	nativeStorage
}

func (m *nativeMap) ConvertToType(target ref.Type) ref.Val {
	if target == types.MapType {
		return m
	}
	return m.Mapper.ConvertToType(target)
}
