package schema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	// Import to register proto descriptors in global registry
	_ "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	_ "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
)

func TestResolveProtoTypes_PositionKeeping(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"position_keeping.initiate_log": {
				Description:          "Initiate a position log entry",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod: "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
					ExposedParams: []string{
						"account_id",
					},
					ExposedReturns: []string{
						"log",
					},
					ParamAliases: map[string]string{
						"account_id": "position_id",
					},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err)

	handler := s.Handlers["position_keeping.initiate_log"]

	// Verify params were resolved
	require.NotEmpty(t, handler.Params, "params should be populated from proto")

	// account_id should be aliased to position_id
	_, hasAccountID := handler.Params["account_id"]
	assert.False(t, hasAccountID, "account_id should be aliased away")

	positionID, hasPositionID := handler.Params["position_id"]
	assert.True(t, hasPositionID, "position_id alias should exist")
	assert.Equal(t, TypeString, positionID.Type)

	// Returns should have "log" field (nested message -> TypeMap)
	require.NotEmpty(t, handler.Returns, "returns should be populated from proto")
	logField, hasLog := handler.Returns["log"]
	assert.True(t, hasLog, "log return field should exist")
	assert.Equal(t, TypeMap, logField.Type) // Nested message maps to TypeMap
}

func TestResolveProtoTypes_AllFields(t *testing.T) {
	// When ExposedParams is empty, all top-level fields should be included
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"position_keeping.initiate_log_all": {
				Description:          "All fields exposed",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod: "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err)

	handler := s.Handlers["position_keeping.initiate_log_all"]
	require.NotEmpty(t, handler.Params, "all top-level params should be included")

	// account_id is a known field on InitiateFinancialPositionLogRequest
	_, hasAccountID := handler.Params["account_id"]
	assert.True(t, hasAccountID, "account_id should be present when all fields exposed")
}

func TestResolveProtoTypes_ServiceNotFound(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.handler": {
				Description:          "Test",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod: "nonexistent.v1.FakeService/FakeMethod",
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProtoServiceNotFound)
}

func TestResolveProtoTypes_MethodNotFound(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.handler": {
				Description:          "Test",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod: "meridian.position_keeping.v1.PositionKeepingService/NonexistentMethod",
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProtoMethodNotFound)
}

func TestResolveProtoTypes_InlineHandlersUntouched(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.inline_handler": {
				Description:          "Handler with inline params",
				CompensationStrategy: CompensationStrategyNone,
				Params: map[string]*FieldDef{
					"id": {Type: TypeString, Required: true},
				},
				Returns: map[string]*FieldDef{
					"status": {Type: TypeString},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err)

	handler := s.Handlers["test.inline_handler"]
	assert.Len(t, handler.Params, 1)
	assert.Equal(t, TypeString, handler.Params["id"].Type)
	assert.Len(t, handler.Returns, 1)
}

func TestResolveProtoTypes_MixedFormat(t *testing.T) {
	// Schema with both inline-param and proto-referenced handlers
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.inline": {
				Description:          "Inline-param handler",
				CompensationStrategy: CompensationStrategyNone,
				Params: map[string]*FieldDef{
					"name": {Type: TypeString, Required: true},
				},
			},
			"position_keeping.proto_ref": {
				Description:          "Proto-referenced handler",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod:    "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
					ExposedParams: []string{"account_id"},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.NoError(t, err)

	// Inline-param handler unchanged
	inline := s.Handlers["test.inline"]
	assert.Len(t, inline.Params, 1)
	assert.Equal(t, TypeString, inline.Params["name"].Type)

	// Proto-referenced handler resolved
	protoRef := s.Handlers["position_keeping.proto_ref"]
	require.NotEmpty(t, protoRef.Params)
	_, hasAccountID := protoRef.Params["account_id"]
	assert.True(t, hasAccountID)
}

func TestParseProtoReferencedHandler(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  position_keeping.initiate_log:
    description: "Initiate a position log entry"
    compensation_strategy: none
    proto_ref:
      proto_rpc: "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog"
      exposed_params:
        - account_id
      exposed_returns:
        - log
      param_aliases:
        account_id: position_id
`
	schema, err := Parse([]byte(yaml))
	require.NoError(t, err)

	handler := schema.Handlers["position_keeping.initiate_log"]
	require.NotNil(t, handler.ProtoRef)
	assert.Equal(t,
		"meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
		handler.ProtoRef.FullMethod)
	assert.Equal(t, []string{"account_id"}, handler.ProtoRef.ExposedParams)
	assert.Equal(t, []string{"log"}, handler.ProtoRef.ExposedReturns)
	assert.Equal(t, map[string]string{"account_id": "position_id"}, handler.ProtoRef.ParamAliases)

	// Params should be empty until ResolveProtoTypes is called
	assert.Empty(t, handler.Params)
}

func TestProtoRefValidation_EmptyRPC(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    compensation_strategy: none
    proto_ref:
      proto_rpc: ""
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidProtoRPC)
}

func TestProtoRefValidation_MissingSlash(t *testing.T) {
	yaml := `
service: test
version: "1.0"
handlers:
  test.handler:
    description: "Test"
    compensation_strategy: none
    proto_ref:
      proto_rpc: "meridian.v1.Service.Method"
`
	_, err := Parse([]byte(yaml))
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidProtoRPC)
}

func TestResolveFieldPath(t *testing.T) {
	// Find the InitiateFinancialPositionLog method's request message
	serviceDesc, err := findServiceDescriptor(protoregistry.GlobalFiles,
		"meridian.position_keeping.v1.PositionKeepingService")
	require.NoError(t, err)

	method := serviceDesc.Methods().ByName(protoreflect.Name("InitiateFinancialPositionLog"))
	require.NotNil(t, method)

	reqMsg := method.Input()

	// Test simple field path
	fd := resolveFieldPath(reqMsg, "account_id")
	require.NotNil(t, fd, "account_id should exist in request message")
	assert.Equal(t, "account_id", string(fd.Name()))

	// Test invalid field path
	fd = resolveFieldPath(reqMsg, "nonexistent_field")
	assert.Nil(t, fd, "nonexistent field should return nil")
}

func TestLeafFieldName(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"account_id", "account_id"},
		{"log.log_id", "log_id"},
		{"log.status_tracking.current_status", "current_status"},
		{"", ""},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			assert.Equal(t, tc.expected, leafFieldName(tc.path))
		})
	}
}

func TestResolveProtoTypes_InvalidExposedParam(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.bad_field": {
				Description:          "Handler with invalid exposed param",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod:    "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
					ExposedParams: []string{"nonexistent_field"},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProtoFieldPathNotFound)
}

func TestResolveProtoTypes_InvalidExposedReturn(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.bad_return": {
				Description:          "Handler with invalid exposed return",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod:     "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
					ExposedReturns: []string{"nonexistent_return_field"},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrProtoFieldPathNotFound)
}

func TestResolveProtoTypes_UnknownAliasSource(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.bad_alias": {
				Description:          "Handler with unknown alias source",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod:    "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
					ExposedParams: []string{"account_id"},
					ParamAliases:  map[string]string{"typo_field": "position_id"},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownAliasSource)
}

func TestResolveProtoTypes_AliasCollision(t *testing.T) {
	s := &Schema{
		Service: "test",
		Version: "1.0",
		Handlers: map[string]*HandlerDef{
			"test.collision": {
				Description:          "Handler with alias collision",
				CompensationStrategy: CompensationStrategyNone,
				ProtoRef: &ProtoReference{
					FullMethod: "meridian.position_keeping.v1.PositionKeepingService/InitiateFinancialPositionLog",
					// Alias account_id to initial_entry which already exists in the request message
					ParamAliases: map[string]string{"account_id": "initial_entry"},
				},
			},
		},
	}

	err := s.ResolveProtoTypes(protoregistry.GlobalFiles)
	require.Error(t, err, "aliasing to an existing field name should fail")
	assert.ErrorIs(t, err, ErrAliasCollision)
}

func TestHasProtoRef(t *testing.T) {
	withRef := &HandlerDef{
		ProtoRef: &ProtoReference{FullMethod: "pkg.Svc/Method"},
	}
	assert.True(t, withRef.HasProtoRef())

	withoutRef := &HandlerDef{}
	assert.False(t, withoutRef.HasProtoRef())
}
