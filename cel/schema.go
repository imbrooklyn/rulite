package cel

import (
	"errors"
	"fmt"
	"path"
	"reflect"
	"strings"
	"time"

	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type schema struct {
	jsonNames  bool
	natives    map[reflect.Type]*nativeType
	ordered    []*nativeType
	names      map[string]reflect.Type
	files      []protoreflect.FileDescriptor
	protoNames map[string]bool
}

func newSchema(jsonNames bool) *schema {
	return &schema{jsonNames: jsonNames, natives: make(map[reflect.Type]*nativeType), names: make(map[string]reflect.Type), protoNames: make(map[string]bool)}
}

func scalarType(tp reflect.Type) *types.Type {
	if tp == reflect.TypeFor[time.Time]() {
		return types.TimestampType
	}
	if tp == reflect.TypeFor[time.Duration]() {
		return types.DurationType
	}
	switch tp.Kind() {
	case reflect.Bool:
		return types.BoolType
	case reflect.String:
		return types.StringType
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return types.IntType
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return types.UintType
	case reflect.Float32, reflect.Float64:
		return types.DoubleType
	}
	return nil
}

func (s *schema) fieldName(field reflect.StructField) string {
	if s.jsonNames {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name != "" {
			return name
		}
	}
	return field.Name
}

func (s *schema) native(tp reflect.Type, depth int) (*types.Type, error) {
	if depth > 32 {
		return nil, errors.New("native schema nesting exceeds 32")
	}
	if tp.Implements(reflect.TypeFor[ref.Val]()) || reflect.PointerTo(tp).Implements(reflect.TypeFor[ref.Val]()) {
		return nil, errors.New("custom CEL values require an explicit native projection")
	}
	if tp.Implements(reflect.TypeFor[proto.Message]()) || reflect.PointerTo(tp).Implements(reflect.TypeFor[proto.Message]()) {
		return nil, errors.New("protobuf messages require BindProto")
	}
	if scalar := scalarType(tp); scalar != nil {
		return scalar, nil
	}
	switch tp.Kind() {
	case reflect.Pointer:
		if tp.Elem().Kind() == reflect.Pointer {
			return nil, errors.New("multiple pointer indirections require a projection")
		}
		if tp.Elem().Kind() != reflect.Struct && scalarType(tp.Elem()) == nil {
			return nil, errors.New("pointers to collections require a projection")
		}
		return s.native(tp.Elem(), depth+1)
	case reflect.Slice:
		if tp.Elem() == reflect.TypeFor[byte]() {
			return types.BytesType, nil
		}
		elem, err := s.native(tp.Elem(), depth+1)
		if err != nil {
			return nil, err
		}
		return types.NewListType(elem), nil
	case reflect.Map:
		key := scalarType(tp.Key())
		if key != types.StringType && key != types.IntType && key != types.UintType && key != types.BoolType {
			return nil, errors.New("map keys must be string, integer, or bool")
		}
		if _, err := s.native(tp.Key(), depth+1); err != nil {
			return nil, err
		}
		elem, err := s.native(tp.Elem(), depth+1)
		if err != nil {
			return nil, err
		}
		return types.NewMapType(key, elem), nil
	case reflect.Struct:
		if tp.Name() == "" {
			return nil, errors.New("native structs must be named")
		}
		if nt, ok := s.natives[tp]; ok {
			return types.NewObjectType(nt.TypeName()), nil
		}
		if len(s.natives) >= 256 || tp.NumField() > 128 {
			return nil, errors.New("native schema exceeds type or field limit")
		}
		name := path.Base(tp.PkgPath()) + "." + tp.Name()
		if previous, ok := s.names[name]; ok && previous != tp || s.protoNames[name] {
			return nil, errors.New("CEL type name has conflicting mappings")
		}
		nt, err := types.NewNativeType(tp, types.ParseStructField(s.fieldName))
		if err != nil {
			return nil, err
		}
		descriptor := &nativeType{NativeType: nt, fields: make(map[string]int)}
		s.natives[tp], s.names[name] = descriptor, tp
		s.ordered = append(s.ordered, descriptor)
		for i := 0; i < tp.NumField(); i++ {
			field := tp.Field(i)
			if field.Anonymous {
				return nil, errors.New("embedded fields require an explicit projection")
			}
			if !field.IsExported() {
				continue
			}
			fieldName := s.fieldName(field)
			if fieldName == "-" {
				continue
			}
			if !variableName.MatchString(fieldName) {
				return nil, errors.New("native field names must be ASCII identifiers of 1-64 bytes")
			}
			if _, ok := descriptor.fields[fieldName]; ok {
				return nil, errors.New("duplicate native field mapping")
			}
			if _, err := s.native(field.Type, depth+1); err != nil {
				return nil, fmt.Errorf("unsupported native field %s: %w", field.Name, err)
			}
			descriptor.fields[fieldName] = i
			descriptor.indexes = append(descriptor.indexes, i)
		}
		return types.NewObjectType(name), nil
	}
	return nil, errors.New("unsupported native type; use an explicit projection")
}

func (s *schema) addProto(descriptor protoreflect.MessageDescriptor) (*types.Type, error) {
	if err := s.addFile(descriptor.ParentFile()); err != nil {
		return nil, err
	}
	return types.NewObjectType(string(descriptor.FullName())), nil
}

func (s *schema) addFile(file protoreflect.FileDescriptor) error {
	for _, previous := range s.files {
		if previous.Path() == file.Path() {
			if previous != file {
				return errors.New("conflicting protobuf file descriptors")
			}
			return nil
		}
	}
	if len(s.files) >= 256 {
		return errors.New("protobuf schema exceeds 256 files")
	}
	s.files = append(s.files, file)
	var messages func(protoreflect.MessageDescriptors) error
	messages = func(list protoreflect.MessageDescriptors) error {
		for i := 0; i < list.Len(); i++ {
			message := list.Get(i)
			name := string(message.FullName())
			switch name {
			case "google.protobuf.Any", "google.protobuf.Struct", "google.protobuf.Value", "google.protobuf.ListValue":
				return errors.New("dynamic protobuf payloads require an explicit typed projection")
			}
			if s.protoNames[name] || s.names[name] != nil {
				return errors.New("CEL type name has conflicting mappings")
			}
			s.protoNames[name] = true
			if err := messages(message.Messages()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := messages(file.Messages()); err != nil {
		return err
	}
	for i := 0; i < file.Imports().Len(); i++ {
		if err := s.addFile(file.Imports().Get(i).FileDescriptor); err != nil {
			return err
		}
	}
	return nil
}

func (s *schema) registry() (*types.Registry, error) {
	registry, err := types.NewRegistry()
	if err != nil {
		return nil, err
	}
	for _, file := range s.files {
		if err := registry.RegisterDescriptor(file); err != nil {
			return nil, err
		}
	}
	for _, native := range s.ordered {
		if err := registry.RegisterType(native); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// Reuse CEL native types while keeping zero timestamps intact in optimized
// attribute access and dynamic object, list, and map access.
type nativeType struct {
	*types.NativeType
	fields  map[string]int
	indexes []int
}

// FindFieldType preserves the native checked type and exact field presence.
func (t *nativeType) FindFieldType(name string) (*types.FieldType, bool) {
	index, ok := t.fields[name]
	if !ok {
		return nil, false
	}
	field, ok := t.NativeType.FindFieldType(name)
	if !ok {
		return nil, false
	}
	field.IsSet = func(input any) bool { return !nativeField(input, index).IsZero() }
	field.GetFrom = func(input any) (any, error) { return nativeFieldValue(nativeField(input, index)), nil }
	return field, true
}

func nativeField(input any, index int) reflect.Value {
	if value, ok := input.(ref.Val); ok {
		input = value.Value()
	}
	v := reflect.ValueOf(input)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v = reflect.Zero(v.Type().Elem())
		} else {
			v = v.Elem()
		}
	}
	return v.Field(index)
}

func nativeFieldValue(value reflect.Value) any {
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return reflect.Zero(value.Type().Elem()).Interface()
	}
	return value.Interface()
}

// Adapt uses caller-owned native storage without constructing a field map.
func (t *nativeType) Adapt(adapter types.Adapter, value any) ref.Val {
	base := t.NativeType.Adapt(adapter, value)
	if base == types.NullValue {
		return base
	}
	return &nativeValue{Val: base, descriptor: t, adapter: adapter}
}

type nativeValue struct {
	ref.Val
	descriptor *nativeType
	adapter    types.Adapter
}

type nativeAdapter struct {
	registry *types.Registry
	schema   *schema
}

type nativeProvider struct {
	*types.Registry
	adapter *nativeAdapter
}

// NewValue keeps native literal construction on the same field adaptation path.
func (p *nativeProvider) NewValue(name string, fields map[string]ref.Val) ref.Val {
	if tp := p.adapter.schema.names[name]; tp != nil {
		return p.adapter.schema.natives[tp].NewValue(p.adapter, fields)
	}
	return p.Registry.NewValue(name, fields)
}

// NativeToValue keeps native pointer scalars and nested containers typed.
func (a *nativeAdapter) NativeToValue(value any) ref.Val {
	if v, ok := value.(ref.Val); ok {
		return v
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return types.NullValue
	}
	tp := v.Type()
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return types.NullValue
		}
		tp = tp.Elem()
		if scalarType(tp) != nil {
			return a.registry.NativeToValue(v.Elem().Interface())
		}
	}
	if descriptor := a.schema.natives[tp]; descriptor != nil {
		return descriptor.Adapt(a, value)
	}
	switch v.Kind() {
	case reflect.Slice:
		if v.Type().Elem() != reflect.TypeFor[byte]() {
			return types.NewDynamicList(a, value)
		}
	case reflect.Map:
		mapping := types.NewDynamicMap(a, value)
		if kind := v.Type().Key().Kind(); kind == reflect.Int || kind == reflect.Uint {
			return &nativeIntegerMap{Mapper: mapping, adapter: a, value: v}
		}
		return mapping
	}
	return a.registry.NativeToValue(value)
}

// Get reads the exact mapped field and retains Go's zero timestamp.
func (v *nativeValue) Get(field ref.Val) ref.Val {
	name, ok := field.(types.String)
	if !ok {
		return types.NewErr("native field name must be a string")
	}
	index, ok := v.descriptor.fields[string(name)]
	if !ok {
		return types.NewErr("unknown native field")
	}
	return v.adapter.NativeToValue(nativeFieldValue(nativeField(v.Value(), index)))
}

// IsSet reports Go zero-value presence, including nil versus non-nil pointers.
func (v *nativeValue) IsSet(field ref.Val) ref.Val {
	name, ok := field.(types.String)
	if !ok {
		return types.NewErr("native field name must be a string")
	}
	index, ok := v.descriptor.fields[string(name)]
	if !ok {
		return types.NewErr("unknown native field")
	}
	return types.Bool(!nativeField(v.Value(), index).IsZero())
}

// Equal compares exposed fields only, retaining nil collection and pointer
// presence. Hidden fields cannot influence CEL equality or its traversal cost.
func (v *nativeValue) Equal(other ref.Val) ref.Val {
	wrapped, ok := other.(*nativeValue)
	if !ok || v.descriptor.ReflectType() != wrapped.descriptor.ReflectType() {
		return types.False
	}
	for _, index := range v.descriptor.indexes {
		left, right := nativeField(v.Value(), index), nativeField(wrapped.Value(), index)
		switch left.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			if left.IsNil() != right.IsNil() {
				return types.False
			}
			if left.IsNil() {
				continue
			}
		}
		equal := v.adapter.NativeToValue(nativeFieldValue(left)).Equal(v.adapter.NativeToValue(nativeFieldValue(right)))
		if equal != types.True {
			return equal
		}
	}
	return types.True
}

// ConvertToType keeps field adaptation intact across dynamic casts.
func (v *nativeValue) ConvertToType(tp ref.Type) ref.Val {
	if tp.TypeName() == v.Type().TypeName() {
		return v
	}
	return v.Val.ConvertToType(tp)
}
