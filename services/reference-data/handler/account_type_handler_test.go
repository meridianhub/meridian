package handler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// --- Mock AccountType Registry ---

type mockAccountTypeRegistry struct {
	definitions  map[uuid.UUID]*accounttype.Definition
	createErr    error
	updateErr    error
	activateErr  error
	deprecateErr error
}

func newMockAccountTypeRegistry() *mockAccountTypeRegistry {
	return &mockAccountTypeRegistry{
		definitions: make(map[uuid.UUID]*accounttype.Definition),
	}
}

func (m *mockAccountTypeRegistry) GetDefinitionByID(_ context.Context, id uuid.UUID) (*accounttype.Definition, error) {
	def, ok := m.definitions[id]
	if !ok {
		return nil, accounttype.ErrNotFound
	}
	return def, nil
}

func (m *mockAccountTypeRegistry) GetDefinition(_ context.Context, code string, version int) (*accounttype.Definition, error) {
	for _, def := range m.definitions {
		if def.Code == code && def.Version == version {
			return def, nil
		}
	}
	return nil, accounttype.ErrNotFound
}

func (m *mockAccountTypeRegistry) GetActiveDefinition(_ context.Context, code string) (*accounttype.Definition, error) {
	for _, def := range m.definitions {
		if def.Code == code && def.Status == accounttype.StatusActive {
			return def, nil
		}
	}
	return nil, accounttype.ErrNotFound
}

func (m *mockAccountTypeRegistry) ListActive(_ context.Context) ([]*accounttype.Definition, error) {
	var result []*accounttype.Definition
	for _, def := range m.definitions {
		if def.Status == accounttype.StatusActive {
			result = append(result, def)
		}
	}
	return result, nil
}

func (m *mockAccountTypeRegistry) ListAll(_ context.Context, statusFilter []accounttype.Status) ([]*accounttype.Definition, error) {
	if len(statusFilter) == 0 {
		result := make([]*accounttype.Definition, 0, len(m.definitions))
		for _, def := range m.definitions {
			result = append(result, def)
		}
		return result, nil
	}
	filterSet := make(map[accounttype.Status]bool, len(statusFilter))
	for _, s := range statusFilter {
		filterSet[s] = true
	}
	var result []*accounttype.Definition
	for _, def := range m.definitions {
		if filterSet[def.Status] {
			result = append(result, def)
		}
	}
	return result, nil
}

func (m *mockAccountTypeRegistry) CreateDraft(_ context.Context, def *accounttype.Definition) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.definitions[def.ID] = def
	return nil
}

func (m *mockAccountTypeRegistry) UpdateDefinition(_ context.Context, code string, version int, _ *accounttype.Definition) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	for _, def := range m.definitions {
		if def.Code == code && def.Version == version {
			def.UpdatedAt = time.Now()
			return nil
		}
	}
	return accounttype.ErrNotFound
}

func (m *mockAccountTypeRegistry) ActivateAccountType(_ context.Context, code string, version int) error {
	if m.activateErr != nil {
		return m.activateErr
	}
	for _, def := range m.definitions {
		if def.Code == code && def.Version == version {
			def.Status = accounttype.StatusActive
			now := time.Now()
			def.ActivatedAt = &now
			return nil
		}
	}
	return accounttype.ErrNotFound
}

func (m *mockAccountTypeRegistry) DeprecateAccountType(_ context.Context, code string, version int, successorID *uuid.UUID) error {
	if m.deprecateErr != nil {
		return m.deprecateErr
	}
	for _, def := range m.definitions {
		if def.Code == code && def.Version == version {
			def.Status = accounttype.StatusDeprecated
			now := time.Now()
			def.DeprecatedAt = &now
			def.SuccessorID = successorID
			return nil
		}
	}
	return accounttype.ErrNotFound
}

func (m *mockAccountTypeRegistry) ValidateTransaction(_ context.Context, _ string, _ int, _ accounttype.AttributeBag) (accounttype.ValidationResult, error) {
	return accounttype.ValidationResult{Valid: true}, nil
}

func (m *mockAccountTypeRegistry) CheckEligibility(_ context.Context, _ string, _ int, _ accounttype.AttributeBag) (accounttype.ValidationResult, error) {
	return accounttype.ValidationResult{Valid: true}, nil
}

func (m *mockAccountTypeRegistry) GetProductFeatures(_ context.Context, _ string, _ int) (map[string]any, error) {
	return nil, nil
}

// --- Mock Instrument Registry ---

type mockInstrumentRegistryForValidation struct {
	instruments map[string]*registry.InstrumentDefinition
}

func (m *mockInstrumentRegistryForValidation) GetDefinition(_ context.Context, _ string, _ int) (*registry.InstrumentDefinition, error) {
	return nil, registry.ErrNotFound
}

func (m *mockInstrumentRegistryForValidation) GetActiveDefinition(_ context.Context, code string) (*registry.InstrumentDefinition, error) {
	def, ok := m.instruments[code]
	if !ok {
		return nil, registry.ErrNotFound
	}
	return def, nil
}

func (m *mockInstrumentRegistryForValidation) ListActive(_ context.Context) ([]*registry.InstrumentDefinition, error) {
	result := make([]*registry.InstrumentDefinition, 0, len(m.instruments))
	for _, def := range m.instruments {
		result = append(result, def)
	}
	return result, nil
}

func (m *mockInstrumentRegistryForValidation) ListByStatus(_ context.Context, _ registry.Status) ([]*registry.InstrumentDefinition, error) {
	return nil, nil
}

func (m *mockInstrumentRegistryForValidation) CreateDraft(_ context.Context, _ *registry.InstrumentDefinition) error {
	return nil
}

func (m *mockInstrumentRegistryForValidation) UpdateDefinition(_ context.Context, _ string, _ int, _ *registry.InstrumentDefinition) error {
	return nil
}

func (m *mockInstrumentRegistryForValidation) ActivateInstrument(_ context.Context, _ string, _ int) error {
	return nil
}

func (m *mockInstrumentRegistryForValidation) DeprecateInstrument(_ context.Context, _ string, _ int, _ *uuid.UUID) error {
	return nil
}

func (m *mockInstrumentRegistryForValidation) ValidateAttributes(_ context.Context, _ string, _ int, _ registry.AttributeBag) (registry.ValidationResult, error) {
	return registry.ValidationResult{Valid: true}, nil
}

// --- Helper ---

func newTestAccountTypeService(t *testing.T) (*AccountTypeService, *mockAccountTypeRegistry) {
	t.Helper()
	reg := newMockAccountTypeRegistry()
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)
	svc, err := NewAccountTypeService(reg, nil, compiler, nil)
	require.NoError(t, err)
	return svc, reg
}

func newTestAccountTypeServiceWithInstruments(t *testing.T) (*AccountTypeService, *mockAccountTypeRegistry, *mockInstrumentRegistryForValidation) {
	t.Helper()
	reg := newMockAccountTypeRegistry()
	instReg := &mockInstrumentRegistryForValidation{
		instruments: map[string]*registry.InstrumentDefinition{
			"GBP": {Code: "GBP", Status: registry.StatusActive},
			"USD": {Code: "USD", Status: registry.StatusActive},
			"EUR": {Code: "EUR", Status: registry.StatusActive},
		},
	}
	compiler, err := refcel.NewCompiler()
	require.NoError(t, err)
	svc, err := NewAccountTypeService(reg, instReg, compiler, nil)
	require.NoError(t, err)
	return svc, reg, instReg
}

func makeDraftDef(code string) *accounttype.Definition {
	now := time.Now()
	return &accounttype.Definition{
		ID:             uuid.New(),
		Code:           code,
		Version:        1,
		DisplayName:    code + " Account",
		Description:    "Test " + code,
		NormalBalance:  accounttype.NormalBalanceDebit,
		BehaviorClass:  accounttype.BehaviorClassCustomer,
		InstrumentCode: "GBP",
		Status:         accounttype.StatusDraft,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// --- Tests ---

func TestAccountTypeService_CreateDraft_MapsProtoToDomain(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	resp, err := svc.CreateDraft(ctx, &pb.CreateDraftRequest{
		Code:           "CUSTOMER_CURRENT",
		DisplayName:    "Customer Current Account",
		Description:    "Standard current account",
		NormalBalance:  pb.NormalBalance_NORMAL_BALANCE_DEBIT,
		BehaviorClass:  pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER,
		InstrumentCode: "GBP",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Definition)

	def := resp.Definition
	assert.Equal(t, "CUSTOMER_CURRENT", def.Code)
	assert.Equal(t, int32(1), def.Version)
	assert.Equal(t, "Customer Current Account", def.DisplayName)
	assert.Equal(t, pb.NormalBalance_NORMAL_BALANCE_DEBIT, def.NormalBalance)
	assert.Equal(t, pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER, def.BehaviorClass)
	assert.Equal(t, "GBP", def.InstrumentCode)
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DRAFT, def.Status)
	assert.Len(t, reg.definitions, 1)
}

func TestAccountTypeService_ActivateWithPreCheckFailure(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	def := makeDraftDef("TEST")
	reg.definitions[def.ID] = def
	reg.activateErr = errors.Join(
		accounttype.ErrNotDraft,
		errors.New("pre-check failure"),
	)

	_, err := svc.ActivateAccountType(ctx, &pb.ActivateAccountTypeRequest{
		Id: def.ID.String(),
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestAccountTypeService_ValidateProductDefinition_InvalidCEL(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	ctx := context.Background()

	resp, err := svc.ValidateProductDefinition(ctx, &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:          "TEST",
			ValidationCel: "this is not valid CEL !!!",
			BucketingCel:  "",
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "validation_cel", resp.Errors[0].Field)
	assert.Equal(t, "CEL_COMPILE_ERROR", resp.Errors[0].ErrorCode)
}

func TestAccountTypeService_ValidateProductDefinition_TypoSuggestsGBP(t *testing.T) {
	svc, _, _ := newTestAccountTypeServiceWithInstruments(t)
	ctx := context.Background()

	resp, err := svc.ValidateProductDefinition(ctx, &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:           "TEST",
			InstrumentCode: "GBB", // typo
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	require.Len(t, resp.Errors, 1)

	ve := resp.Errors[0]
	assert.Equal(t, "instrument_code", ve.Field)
	assert.Equal(t, "UNRESOLVABLE_REFERENCE", ve.ErrorCode)
	assert.Contains(t, ve.Suggestions, "GBP")
}

func TestAccountTypeService_OptimisticLockMapsToAborted(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	def := makeDraftDef("TEST")
	reg.definitions[def.ID] = def
	reg.updateErr = accounttype.ErrOptimisticLock

	_, err := svc.UpdateDefinition(ctx, &pb.UpdateDefinitionRequest{
		Id:          def.ID.String(),
		DisplayName: "New Name",
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Aborted, st.Code())
}

func TestAccountTypeService_GetDefinition_NotFound(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	ctx := context.Background()

	_, err := svc.GetDefinition(ctx, &pb.GetDefinitionRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAccountTypeService_GetActiveDefinition(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	def := makeDraftDef("ACTIVE_TEST")
	def.Status = accounttype.StatusActive
	now := time.Now()
	def.ActivatedAt = &now
	reg.definitions[def.ID] = def

	resp, err := svc.GetActiveDefinition(ctx, &pb.GetActiveDefinitionRequest{
		Code: "ACTIVE_TEST",
	})
	require.NoError(t, err)
	assert.Equal(t, "ACTIVE_TEST", resp.Definition.Code)
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE, resp.Definition.Status)
}

func TestAccountTypeService_DeprecateAccountType(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	def := makeDraftDef("DEPRECATE_TEST")
	def.Status = accounttype.StatusActive
	reg.definitions[def.ID] = def

	resp, err := svc.DeprecateAccountType(ctx, &pb.DeprecateAccountTypeRequest{
		Id: def.ID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DEPRECATED, resp.Definition.Status)
}

func TestAccountTypeService_ValidateProductDefinition_ValidDefinition(t *testing.T) {
	svc, _, _ := newTestAccountTypeServiceWithInstruments(t)
	ctx := context.Background()

	resp, err := svc.ValidateProductDefinition(ctx, &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:            "VALID_TEST",
			InstrumentCode:  "GBP",
			ValidationCel:   "parse_decimal(amount) > 0.0",
			AttributeSchema: `{"type":"object","properties":{"tier":{"type":"string"}}}`,
			Attributes:      map[string]string{"tier": "gold"},
		},
	})
	require.NoError(t, err)
	assert.True(t, resp.Valid)
	assert.Empty(t, resp.Errors)
}

func TestAccountTypeService_ValidateProductDefinition_InvalidSchema(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	ctx := context.Background()

	resp, err := svc.ValidateProductDefinition(ctx, &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:            "TEST",
			AttributeSchema: "not json at all",
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	require.Len(t, resp.Errors, 1)
	assert.Equal(t, "attribute_schema", resp.Errors[0].Field)
	assert.Equal(t, "INVALID_JSON", resp.Errors[0].ErrorCode)
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"GBP", "GBP", 0},
		{"GBP", "GBB", 1},
		{"GBP", "USD", 3},
		{"", "ABC", 3},
		{"ABC", "", 3},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, levenshtein(tt.a, tt.b), "levenshtein(%q, %q)", tt.a, tt.b)
	}
}

func TestFindSuggestions(t *testing.T) {
	candidates := []string{"GBP", "USD", "EUR", "JPY", "CHF"}

	suggestions := findSuggestions("GBB", candidates)
	assert.Contains(t, suggestions, "GBP")
	assert.LessOrEqual(t, len(suggestions), 3)

	suggestions = findSuggestions("XYZXYZ", candidates)
	assert.Empty(t, suggestions)
}

func TestAccountTypeToProto_RoundTrip(t *testing.T) {
	convID := uuid.New()
	convVersion := 2
	successorID := uuid.New()
	now := time.Now()

	def := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_CODE",
		Version:                        3,
		DisplayName:                    "Test Display",
		Description:                    "Test Description",
		NormalBalance:                  accounttype.NormalBalanceCredit,
		BehaviorClass:                  accounttype.BehaviorClassClearing,
		InstrumentCode:                 "USD",
		DefaultSagaPrefix:              "test_saga",
		DefaultConversionMethodID:      &convID,
		DefaultConversionMethodVersion: &convVersion,
		ValidationCEL:                  "amount > 0.0",
		BucketingCEL:                   `attributes["region"]`,
		EligibilityCEL:                 `party["kyc"] == "verified"`,
		AttributeSchema:                json.RawMessage(`{"type":"object"}`),
		Attributes:                     map[string]any{"tier": "gold"},
		Status:                         accounttype.StatusActive,
		IsSystem:                       true,
		SuccessorID:                    &successorID,
		CreatedAt:                      now,
		UpdatedAt:                      now,
		ActivatedAt:                    &now,
		DeprecatedAt:                   &now,
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				AccountTypeID:          uuid.New(),
				InputInstrument:        "GBP",
				ValuationMethodID:      uuid.New(),
				ValuationMethodVersion: 1,
				Parameters:             map[string]any{"rate": "1.25"},
				Status:                 accounttype.StatusActive,
			},
		},
	}

	proto := accountTypeToProto(def)
	assert.Equal(t, def.ID.String(), proto.Id)
	assert.Equal(t, "TEST_CODE", proto.Code)
	assert.Equal(t, int32(3), proto.Version)
	assert.Equal(t, pb.NormalBalance_NORMAL_BALANCE_CREDIT, proto.NormalBalance)
	assert.Equal(t, pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING, proto.BehaviorClass)
	assert.Equal(t, "USD", proto.InstrumentCode)
	assert.Equal(t, convID.String(), proto.DefaultConversionMethodId)
	assert.Equal(t, int32(2), proto.DefaultConversionMethodVersion)
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE, proto.Status)
	assert.True(t, proto.IsSystem)
	assert.Equal(t, successorID.String(), proto.SuccessorId)
	assert.NotNil(t, proto.ActivatedAt)
	assert.NotNil(t, proto.DeprecatedAt)
	assert.Len(t, proto.ValuationMethods, 1)
	assert.Equal(t, "GBP", proto.ValuationMethods[0].InputInstrument)
	assert.Equal(t, "gold", proto.Attributes["tier"])
}

func TestAccountTypeService_ListAll_ReturnsAllStatuses(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	draft := makeDraftDef("DRAFT_TYPE")
	draft.Status = accounttype.StatusDraft
	reg.definitions[draft.ID] = draft

	active := makeDraftDef("ACTIVE_TYPE")
	active.Status = accounttype.StatusActive
	reg.definitions[active.ID] = active

	deprecated := makeDraftDef("DEPRECATED_TYPE")
	deprecated.Status = accounttype.StatusDeprecated
	reg.definitions[deprecated.ID] = deprecated

	resp, err := svc.ListAll(ctx, &pb.ListAllRequest{})
	require.NoError(t, err)

	codes := make(map[string]bool)
	for _, d := range resp.Definitions {
		codes[d.Code] = true
	}
	assert.True(t, codes["DRAFT_TYPE"])
	assert.True(t, codes["ACTIVE_TYPE"])
	assert.True(t, codes["DEPRECATED_TYPE"])
}

func TestAccountTypeService_ListAll_StatusFilterWorks(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	draft := makeDraftDef("FILTER_DRAFT")
	draft.Status = accounttype.StatusDraft
	reg.definitions[draft.ID] = draft

	active := makeDraftDef("FILTER_ACTIVE")
	active.Status = accounttype.StatusActive
	reg.definitions[active.ID] = active

	deprecated := makeDraftDef("FILTER_DEPRECATED")
	deprecated.Status = accounttype.StatusDeprecated
	reg.definitions[deprecated.ID] = deprecated

	resp, err := svc.ListAll(ctx, &pb.ListAllRequest{
		StatusFilter: []pb.AccountTypeStatus{pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE},
	})
	require.NoError(t, err)
	require.Len(t, resp.Definitions, 1)
	assert.Equal(t, "FILTER_ACTIVE", resp.Definitions[0].Code)
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE, resp.Definitions[0].Status)
}

func TestAccountTypeService_ListAll_EmptyResult(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	ctx := context.Background()

	resp, err := svc.ListAll(ctx, &pb.ListAllRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.Definitions)
	assert.Empty(t, resp.NextPageToken)
}
