package dynamic

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestJSONBoundaryMechanisms(t *testing.T) {
	for depth := 1; depth <= 17; depth++ {
		raw := []byte(strings.Repeat(`{"x":`, depth) + `0` + strings.Repeat("}", depth))
		err := checkJSON(raw, '{', maxParamsBytes, 16, 4096)
		if (err == nil) != (depth <= 16) || depth > 16 && !errors.Is(err, ErrLimit) {
			t.Fatalf("incorrect depth bound at %d: %v", depth, err)
		}
	}
	for _, size := range []int{maxParamsBytes - 1, maxParamsBytes, maxParamsBytes + 1} {
		raw := []byte(`{"x":"` + strings.Repeat("x", size-8) + `"}`)
		if len(raw) != size {
			t.Fatal("invalid byte fixture")
		}
		if err := checkJSON(raw, '{', maxParamsBytes, 16, 4096); (err == nil) != (size <= maxParamsBytes) {
			t.Fatal("byte boundary changed")
		}
	}
	for _, limit := range []int{3, 4, 5} {
		if err := checkJSON([]byte(`{"x":1}`), '{', 100, 16, limit); (err == nil) != (limit >= 4) {
			t.Fatal("token boundary changed")
		}
	}
}

func TestParameterSchemaConflictsAndLimits(t *testing.T) {
	integer := reflect.TypeFor[int]()
	conflict := reflect.StructOf([]reflect.StructField{
		{Name: "First", Type: integer, Tag: `json:"same"`},
		{Name: "Second", Type: integer, Tag: `json:"same"`},
	})
	if err := validateParamType(conflict, 0, map[reflect.Type]bool{}, map[reflect.Type]bool{}); err == nil {
		t.Fatal("mapping conflict accepted")
	}
	for _, tp := range []reflect.Type{reflect.TypeFor[struct{ Value [17000]byte }](), reflect.PointerTo(integer)} {
		if err := validateParamType(tp, 17, map[reflect.Type]bool{}, map[reflect.Type]bool{}); !errors.Is(err, ErrLimit) {
			t.Fatal("schema bound failed")
		}
	}
	for _, name := range []string{"", "1field", "field-name", strings.Repeat("x", 65)} {
		if paramFieldName(name) {
			t.Fatal("invalid field name accepted")
		}
	}
}
