package cel

import (
	"context"
	"reflect"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	maxInputBytes = 65536
	maxListItems  = 4096
)

type inputBudget struct {
	bytes, items, nodes int
	ctx                 context.Context
}

func (b *inputBudget) visit(depth int) error {
	if depth > 32 || b.nodes <= 0 {
		return ErrInputLimit
	}
	b.nodes--
	if b.nodes%16 == 0 {
		return contextError(b.ctx)
	}
	return nil
}

func (b *inputBudget) takeBytes(n int) error {
	if n > b.bytes {
		return ErrInputLimit
	}
	b.bytes -= n
	return nil
}

func (b *inputBudget) takeItems(n int) error {
	if n > b.items {
		return ErrInputLimit
	}
	b.items -= n
	return nil
}

func (b *inputBudget) native(value reflect.Value, schema *schema, depth int) error {
	if err := b.visit(depth); err != nil {
		return err
	}
	if value.Type() == reflect.TypeFor[time.Time]() {
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer:
		if value.IsNil() {
			return nil
		}
		return b.native(value.Elem(), schema, depth+1)
	case reflect.String:
		return b.takeBytes(value.Len())
	case reflect.Struct:
		for _, index := range schema.natives[value.Type()].indexes {
			if err := b.native(value.Field(index), schema, depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice:
		if value.Type().Elem() == reflect.TypeFor[byte]() {
			return b.takeBytes(value.Len())
		}
		if err := b.takeItems(value.Len()); err != nil {
			return err
		}
		for i := 0; i < value.Len(); i++ {
			if err := b.native(value.Index(i), schema, depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		if err := b.takeItems(value.Len()); err != nil {
			return err
		}
		iter := value.MapRange()
		for iter.Next() {
			if err := b.native(iter.Key(), schema, depth+1); err != nil {
				return err
			}
			if err := b.native(iter.Value(), schema, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *inputBudget) message(message protoreflect.Message, depth int) error {
	if err := b.visit(depth); err != nil {
		return err
	}
	if err := b.takeBytes(len(message.GetUnknown())); err != nil {
		return err
	}
	var failure error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsList() {
			list := value.List()
			failure = b.takeItems(list.Len())
			for i := 0; failure == nil && i < list.Len(); i++ {
				failure = b.protoValue(field, list.Get(i), depth+1)
			}
		} else if field.IsMap() {
			mapping := value.Map()
			failure = b.takeItems(mapping.Len())
			if failure == nil {
				mapping.Range(func(key protoreflect.MapKey, value protoreflect.Value) bool {
					failure = b.protoValue(field.MapKey(), key.Value(), depth+1)
					if failure == nil {
						failure = b.protoValue(field.MapValue(), value, depth+1)
					}
					return failure == nil
				})
			}
		} else {
			failure = b.protoValue(field, value, depth+1)
		}
		return failure == nil
	})
	return failure
}

func (b *inputBudget) protoValue(field protoreflect.FieldDescriptor, value protoreflect.Value, depth int) error {
	if field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind {
		return b.message(value.Message(), depth)
	}
	if err := b.visit(depth); err != nil {
		return err
	}
	switch field.Kind() {
	case protoreflect.StringKind:
		return b.takeBytes(len(value.String()))
	case protoreflect.BytesKind:
		return b.takeBytes(len(value.Bytes()))
	}
	return nil
}
