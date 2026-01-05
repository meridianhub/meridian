package handler

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// Static errors for tests.
var (
	errMockDatabase = errors.New("database error")
	errMockUnknown  = errors.New("unknown error")
)

// mockRegistry is a test double for the InstrumentRegistry interface.
type mockRegistry struct {
	definitions    map[string]*registry.InstrumentDefinition
	createDraftErr error
	updateDefErr   error
	getDefErr      error
	activateErr    error
	deprecateErr   error
	listActiveErr  error
	validateResult registry.ValidationResult
	validateErr    error
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		definitions: make(map[string]*registry.InstrumentDefinition),
	}
}

func (m *mockRegistry) key(code string, version int) string {
	return code + ":" + strconv.Itoa(version)
}

func (m *mockRegistry) GetDefinition(_ context.Context, code string, version int) (*registry.InstrumentDefinition, error) {
	if m.getDefErr != nil {
		return nil, m.getDefErr
	}
	def, ok := m.definitions[m.key(code, version)]
	if !ok {
		return nil, registry.ErrNotFound
	}
	return def, nil
}

func (m *mockRegistry) GetActiveDefinition(_ context.Context, code string) (*registry.InstrumentDefinition, error) {
	if m.getDefErr != nil {
		return nil, m.getDefErr
	}
	// Find highest version with ACTIVE status
	for _, def := range m.definitions {
		if def.Code == code && def.Status == registry.StatusActive {
			return def, nil
		}
	}
	return nil, registry.ErrNotFound
}

func (m *mockRegistry) ListActive(_ context.Context) ([]*registry.InstrumentDefinition, error) {
	if m.listActiveErr != nil {
		return nil, m.listActiveErr
	}
	var result []*registry.InstrumentDefinition
	for _, def := range m.definitions {
		if def.Status == registry.StatusActive {
			result = append(result, def)
		}
	}
	return result, nil
}

func (m *mockRegistry) CreateDraft(_ context.Context, def *registry.InstrumentDefinition) error {
	if m.createDraftErr != nil {
		return m.createDraftErr
	}
	if _, exists := m.definitions[m.key(def.Code, def.Version)]; exists {
		return registry.ErrAlreadyExists
	}
	m.definitions[m.key(def.Code, def.Version)] = def
	return nil
}

func (m *mockRegistry) UpdateDefinition(_ context.Context, code string, version int, updates *registry.InstrumentDefinition) error {
	if m.updateDefErr != nil {
		return m.updateDefErr
	}
	def, ok := m.definitions[m.key(code, version)]
	if !ok {
		return registry.ErrNotFound
	}
	if def.Status != registry.StatusDraft {
		return registry.ErrNotDraft
	}
	if def.IsSystem {
		return registry.ErrSystemInstrumentReadOnly
	}
	// Apply updates
	if updates.ValidationExpression != "" {
		def.ValidationExpression = updates.ValidationExpression
	}
	if updates.FungibilityKeyExpression != "" {
		def.FungibilityKeyExpression = updates.FungibilityKeyExpression
	}
	if updates.ErrorMessageExpression != "" {
		def.ErrorMessageExpression = updates.ErrorMessageExpression
	}
	if len(updates.AttributeSchema) > 0 {
		def.AttributeSchema = updates.AttributeSchema
	}
	if updates.DisplayName != "" {
		def.DisplayName = updates.DisplayName
	}
	if updates.Description != "" {
		def.Description = updates.Description
	}
	def.UpdatedAt = time.Now()
	return nil
}

func (m *mockRegistry) ActivateInstrument(_ context.Context, code string, version int) error {
	if m.activateErr != nil {
		return m.activateErr
	}
	def, ok := m.definitions[m.key(code, version)]
	if !ok {
		return registry.ErrNotFound
	}
	if def.Status != registry.StatusDraft {
		return registry.ErrNotDraft
	}
	if def.IsSystem {
		return registry.ErrSystemInstrumentReadOnly
	}
	def.Status = registry.StatusActive
	now := time.Now()
	def.ActivatedAt = &now
	def.UpdatedAt = now
	return nil
}

func (m *mockRegistry) DeprecateInstrument(_ context.Context, code string, version int) error {
	if m.deprecateErr != nil {
		return m.deprecateErr
	}
	def, ok := m.definitions[m.key(code, version)]
	if !ok {
		return registry.ErrNotFound
	}
	if def.Status != registry.StatusActive {
		return registry.ErrNotActive
	}
	if def.IsSystem {
		return registry.ErrSystemInstrumentReadOnly
	}
	def.Status = registry.StatusDeprecated
	now := time.Now()
	def.DeprecatedAt = &now
	def.UpdatedAt = now
	return nil
}

func (m *mockRegistry) ValidateAttributes(_ context.Context, _ string, _ int, _ registry.AttributeBag) (registry.ValidationResult, error) {
	if m.validateErr != nil {
		return registry.ValidationResult{}, m.validateErr
	}
	return m.validateResult, nil
}

func TestNewService(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		reg := newMockRegistry()
		svc, err := NewService(reg, compiler, nil)
		require.NoError(t, err)
		assert.NotNil(t, svc)
	})

	t.Run("nil registry", func(t *testing.T) {
		_, err := NewService(nil, compiler, nil)
		assert.ErrorIs(t, err, ErrRegistryNil)
	})

	t.Run("nil compiler", func(t *testing.T) {
		reg := newMockRegistry()
		_, err := NewService(reg, nil, nil)
		assert.ErrorIs(t, err, ErrCompilerNil)
	})
}

func TestRegisterInstrument(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.RegisterInstrument(context.Background(), &pb.RegisterInstrumentRequest{
			Code:      "KWH",
			Dimension: pb.Dimension_DIMENSION_ENERGY,
			Precision: 2,
		})

		require.NoError(t, err)
		assert.Equal(t, "KWH", resp.Instrument.Code)
		assert.Equal(t, int32(1), resp.Instrument.Version)
		assert.Equal(t, pb.InstrumentStatus_INSTRUMENT_STATUS_DRAFT, resp.Instrument.Status)
	})

	t.Run("already exists", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		// First registration succeeds
		_, err := svc.RegisterInstrument(context.Background(), &pb.RegisterInstrumentRequest{
			Code:      "USD",
			Dimension: pb.Dimension_DIMENSION_CURRENCY,
			Precision: 2,
		})
		require.NoError(t, err)

		// Second registration fails
		reg.createDraftErr = registry.ErrAlreadyExists
		_, err = svc.RegisterInstrument(context.Background(), &pb.RegisterInstrumentRequest{
			Code:      "USD",
			Dimension: pb.Dimension_DIMENSION_CURRENCY,
			Precision: 2,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("invalid CEL", func(t *testing.T) {
		reg := newMockRegistry()
		reg.createDraftErr = registry.ErrInvalidCEL
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.RegisterInstrument(context.Background(), &pb.RegisterInstrumentRequest{
			Code:                 "TEST",
			Dimension:            pb.Dimension_DIMENSION_COUNT,
			ValidationExpression: "invalid { syntax",
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestUpdateInstrument(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["TEST:1"] = &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      "TEST",
			Version:   1,
			Status:    registry.StatusDraft,
			CreatedAt: time.Now(),
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.UpdateInstrument(context.Background(), &pb.UpdateInstrumentRequest{
			Code:        "TEST",
			Version:     1,
			DisplayName: "Test Instrument",
		})

		require.NoError(t, err)
		assert.Equal(t, "Test Instrument", resp.Instrument.DisplayName)
	})

	t.Run("not found", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.UpdateInstrument(context.Background(), &pb.UpdateInstrumentRequest{
			Code:    "MISSING",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("not in draft status", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["ACTIVE:1"] = &registry.InstrumentDefinition{
			ID:      uuid.New(),
			Code:    "ACTIVE",
			Version: 1,
			Status:  registry.StatusActive,
		}
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.UpdateInstrument(context.Background(), &pb.UpdateInstrumentRequest{
			Code:    "ACTIVE",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("system instrument", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["USD:1"] = &registry.InstrumentDefinition{
			ID:       uuid.New(),
			Code:     "USD",
			Version:  1,
			Status:   registry.StatusDraft,
			IsSystem: true,
		}
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.UpdateInstrument(context.Background(), &pb.UpdateInstrumentRequest{
			Code:    "USD",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})
}

func TestRetrieveInstrument(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("specific version", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["USD:1"] = &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      "USD",
			Version:   1,
			Status:    registry.StatusActive,
			CreatedAt: time.Now(),
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.RetrieveInstrument(context.Background(), &pb.RetrieveInstrumentRequest{
			Code:    "USD",
			Version: 1,
		})

		require.NoError(t, err)
		assert.Equal(t, "USD", resp.Instrument.Code)
		assert.Equal(t, int32(1), resp.Instrument.Version)
	})

	t.Run("latest active version", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["EUR:1"] = &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      "EUR",
			Version:   1,
			Status:    registry.StatusActive,
			CreatedAt: time.Now(),
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.RetrieveInstrument(context.Background(), &pb.RetrieveInstrumentRequest{
			Code:    "EUR",
			Version: 0, // 0 means latest active
		})

		require.NoError(t, err)
		assert.Equal(t, "EUR", resp.Instrument.Code)
	})

	t.Run("not found", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.RetrieveInstrument(context.Background(), &pb.RetrieveInstrumentRequest{
			Code:    "MISSING",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestListInstruments(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("list all active", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["USD:1"] = &registry.InstrumentDefinition{
			ID:      uuid.New(),
			Code:    "USD",
			Version: 1,
			Status:  registry.StatusActive,
		}
		reg.definitions["EUR:1"] = &registry.InstrumentDefinition{
			ID:      uuid.New(),
			Code:    "EUR",
			Version: 1,
			Status:  registry.StatusActive,
		}
		reg.definitions["DRAFT:1"] = &registry.InstrumentDefinition{
			ID:      uuid.New(),
			Code:    "DRAFT",
			Version: 1,
			Status:  registry.StatusDraft, // Not active
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.ListInstruments(context.Background(), &pb.ListInstrumentsRequest{})

		require.NoError(t, err)
		assert.Len(t, resp.Instruments, 2)
	})

	t.Run("error from registry", func(t *testing.T) {
		reg := newMockRegistry()
		reg.listActiveErr = errMockDatabase
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.ListInstruments(context.Background(), &pb.ListInstrumentsRequest{})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestActivateInstrument(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["TEST:1"] = &registry.InstrumentDefinition{
			ID:        uuid.New(),
			Code:      "TEST",
			Version:   1,
			Status:    registry.StatusDraft,
			CreatedAt: time.Now(),
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.ActivateInstrument(context.Background(), &pb.ActivateInstrumentRequest{
			Code:    "TEST",
			Version: 1,
		})

		require.NoError(t, err)
		assert.Equal(t, pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, resp.Instrument.Status)
		assert.NotNil(t, resp.Instrument.ActivatedAt)
	})

	t.Run("not draft", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["ACTIVE:1"] = &registry.InstrumentDefinition{
			ID:      uuid.New(),
			Code:    "ACTIVE",
			Version: 1,
			Status:  registry.StatusActive,
		}
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.ActivateInstrument(context.Background(), &pb.ActivateInstrumentRequest{
			Code:    "ACTIVE",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestDeprecateInstrument(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("success", func(t *testing.T) {
		reg := newMockRegistry()
		now := time.Now()
		reg.definitions["TEST:1"] = &registry.InstrumentDefinition{
			ID:          uuid.New(),
			Code:        "TEST",
			Version:     1,
			Status:      registry.StatusActive,
			ActivatedAt: &now,
			CreatedAt:   now,
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.DeprecateInstrument(context.Background(), &pb.DeprecateInstrumentRequest{
			Code:    "TEST",
			Version: 1,
		})

		require.NoError(t, err)
		assert.Equal(t, pb.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED, resp.Instrument.Status)
		assert.NotNil(t, resp.Instrument.DeprecatedAt)
	})

	t.Run("not active", func(t *testing.T) {
		reg := newMockRegistry()
		reg.definitions["DRAFT:1"] = &registry.InstrumentDefinition{
			ID:      uuid.New(),
			Code:    "DRAFT",
			Version: 1,
			Status:  registry.StatusDraft,
		}
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.DeprecateInstrument(context.Background(), &pb.DeprecateInstrumentRequest{
			Code:    "DRAFT",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestEvaluateInstrument(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("valid expressions", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.EvaluateInstrument(context.Background(), &pb.EvaluateInstrumentRequest{
			ValidationExpression:     "parse_decimal(amount) > 0.0",
			FungibilityKeyExpression: `bucket_key([attributes["batch_id"]])`,
			TestAttributes:           map[string]string{"batch_id": "BATCH001"},
			TestAmount:               "100.50",
		})

		require.NoError(t, err)
		assert.Empty(t, resp.CompileErrors)
		assert.True(t, resp.ValidationResult)
		assert.NotEmpty(t, resp.FungibilityKey)
	})

	t.Run("compilation error", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.EvaluateInstrument(context.Background(), &pb.EvaluateInstrumentRequest{
			ValidationExpression: "invalid { syntax !!!",
		})

		require.NoError(t, err) // The RPC itself succeeds
		assert.NotEmpty(t, resp.CompileErrors)
		assert.Contains(t, resp.CompileErrors[0], "validation_expression")
	})

	t.Run("validation fails", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.EvaluateInstrument(context.Background(), &pb.EvaluateInstrumentRequest{
			ValidationExpression: "parse_decimal(amount) > 100.0",
			TestAmount:           "50.0", // Less than 100
		})

		require.NoError(t, err)
		assert.Empty(t, resp.CompileErrors)
		assert.False(t, resp.ValidationResult)
	})

	t.Run("with timestamps", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		now := time.Now()
		resp, err := svc.EvaluateInstrument(context.Background(), &pb.EvaluateInstrumentRequest{
			ValidationExpression: "valid_to > valid_from",
			TestValidFrom:        timestamppb.New(now.Add(-time.Hour)),
			TestValidTo:          timestamppb.New(now.Add(time.Hour)),
		})

		require.NoError(t, err)
		assert.Empty(t, resp.CompileErrors)
		assert.True(t, resp.ValidationResult)
	})
}

func TestGetAttributeSchema(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)

	t.Run("default schema", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.GetAttributeSchema(context.Background(), &pb.GetAttributeSchemaRequest{})

		require.NoError(t, err)
		assert.Contains(t, resp.JsonSchema, "CEL Attribute Bag")
		assert.Contains(t, resp.JsonSchema, "attributes")
		assert.Contains(t, resp.JsonSchema, "amount")
		assert.Empty(t, resp.InstrumentCode)
	})

	t.Run("instrument-specific schema", func(t *testing.T) {
		reg := newMockRegistry()
		customSchema := `{"type":"object","properties":{"batch_id":{"type":"string"}}}`
		reg.definitions["KWH:1"] = &registry.InstrumentDefinition{
			ID:              uuid.New(),
			Code:            "KWH",
			Version:         1,
			Status:          registry.StatusActive,
			AttributeSchema: []byte(customSchema),
			CreatedAt:       time.Now(),
		}
		svc, _ := NewService(reg, compiler, nil)

		resp, err := svc.GetAttributeSchema(context.Background(), &pb.GetAttributeSchemaRequest{
			Code:    "KWH",
			Version: 1,
		})

		require.NoError(t, err)
		assert.Equal(t, customSchema, resp.JsonSchema)
		assert.Equal(t, "KWH", resp.InstrumentCode)
		assert.Equal(t, int32(1), resp.InstrumentVersion)
	})

	t.Run("instrument not found", func(t *testing.T) {
		reg := newMockRegistry()
		svc, _ := NewService(reg, compiler, nil)

		_, err := svc.GetAttributeSchema(context.Background(), &pb.GetAttributeSchemaRequest{
			Code:    "MISSING",
			Version: 1,
		})

		assert.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestErrorMapping(t *testing.T) {
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)
	reg := newMockRegistry()
	svc, _ := NewService(reg, compiler, nil)

	tests := []struct {
		name     string
		err      error
		expected codes.Code
	}{
		{"NotFound", registry.ErrNotFound, codes.NotFound},
		{"SystemReadOnly", registry.ErrSystemInstrumentReadOnly, codes.PermissionDenied},
		{"NotDraft", registry.ErrNotDraft, codes.FailedPrecondition},
		{"NotActive", registry.ErrNotActive, codes.FailedPrecondition},
		{"InvalidCEL", registry.ErrInvalidCEL, codes.InvalidArgument},
		{"AlreadyExists", registry.ErrAlreadyExists, codes.AlreadyExists},
		{"OptimisticLock", registry.ErrOptimisticLock, codes.Aborted},
		{"InvalidStateTransition", registry.ErrInvalidStateTransition, codes.FailedPrecondition},
		{"Unknown", errMockUnknown, codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grpcErr := svc.mapDomainError(tt.err, "test", "CODE")
			st, _ := status.FromError(grpcErr)
			assert.Equal(t, tt.expected, st.Code())
		})
	}
}

func TestDimensionConversion(t *testing.T) {
	tests := []struct {
		proto  pb.Dimension
		domain registry.Dimension
	}{
		{pb.Dimension_DIMENSION_CURRENCY, registry.DimensionMonetary},
		{pb.Dimension_DIMENSION_ENERGY, registry.DimensionEnergy},
		{pb.Dimension_DIMENSION_MASS, registry.DimensionMass},
		{pb.Dimension_DIMENSION_VOLUME, registry.DimensionVolume},
		{pb.Dimension_DIMENSION_TIME, registry.DimensionTime},
		{pb.Dimension_DIMENSION_COMPUTE, registry.DimensionCompute},
		{pb.Dimension_DIMENSION_COUNT, registry.DimensionQuantity},
	}

	for _, tt := range tests {
		t.Run(tt.proto.String(), func(t *testing.T) {
			// proto -> domain
			assert.Equal(t, tt.domain, protoDimensionToDomain(tt.proto))
			// domain -> proto
			assert.Equal(t, tt.proto, domainDimensionToProto(tt.domain))
		})
	}
}

func TestStatusConversion(t *testing.T) {
	tests := []struct {
		proto  pb.InstrumentStatus
		domain registry.Status
	}{
		{pb.InstrumentStatus_INSTRUMENT_STATUS_DRAFT, registry.StatusDraft},
		{pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, registry.StatusActive},
		{pb.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED, registry.StatusDeprecated},
	}

	for _, tt := range tests {
		t.Run(tt.proto.String(), func(t *testing.T) {
			// proto -> domain
			assert.Equal(t, tt.domain, protoStatusToDomain(tt.proto))
			// domain -> proto
			assert.Equal(t, tt.proto, domainStatusToProto(tt.domain))
		})
	}
}
