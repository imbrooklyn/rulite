package cel_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/imbrooklyn/rulite/cel"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

func messageDescriptor(t testing.TB) protoreflect.MessageDescriptor {
	t.Helper()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("pricing.proto"), Package: proto.String("pricing"), Syntax: proto.String("proto3"),
		EnumType: []*descriptorpb.EnumDescriptorProto{{Name: proto.String("Status"), Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: proto.String("UNKNOWN"), Number: proto.Int32(0)}, {Name: proto.String("ACTIVE"), Number: proto.Int32(1)},
		}}},
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("Order"),
			OneofDecl: []*descriptorpb.OneofDescriptorProto{{Name: proto.String("contact")}, {Name: proto.String("_coupon")}},
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("total_amount"), JsonName: proto.String("totalAmount"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum()},
				{Name: proto.String("status"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(), TypeName: proto.String(".pricing.Status")},
				{Name: proto.String("email"), Number: proto.Int32(3), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), OneofIndex: proto.Int32(0)},
				{Name: proto.String("phone"), Number: proto.Int32(4), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), OneofIndex: proto.Int32(0)},
				{Name: proto.String("coupon"), Number: proto.Int32(5), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), OneofIndex: proto.Int32(1), Proto3Optional: proto.Bool(true)},
				{Name: proto.String("scores"), Number: proto.Int32(6), Type: descriptorpb.FieldDescriptorProto_TYPE_INT64.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()},
				{Name: proto.String("child"), Number: proto.Int32(7), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".pricing.Order")},
				{Name: proto.String("labels"), Number: proto.Int32(8), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: proto.String(".pricing.Order.LabelsEntry"), Label: descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()},
			},
			NestedType: []*descriptorpb.DescriptorProto{{Name: proto.String("LabelsEntry"), Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)}, Field: []*descriptorpb.FieldDescriptorProto{
				{Name: proto.String("key"), Number: proto.Int32(1), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
				{Name: proto.String("value"), Number: proto.Int32(2), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()},
			}}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return file.Messages().ByName("Order")
}

func TestDynamicProtoPayloadsRequireProjection(t *testing.T) {
	for _, message := range []proto.Message{&anypb.Any{}, &structpb.Struct{}, &structpb.Value{}, &structpb.ListValue{}} {
		b := builder[price](t)
		if err := b.BindProto("payload", message.ProtoReflect().Descriptor(), func(context.Context, *price) (proto.Message, error) { return message, nil }); err == nil {
			t.Fatal("dynamic payload was automatically exposed")
		}
	}
}

func TestNativeAndProtoTypeNameConflict(t *testing.T) {
	b := builder[price](t)
	if err := b.Bind("native", func(context.Context, *price) (address, error) { return address{}, nil }); err != nil {
		t.Fatal(err)
	}
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: proto.String("conflict.proto"), Package: proto.String("cel_test"), Syntax: proto.String("proto3"), MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("address")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := file.Messages().Get(0)
	if err := b.BindProto("message", desc, func(context.Context, *price) (*dynamicpb.Message, error) { return dynamicpb.NewMessage(desc), nil }); err == nil {
		t.Fatal("native/protobuf name conflict accepted")
	}
}

func protoOrder(desc protoreflect.MessageDescriptor, total int64) *dynamicpb.Message {
	m := dynamicpb.NewMessage(desc)
	m.Set(desc.Fields().ByName("total_amount"), protoreflect.ValueOfInt64(total))
	m.Set(desc.Fields().ByName("status"), protoreflect.ValueOfEnum(1))
	m.Set(desc.Fields().ByName("email"), protoreflect.ValueOfString(""))
	m.Set(desc.Fields().ByName("coupon"), protoreflect.ValueOfString(""))
	m.Mutable(desc.Fields().ByName("scores")).List().Append(protoreflect.ValueOfInt64(7))
	m.Mutable(desc.Fields().ByName("labels")).Map().Set(protoreflect.ValueOfString("tier").MapKey(), protoreflect.ValueOfString("vip"))
	return m
}

func protoCompiler(t testing.TB, desc protoreflect.MessageDescriptor, options ...cel.Option) *cel.Compiler[dynamicpb.Message] {
	t.Helper()
	b := builder[dynamicpb.Message](t, options...)
	if err := b.BindProto("order", desc, func(_ context.Context, input *dynamicpb.Message) (*dynamicpb.Message, error) { return input, nil }); err != nil {
		t.Fatal(err, errors.Unwrap(err))
	}
	return built(t, b)
}

func TestProtobufDescriptorNamesAndPresence(t *testing.T) {
	desc := messageDescriptor(t)
	for _, options := range [][]cel.Option{nil, {cel.WithJSONFieldNames()}} {
		c := protoCompiler(t, desc, options...)
		input := protoOrder(desc, 12000)
		checkTrue(t, c, input,
			"order.total_amount == 12000 && order.status == pricing.Status.ACTIVE",
			"has(order.email) && !has(order.phone) && has(order.coupon) && order.coupon == ''",
			"order.?coupon.hasValue() && order.?coupon.value() == '' && !has(order.child)",
			"order.scores == [7] && order.labels['tier'] == 'vip'")
		checkCompileFailure(t, c, "order.totalAmount > 0", "order.TotalAmount > 0", "order.contact == ''", "order.amount > 0")
		empty := dynamicpb.NewMessage(desc)
		checkTrue(t, c, empty, "!has(order.total_amount) && order.total_amount == 0 && !has(order.coupon) && !order.?coupon.hasValue() && size(order.scores) == 0 && size(order.labels) == 0")
		input.Set(desc.Fields().ByName("phone"), protoreflect.ValueOfString("123"))
		input.Set(desc.Fields().ByName("child"), protoreflect.ValueOfMessage(empty))
		checkTrue(t, c, input, "!has(order.email) && has(order.phone) && has(order.child) && order.child.total_amount == 0")
		input.Set(desc.Fields().ByName("status"), protoreflect.ValueOfEnum(99))
		checkTrue(t, c, input, "order.status == 99 && order.status != pricing.Status.ACTIVE")
	}
}

func TestGeneratedProtoAndNativeBindings(t *testing.T) {
	type input struct {
		Field  *descriptorpb.FieldDescriptorProto
		Native price
	}
	b := builder[input](t)
	desc := (&descriptorpb.FieldDescriptorProto{}).ProtoReflect().Descriptor()
	if err := b.BindProto("field", desc, func(_ context.Context, in *input) (*descriptorpb.FieldDescriptorProto, error) { return in.Field, nil }); err != nil {
		t.Fatal(err, errors.Unwrap(err))
	}
	if err := b.Bind("native", func(_ context.Context, in *input) (*price, error) { return &in.Native, nil }); err != nil {
		t.Fatal(err)
	}
	in := input{Field: &descriptorpb.FieldDescriptorProto{Name: proto.String("")}, Native: price{VIP: true}}
	c := built(t, b)
	checkTrue(t, c, &in, "has(field.name) && field.name == '' && !has(field.number) && native.VIP")
	checkCompileFailure(t, c, "field.Name == ''", "native.vip")
}

func TestProtobufBoundsAndDescriptorMismatch(t *testing.T) {
	desc := messageDescriptor(t)
	c := protoCompiler(t, desc)
	f := condition(t, c, "true")
	wrong := protoOrder(messageDescriptor(t), 1)
	if ok, err := f(context.Background(), wrong); ok || !errors.Is(err, cel.ErrProtoDescriptor) {
		t.Fatal("unexpected descriptor accepted")
	}
	for _, field := range []protoreflect.Name{"coupon", "labels", "scores", "child", "unknown"} {
		input := protoOrder(desc, 1)
		switch field {
		case "coupon":
			input.Set(desc.Fields().ByName(field), protoreflect.ValueOfString(strings.Repeat("x", 65537)))
		case "labels":
			input.Mutable(desc.Fields().ByName(field)).Map().Set(protoreflect.ValueOfString("x").MapKey(), protoreflect.ValueOfString(strings.Repeat("x", 65536)))
		case "scores":
			list := input.Mutable(desc.Fields().ByName(field)).List()
			for range 4096 {
				list.Append(protoreflect.ValueOfInt64(1))
			}
		case "child":
			input.Set(desc.Fields().ByName(field), protoreflect.ValueOfMessage(input))
		case "unknown":
			input.SetUnknown([]byte(strings.Repeat("x", 65537)))
		}
		if ok, err := f(context.Background(), input); ok || !errors.Is(err, cel.ErrInputLimit) {
			t.Fatalf("protobuf bound bypassed: %s", field)
		}
	}
	b := builder[dynamicpb.Message](t)
	project := func(_ context.Context, in *dynamicpb.Message) (*dynamicpb.Message, error) { return in, nil }
	if err := b.BindProto("initial", desc, project); err != nil {
		t.Fatal(err)
	}
	if err := b.BindProto("equivalent", messageDescriptor(t), project); err == nil {
		t.Fatal("distinct descriptor identity accepted")
	}
	changed := protodesc.ToFileDescriptorProto(desc.ParentFile())
	changed.MessageType[0].Field[0].Name = proto.String("renamed_amount")
	file, err := protodesc.NewFile(changed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BindProto("second", file.Messages().Get(0), project); err == nil {
		t.Fatal("conflicting descriptor accepted")
	}
	if err := b.BindProto("missing", nil, project); err == nil {
		t.Fatal("nil descriptor accepted")
	}
	if err := b.BindProto[*dynamicpb.Message]("nilProject", desc, nil); err == nil {
		t.Fatal("nil proto projector accepted")
	}
	checkTrue(t, built(t, b), protoOrder(desc, 1), "initial.total_amount == 1")
}
