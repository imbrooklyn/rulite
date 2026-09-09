package dynamic

import (
	"errors"
	"reflect"
	"strings"
	"time"
)

// Parameter schemas deliberately exclude decoder hooks and field promotion.
// Only the registered typed action and validator are trusted executable code.
func validateParamType(tp reflect.Type, depth int, active, seen map[reflect.Type]bool) error {
	if depth > 16 || tp.Size() > maxParamsBytes || len(seen) >= 128 && !seen[tp] {
		return ErrLimit
	}
	if active[tp] {
		return errors.New("recursive parameter schemas are not supported")
	}
	for _, candidate := range []reflect.Type{tp, reflect.PointerTo(tp)} {
		for _, name := range []string{"UnmarshalJSON", "UnmarshalJSONFrom", "UnmarshalText"} {
			if _, ok := candidate.MethodByName(name); ok {
				return errors.New("custom parameter decoding is not supported")
			}
		}
	}
	if tp == reflect.TypeFor[time.Duration]() {
		return errors.New("use integer units or a string for durations")
	}
	if seen[tp] {
		return nil
	}
	seen[tp], active[tp] = true, true
	defer delete(active, tp)
	switch tp.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return nil
	case reflect.Pointer, reflect.Slice:
		return validateParamType(tp.Elem(), depth+1, active, seen)
	case reflect.Map:
		if tp.Key().Kind() != reflect.String {
			return errors.New("parameter map keys must be strings")
		}
		if err := validateParamType(tp.Key(), depth+1, active, seen); err != nil {
			return err
		}
		return validateParamType(tp.Elem(), depth+1, active, seen)
	case reflect.Struct:
		if tp.NumField() > 64 {
			return ErrLimit
		}
		names := make(map[string]bool)
		for i := 0; i < tp.NumField(); i++ {
			field := tp.Field(i)
			if field.Anonymous {
				return errors.New("embedded parameter fields are not supported")
			}
			if field.PkgPath != "" || field.Tag.Get("json") == "-" {
				continue
			}
			parts := strings.Split(field.Tag.Get("json"), ",")
			name := parts[0]
			if name == "" {
				name = field.Name
			}
			if !paramFieldName(name) || names[name] {
				return errors.New("invalid or duplicate parameter field name")
			}
			names[name] = true
			for _, option := range parts[1:] {
				if option != "omitempty" && option != "omitzero" {
					return errors.New("unsupported parameter field option")
				}
			}
			if err := validateParamType(field.Type, depth+1, active, seen); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("unsupported parameter type")
	}
}

func paramFieldName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i, c := range []byte(name) {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}
