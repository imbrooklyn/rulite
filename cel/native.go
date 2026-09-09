package cel

import (
	"errors"
	"fmt"
	"reflect"
	"time"

	"cel.dev/cel-go/common/types/ref"
)

const (
	maxInputBytes = 65536
	maxListItems  = 4096
)

func nativeSchema(tp reflect.Type) ([]int, error) {
	if tp.Kind() != reflect.Struct || tp.Name() == "" || tp == reflect.TypeFor[time.Time]() {
		return nil, errors.New("input must be a named native struct")
	}
	if tp.Implements(reflect.TypeFor[ref.Val]()) || reflect.PointerTo(tp).Implements(reflect.TypeFor[ref.Val]()) {
		return nil, errors.New("custom CEL values are not supported")
	}
	if tp.NumField() > 128 {
		return nil, errors.New("input schema exceeds 128 fields")
	}
	var bounded []int
	for i := 0; i < tp.NumField(); i++ {
		field := tp.Field(i)
		if field.Anonymous {
			return nil, errors.New("embedded fields are not supported")
		}
		if !field.IsExported() {
			continue
		}
		ft := field.Type
		if ft.Kind() == reflect.Slice {
			// Named collections need a separate conversion contract.
			if ft.Name() != "" || !nativeScalar(ft.Elem()) {
				return nil, fmt.Errorf("unsupported native field %s", field.Name)
			}
		} else if !nativeScalar(ft) {
			return nil, fmt.Errorf("unsupported native field %s", field.Name)
		}
		if ft.Kind() == reflect.String || ft.Kind() == reflect.Slice {
			bounded = append(bounded, i)
		}
	}
	return bounded, nil
}

func nativeScalar(tp reflect.Type) bool {
	if tp == reflect.TypeFor[time.Duration]() || tp.Implements(reflect.TypeFor[ref.Val]()) {
		return false
	}
	switch tp.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// The schema indexes are frozen once. Evaluation inspects lengths only; native
// objects and collections stay caller-owned and are never projected into maps.
func withinInputLimits(input reflect.Value, fields []int) bool {
	bytesLeft, itemsLeft := maxInputBytes, maxListItems
	for _, index := range fields {
		field := input.Field(index)
		if field.Kind() == reflect.String || field.Type() == reflect.TypeFor[[]byte]() {
			if field.Len() > bytesLeft {
				return false
			}
			bytesLeft -= field.Len()
			continue
		}
		if field.Len() > itemsLeft {
			return false
		}
		itemsLeft -= field.Len()
		if field.Type().Elem().Kind() == reflect.String {
			for i := 0; i < field.Len(); i++ {
				n := field.Index(i).Len()
				if n > bytesLeft {
					return false
				}
				bytesLeft -= n
			}
		}
	}
	return true
}
