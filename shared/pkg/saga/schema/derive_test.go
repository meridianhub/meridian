package schema_test

import (
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"google.golang.org/protobuf/reflect/protodesc"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
)

// buildTestProto creates a synthetic proto file descriptor for testing.
// This avoids depending on generated .pb.go files that may not exist in the worktree.
func buildTestProto(t *testing.T) (proto.Message, proto.Message) {
	t.Helper()

	// Build an enum descriptor: POSTING_DIRECTION_{UNSPECIFIED, DEBIT, CREDIT}
	enumName := protoreflect.Name("PostingDirection")
	enumFullName := protoreflect.FullName("test.PostingDirection")
	_ = enumFullName

	unspecified := "POSTING_DIRECTION_UNSPECIFIED"
	debit := "POSTING_DIRECTION_DEBIT"
	credit := "POSTING_DIRECTION_CREDIT"

	enumVal0 := int32(0)
	enumVal1 := int32(1)
	enumVal2 := int32(2)

	enumDescProto := &descriptorpb.EnumDescriptorProto{
		Name: strPtr(string(enumName)),
		Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: &unspecified, Number: &enumVal0},
			{Name: &debit, Number: &enumVal1},
			{Name: &credit, Number: &enumVal2},
		},
	}

	// Build request message: TestRequest
	reqMsg := &descriptorpb.DescriptorProto{
		Name: strPtr("TestRequest"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     strPtr("account_id"),
				Number:   int32Ptr(1),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("accountId"),
			},
			{
				Name:     strPtr("amount"),
				Number:   int32Ptr(2),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("amount"),
			},
			{
				Name:     strPtr("direction"),
				Number:   int32Ptr(3),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_ENUM),
				TypeName: strPtr(".test.PostingDirection"),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("direction"),
			},
			{
				Name:     strPtr("count"),
				Number:   int32Ptr(4),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_INT32),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("count"),
			},
			{
				Name:     strPtr("is_active"),
				Number:   int32Ptr(5),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("isActive"),
			},
			{
				Name:     strPtr("version"),
				Number:   int32Ptr(6),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_INT64),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("version"),
			},
			{
				Name:     strPtr("tags"),
				Number:   int32Ptr(7),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_REPEATED),
				JsonName: strPtr("tags"),
			},
			{
				Name:     strPtr("flags"),
				Number:   int32Ptr(8),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_UINT32),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("flags"),
			},
			{
				Name:     strPtr("payload"),
				Number:   int32Ptr(9),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_BYTES),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("payload"),
			},
		},
	}

	// Build response message: TestResponse
	respMsg := &descriptorpb.DescriptorProto{
		Name: strPtr("TestResponse"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     strPtr("log_id"),
				Number:   int32Ptr(1),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("logId"),
			},
			{
				Name:     strPtr("success"),
				Number:   int32Ptr(2),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("success"),
			},
		},
	}

	syntax := "proto3"
	fileDesc := &descriptorpb.FileDescriptorProto{
		Name:        strPtr("test.proto"),
		Package:     strPtr("test"),
		Syntax:      &syntax,
		EnumType:    []*descriptorpb.EnumDescriptorProto{enumDescProto},
		MessageType: []*descriptorpb.DescriptorProto{reqMsg, respMsg},
	}

	fd, err := protodesc.NewFile(fileDesc, nil)
	require.NoError(t, err)

	reqMsgDesc := fd.Messages().ByName("TestRequest")
	require.NotNil(t, reqMsgDesc)
	respMsgDesc := fd.Messages().ByName("TestResponse")
	require.NotNil(t, respMsgDesc)

	return dynamicpb.NewMessage(reqMsgDesc), dynamicpb.NewMessage(respMsgDesc)
}

func strPtr(s string) *string { return &s }
func int32Ptr(i int32) *int32 { return &i }
func fieldType(t descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto_Type {
	return &t
}

func fieldLabel(l descriptorpb.FieldDescriptorProto_Label) *descriptorpb.FieldDescriptorProto_Label {
	return &l
}

func TestDeriveSchema_EmptyRegistry(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)
	assert.Empty(t, s.Handlers)
}

func TestDeriveSchema_HandlerWithoutProto(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	meta := &saga.HandlerMetadata{
		Description:          "A handler without proto types",
		Compensate:           "undo_foo",
		CompensationStrategy: "auto",
		Version:              2,
	}
	err := registry.RegisterWithMetadata("test.do_foo", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, meta)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h, ok := s.Handlers["test.do_foo"]
	require.True(t, ok)
	assert.Equal(t, "A handler without proto types", h.Description)
	assert.Equal(t, "undo_foo", h.Compensate)
	assert.Equal(t, 2, h.Version)
	assert.Empty(t, h.Params)
	assert.Empty(t, h.Returns)
}

func TestDeriveSchema_HandlerWithNilMetadata(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	err := registry.RegisterWithMetadata("test.nil_meta", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, nil)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h, ok := s.Handlers["test.nil_meta"]
	require.True(t, ok)
	assert.Empty(t, h.Params)
	assert.Empty(t, h.Returns)
}

func TestDeriveSchema_ProtoFieldMapping(t *testing.T) {
	reqProto, respProto := buildTestProto(t)

	registry := saga.NewHandlerRegistry()
	meta := &saga.HandlerMetadata{
		Description:          "Test handler with proto types",
		Compensate:           "test.undo",
		CompensationStrategy: "auto",
		ProtoRequestType:     reqProto,
		ProtoResponseType:    respProto,
		Version:              1,
	}
	err := registry.RegisterWithMetadata("test.create", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, meta)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h := s.Handlers["test.create"]
	require.NotNil(t, h)

	// Verify field type mappings
	tests := []struct {
		field    string
		wantType schema.FieldType
	}{
		{"account_id", schema.TypeString},
		{"amount", schema.TypeString},
		{"count", schema.TypeInt32},
		{"is_active", schema.TypeBool},
		{"version", schema.TypeInt64},
		{"flags", schema.TypeUint32},
		{"payload", schema.TypeString}, // bytes -> string (base64)
	}

	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			f, ok := h.Params[tc.field]
			require.True(t, ok, "param %s not found", tc.field)
			assert.Equal(t, tc.wantType, f.Type, "param %s type mismatch", tc.field)
		})
	}

	// Verify enum field
	dirField, ok := h.Params["direction"]
	require.True(t, ok)
	assert.Equal(t, schema.TypeEnum, dirField.Type)
	assert.ElementsMatch(t, []string{"DEBIT", "CREDIT"}, dirField.Values)

	// Verify repeated field
	tagsField, ok := h.Params["tags"]
	require.True(t, ok)
	assert.Equal(t, schema.TypeArray, tagsField.Type)
	assert.Equal(t, schema.TypeString, tagsField.ItemType)

	// Verify response fields
	logIDField, ok := h.Returns["log_id"]
	require.True(t, ok)
	assert.Equal(t, schema.TypeString, logIDField.Type)

	successField, ok := h.Returns["success"]
	require.True(t, ok)
	assert.Equal(t, schema.TypeBool, successField.Type)
}

func TestDeriveSchema_ParamOverrides(t *testing.T) {
	reqProto, _ := buildTestProto(t)

	reqTrue := true

	registry := saga.NewHandlerRegistry()
	meta := &saga.HandlerMetadata{
		ProtoRequestType:     reqProto,
		Compensate:           "test.undo",
		CompensationStrategy: "auto",
		ParamOverrides: map[string]saga.ParamOverride{
			"amount": {
				Type:  "Decimal", // Override string -> Decimal
				Alias: "qty",     // Rename in Starlark
			},
			"account_id": {
				Required: &reqTrue,
			},
			"count": {
				Derived: true, // Should be excluded from params
			},
			"is_active": {
				Deprecated: "use status field instead",
			},
		},
		Version: 1,
	}
	err := registry.RegisterWithMetadata("test.overridden", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, meta)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h := s.Handlers["test.overridden"]
	require.NotNil(t, h)

	// Alias: "amount" should appear as "qty"
	_, hasAmount := h.Params["amount"]
	assert.False(t, hasAmount, "original name 'amount' should not appear when alias is set")
	qtyField, ok := h.Params["qty"]
	require.True(t, ok, "alias 'qty' should be present")
	assert.Equal(t, schema.TypeDecimal, qtyField.Type)

	// Required override
	accField, ok := h.Params["account_id"]
	require.True(t, ok)
	assert.True(t, accField.Required)

	// Derived: "count" should be excluded
	_, hasCount := h.Params["count"]
	assert.False(t, hasCount, "derived param 'count' should be excluded")

	// Deprecated: "is_active" should still be present but we track deprecation
	// (FieldDef doesn't have a Deprecated field, so the param is just present)
	_, hasActive := h.Params["is_active"]
	assert.True(t, hasActive, "deprecated param should still be present")
}

func TestDeriveSchema_EnumPrefixStripping(t *testing.T) {
	// Test various enum naming patterns
	tests := []struct {
		name     string
		values   []string
		expected []string
	}{
		{
			name:     "POSTING_DIRECTION prefix",
			values:   []string{"POSTING_DIRECTION_DEBIT", "POSTING_DIRECTION_CREDIT"},
			expected: []string{"DEBIT", "CREDIT"},
		},
		{
			name:     "with UNSPECIFIED included",
			values:   []string{"POSTING_DIRECTION_UNSPECIFIED", "POSTING_DIRECTION_DEBIT", "POSTING_DIRECTION_CREDIT"},
			expected: []string{"UNSPECIFIED", "DEBIT", "CREDIT"},
		},
		{
			name:     "single segment values",
			values:   []string{"FOO", "BAR"},
			expected: []string{"FOO", "BAR"},
		},
		{
			name:     "empty",
			values:   []string{},
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stripped := schema.StripEnumPrefix(tc.values)
			assert.Equal(t, tc.expected, stripped)
		})
	}
}

func TestDeriveSchema_HandlerMetadataFields(t *testing.T) {
	registry := saga.NewHandlerRegistry()
	meta := &saga.HandlerMetadata{
		Description:          "Transfer funds between accounts",
		Compensate:           "test.reverse_transfer",
		CompensationStrategy: "auto",
		HasAutoCompensation:  true,
		Version:              3,
		DeprecatedMessage:    "use test.transfer_v2 instead",
		Conversions: []saga.HandlerConversion{
			{
				FromVersion:  2,
				FromName:     "test.old_transfer",
				ParamMapping: map[string]string{"source": "from_account"},
				Defaults:     map[string]string{"currency": "USD"},
			},
		},
	}
	err := registry.RegisterWithMetadata("test.transfer", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, meta)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h := s.Handlers["test.transfer"]
	require.NotNil(t, h)

	assert.Equal(t, "Transfer funds between accounts", h.Description)
	assert.Equal(t, "test.reverse_transfer", h.Compensate)
	assert.Equal(t, schema.CompensationStrategyAuto, h.CompensationStrategy)
	assert.Equal(t, 3, h.Version)
	assert.True(t, h.Deprecated)

	require.Len(t, h.Conversions, 1)
	conv := h.Conversions[0]
	assert.Equal(t, 2, conv.FromVersion)
	assert.Equal(t, "test.old_transfer", conv.FromName)
	assert.Equal(t, map[string]string{"source": "from_account"}, conv.ParamMapping)
	assert.Equal(t, map[string]string{"currency": "USD"}, conv.Defaults)
}

func TestDeriveSchema_MessageField(t *testing.T) {
	// Test that nested message fields map to TypeMap
	// Use wrapperspb.StringValue as a well-known message type
	reqProto := &wrapperspb.StringValue{}
	_ = reqProto

	// Build a proto with a message-typed field
	nestedMsg := &descriptorpb.DescriptorProto{
		Name: strPtr("InnerMessage"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     strPtr("value"),
				Number:   int32Ptr(1),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("value"),
			},
		},
	}

	outerMsg := &descriptorpb.DescriptorProto{
		Name: strPtr("OuterMessage"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     strPtr("inner"),
				Number:   int32Ptr(1),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_MESSAGE),
				TypeName: strPtr(".test2.InnerMessage"),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
				JsonName: strPtr("inner"),
			},
		},
	}

	syntax := "proto3"
	fileDesc := &descriptorpb.FileDescriptorProto{
		Name:        strPtr("test2.proto"),
		Package:     strPtr("test2"),
		Syntax:      &syntax,
		MessageType: []*descriptorpb.DescriptorProto{nestedMsg, outerMsg},
	}

	fd, err := protodesc.NewFile(fileDesc, nil)
	require.NoError(t, err)

	outerMsgDesc := fd.Messages().ByName("OuterMessage")
	require.NotNil(t, outerMsgDesc)

	registry := saga.NewHandlerRegistry()
	meta := &saga.HandlerMetadata{
		ProtoRequestType:     dynamicpb.NewMessage(outerMsgDesc),
		CompensationStrategy: "none",
		Version:              1,
	}
	err = registry.RegisterWithMetadata("test.nested", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, meta)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h := s.Handlers["test.nested"]
	require.NotNil(t, h)

	innerField, ok := h.Params["inner"]
	require.True(t, ok)
	assert.Equal(t, schema.TypeMap, innerField.Type)
}

func TestDeriveHandlerDef_Exported(t *testing.T) {
	// Verify DeriveHandlerDef is exported for contract tests (Task 4)
	meta := &saga.HandlerMetadata{
		Description:          "exported function test",
		CompensationStrategy: "none",
		Version:              1,
	}

	hd := schema.DeriveHandlerDef("test.exported", meta)
	require.NotNil(t, hd)
	assert.Equal(t, "exported function test", hd.Description)
}

func TestDeriveSchema_MapField(t *testing.T) {
	// Proto map<string,string> fields should derive as TypeMap with key/value types
	mapEntry := &descriptorpb.DescriptorProto{
		Name: strPtr("AttributesEntry"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:   strPtr("key"),
				Number: int32Ptr(1),
				Type:   fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:  fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
			},
			{
				Name:   strPtr("value"),
				Number: int32Ptr(2),
				Type:   fieldType(descriptorpb.FieldDescriptorProto_TYPE_STRING),
				Label:  fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL),
			},
		},
		Options: &descriptorpb.MessageOptions{
			MapEntry: boolPtr(true),
		},
	}

	outerMsg := &descriptorpb.DescriptorProto{
		Name: strPtr("MapMessage"),
		Field: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     strPtr("attributes"),
				Number:   int32Ptr(1),
				Type:     fieldType(descriptorpb.FieldDescriptorProto_TYPE_MESSAGE),
				TypeName: strPtr(".test3.MapMessage.AttributesEntry"),
				Label:    fieldLabel(descriptorpb.FieldDescriptorProto_LABEL_REPEATED),
				JsonName: strPtr("attributes"),
			},
		},
		NestedType: []*descriptorpb.DescriptorProto{mapEntry},
	}

	syntax := "proto3"
	fileDesc := &descriptorpb.FileDescriptorProto{
		Name:        strPtr("test3.proto"),
		Package:     strPtr("test3"),
		Syntax:      &syntax,
		MessageType: []*descriptorpb.DescriptorProto{outerMsg},
	}

	fd, err := protodesc.NewFile(fileDesc, nil)
	require.NoError(t, err)

	msgDesc := fd.Messages().ByName("MapMessage")
	require.NotNil(t, msgDesc)

	registry := saga.NewHandlerRegistry()
	meta := &saga.HandlerMetadata{
		ProtoRequestType:     dynamicpb.NewMessage(msgDesc),
		CompensationStrategy: "none",
		Version:              1,
	}
	err = registry.RegisterWithMetadata("test.with_map", func(_ *saga.StarlarkContext, _ map[string]any) (any, error) {
		return nil, nil
	}, meta)
	require.NoError(t, err)

	s, err := schema.DeriveSchema(registry)
	require.NoError(t, err)

	h := s.Handlers["test.with_map"]
	require.NotNil(t, h)

	attrField, ok := h.Params["attributes"]
	require.True(t, ok)
	assert.Equal(t, schema.TypeMap, attrField.Type)
	assert.Equal(t, schema.TypeString, attrField.KeyType)
	assert.Equal(t, schema.TypeString, attrField.ValueType)
}

func boolPtr(b bool) *bool { return &b }
