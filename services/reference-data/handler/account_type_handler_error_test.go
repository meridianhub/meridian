package handler

import (
	"context"
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
)

// --- NewAccountTypeService error paths ---

func TestNewAccountTypeService_NilRegistry(t *testing.T) {
	_, err := NewAccountTypeService(nil, nil, nil, nil)
	require.ErrorIs(t, err, ErrAccountTypeRegistryNil)
}

func TestNewAccountTypeService_NilCompiler(t *testing.T) {
	reg := newMockAccountTypeRegistry()
	_, err := NewAccountTypeService(reg, nil, nil, nil)
	require.ErrorIs(t, err, ErrCompilerNil)
}

// --- GetDefinition error paths ---

func TestAccountTypeService_GetDefinition_InvalidID(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.GetDefinition(context.Background(), &pb.GetDefinitionRequest{
		Id: "not-a-uuid",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// --- GetActiveDefinition error paths ---

func TestAccountTypeService_GetActiveDefinition_EmptyCode(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.GetActiveDefinition(context.Background(), &pb.GetActiveDefinitionRequest{
		Code: "",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_GetActiveDefinition_NotFound(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.GetActiveDefinition(context.Background(), &pb.GetActiveDefinitionRequest{
		Code: "NONEXISTENT",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// --- ListActive ---

func TestAccountTypeService_ListActive_Pagination(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	for _, code := range []string{"A_TYPE", "B_TYPE", "C_TYPE", "D_TYPE", "E_TYPE"} {
		def := makeDraftDef(code)
		def.Status = accounttype.StatusActive
		reg.definitions[def.ID] = def
	}

	resp, err := svc.ListActive(ctx, &pb.ListActiveRequest{PageSize: 2})
	require.NoError(t, err)
	assert.Len(t, resp.Definitions, 2)
	assert.NotEmpty(t, resp.NextPageToken)

	resp2, err := svc.ListActive(ctx, &pb.ListActiveRequest{
		PageSize:  2,
		PageToken: resp.NextPageToken,
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Definitions, 2)
}

func TestAccountTypeService_ListActive_BehaviorClassFilter(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	ctx := context.Background()

	def1 := makeDraftDef("CUSTOMER")
	def1.Status = accounttype.StatusActive
	def1.BehaviorClass = accounttype.BehaviorClassCustomer
	reg.definitions[def1.ID] = def1

	def2 := makeDraftDef("CLEARING")
	def2.Status = accounttype.StatusActive
	def2.BehaviorClass = accounttype.BehaviorClassClearing
	reg.definitions[def2.ID] = def2

	resp, err := svc.ListActive(ctx, &pb.ListActiveRequest{
		BehaviorClassFilter: pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Definitions, 1)
	assert.Equal(t, "CUSTOMER", resp.Definitions[0].Code)
}

// --- UpdateDefinition error paths ---

func TestAccountTypeService_UpdateDefinition_InvalidID(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.UpdateDefinition(context.Background(), &pb.UpdateDefinitionRequest{
		Id: "invalid",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_UpdateDefinition_NotFound(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.UpdateDefinition(context.Background(), &pb.UpdateDefinitionRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAccountTypeService_UpdateDefinition_InvalidConversionMethodID(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	def := makeDraftDef("UPDATE_TEST")
	reg.definitions[def.ID] = def

	_, err := svc.UpdateDefinition(context.Background(), &pb.UpdateDefinitionRequest{
		Id:                             def.ID.String(),
		DefaultConversionMethodId:      "not-a-uuid",
		DefaultConversionMethodVersion: 1,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// --- ActivateAccountType error paths ---

func TestAccountTypeService_ActivateAccountType_InvalidID(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.ActivateAccountType(context.Background(), &pb.ActivateAccountTypeRequest{
		Id: "bad-uuid",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_ActivateAccountType_NotFound(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.ActivateAccountType(context.Background(), &pb.ActivateAccountTypeRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

// --- DeprecateAccountType error paths ---

func TestAccountTypeService_DeprecateAccountType_InvalidID(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.DeprecateAccountType(context.Background(), &pb.DeprecateAccountTypeRequest{
		Id: "bad-uuid",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_DeprecateAccountType_NotFound(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.DeprecateAccountType(context.Background(), &pb.DeprecateAccountTypeRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestAccountTypeService_DeprecateAccountType_InvalidSuccessorID(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	def := makeDraftDef("DEP_TEST")
	def.Status = accounttype.StatusActive
	reg.definitions[def.ID] = def

	_, err := svc.DeprecateAccountType(context.Background(), &pb.DeprecateAccountTypeRequest{
		Id:          def.ID.String(),
		SuccessorId: "not-a-uuid",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_DeprecateAccountType_DeprecateError(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	def := makeDraftDef("DEP_ERR_TEST")
	def.Status = accounttype.StatusActive
	reg.definitions[def.ID] = def
	reg.deprecateErr = accounttype.ErrNotActive

	_, err := svc.DeprecateAccountType(context.Background(), &pb.DeprecateAccountTypeRequest{
		Id: def.ID.String(),
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// --- ValidateProductDefinition error paths ---

func TestAccountTypeService_ValidateProductDefinition_NilDefinition(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.ValidateProductDefinition(context.Background(), &pb.ValidateProductDefinitionRequest{
		Definition: nil,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_ValidateProductDefinition_AllCELInvalid(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	resp, err := svc.ValidateProductDefinition(context.Background(), &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:           "TEST",
			ValidationCel:  "!!! invalid !!!",
			BucketingCel:   "!!! also invalid !!!",
			EligibilityCel: "!!! very invalid !!!",
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	assert.Len(t, resp.Errors, 3)
	for _, e := range resp.Errors {
		assert.Equal(t, "CEL_COMPILE_ERROR", e.ErrorCode)
	}
}

func TestAccountTypeService_ValidateProductDefinition_SchemaValidationFails(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	resp, err := svc.ValidateProductDefinition(context.Background(), &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:            "TEST",
			AttributeSchema: `{"type":"object","properties":{"tier":{"type":"string"}},"required":["tier"]}`,
			Attributes:      map[string]string{"wrong_field": "value"},
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	found := false
	for _, e := range resp.Errors {
		if e.ErrorCode == "SCHEMA_VALIDATION_FAILED" {
			found = true
		}
	}
	assert.True(t, found, "expected SCHEMA_VALIDATION_FAILED error")
}

func TestAccountTypeService_ValidateProductDefinition_ValuationMethodInstrumentRef(t *testing.T) {
	svc, _, _ := newTestAccountTypeServiceWithInstruments(t)

	resp, err := svc.ValidateProductDefinition(context.Background(), &pb.ValidateProductDefinitionRequest{
		Definition: &pb.AccountTypeDefinition{
			Code:           "TEST",
			InstrumentCode: "GBP",
			ValuationMethods: []*pb.ValuationMethodTemplate{
				{InputInstrument: "INVALID_INSTR"},
			},
		},
	})
	require.NoError(t, err)
	assert.False(t, resp.Valid)
	found := false
	for _, e := range resp.Errors {
		if e.Field == "valuation_methods[0].input_instrument" {
			found = true
			assert.Equal(t, "UNRESOLVABLE_REFERENCE", e.ErrorCode)
		}
	}
	assert.True(t, found)
}

// --- mapDomainError ---

func TestMapDomainError_AllSentinels(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	ctx := context.Background()

	tests := []struct {
		err      error
		expected codes.Code
	}{
		{accounttype.ErrNotFound, codes.NotFound},
		{accounttype.ErrOptimisticLock, codes.Aborted},
		{accounttype.ErrNotDraft, codes.FailedPrecondition},
		{accounttype.ErrNotActive, codes.FailedPrecondition},
		{accounttype.ErrFieldImmutable, codes.InvalidArgument},
		{accounttype.ErrInvalidCEL, codes.InvalidArgument},
		{accounttype.ErrActiveCodeExists, codes.AlreadyExists},
		{accounttype.ErrInvalidBehaviorClass, codes.InvalidArgument},
		{accounttype.ErrInvalidNormalBalance, codes.InvalidArgument},
		{accounttype.ErrConversionMethodPair, codes.InvalidArgument},
		{accounttype.ErrSuccessorWriteOnce, codes.FailedPrecondition},
		{accounttype.ErrInvalidInstrument, codes.FailedPrecondition},
		{accounttype.ErrInvalidConversionMethod, codes.FailedPrecondition},
		{accounttype.ErrInvalidValuationMethod, codes.FailedPrecondition},
		{accounttype.ErrInvalidAttributeSchema, codes.InvalidArgument},
		{accounttype.ErrAttributesInvalid, codes.InvalidArgument},
	}

	for _, tt := range tests {
		grpcErr := svc.mapDomainError(ctx, tt.err, "TestOp", "test-id")
		st, ok := status.FromError(grpcErr)
		require.True(t, ok, "error %v should be gRPC status", tt.err)
		assert.Equal(t, tt.expected, st.Code(), "error %v should map to %v", tt.err, tt.expected)
	}
}

func TestMapDomainError_UnknownError(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	grpcErr := svc.mapDomainError(context.Background(), errors.New("unexpected"), "TestOp", "id")
	st, ok := status.FromError(grpcErr)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}

func TestMapDomainError_WrappedError(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	wrapped := errors.Join(accounttype.ErrNotFound, errors.New("extra context"))
	grpcErr := svc.mapDomainError(context.Background(), wrapped, "TestOp", "id")
	st, ok := status.FromError(grpcErr)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// --- Proto mapping edge cases ---

func TestDomainNormalBalanceToProto_AllValues(t *testing.T) {
	assert.Equal(t, pb.NormalBalance_NORMAL_BALANCE_DEBIT, domainNormalBalanceToProto(accounttype.NormalBalanceDebit))
	assert.Equal(t, pb.NormalBalance_NORMAL_BALANCE_CREDIT, domainNormalBalanceToProto(accounttype.NormalBalanceCredit))
	assert.Equal(t, pb.NormalBalance_NORMAL_BALANCE_UNSPECIFIED, domainNormalBalanceToProto(accounttype.NormalBalance("UNKNOWN")))
}

func TestProtoBehaviorNormalBalanceToDomainString(t *testing.T) {
	assert.Equal(t, "", protoBehaviorNormalBalanceToDomainString(pb.NormalBalance_NORMAL_BALANCE_UNSPECIFIED))
	assert.Equal(t, "DEBIT", protoBehaviorNormalBalanceToDomainString(pb.NormalBalance_NORMAL_BALANCE_DEBIT))
	assert.Equal(t, "CREDIT", protoBehaviorNormalBalanceToDomainString(pb.NormalBalance_NORMAL_BALANCE_CREDIT))
	assert.Equal(t, "", protoBehaviorNormalBalanceToDomainString(pb.NormalBalance(999)))
}

func TestDomainAccountTypeStatusToProto_AllValues(t *testing.T) {
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DRAFT, domainAccountTypeStatusToProto(accounttype.StatusDraft))
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE, domainAccountTypeStatusToProto(accounttype.StatusActive))
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DEPRECATED, domainAccountTypeStatusToProto(accounttype.StatusDeprecated))
	assert.Equal(t, pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_UNSPECIFIED, domainAccountTypeStatusToProto(accounttype.Status("UNKNOWN")))
}

func TestAccountTypeToProto_Nil(t *testing.T) {
	assert.Nil(t, accountTypeToProto(nil))
}

func TestAccountTypeToProto_MinimalFields(t *testing.T) {
	now := time.Now()
	def := &accounttype.Definition{
		ID:        uuid.New(),
		Code:      "BASIC",
		Version:   1,
		Status:    accounttype.StatusDraft,
		CreatedAt: now,
		UpdatedAt: now,
	}
	proto := accountTypeToProto(def)
	require.NotNil(t, proto)
	assert.Equal(t, "BASIC", proto.Code)
	assert.Empty(t, proto.DefaultConversionMethodId)
	assert.Empty(t, proto.SuccessorId)
	assert.Nil(t, proto.ActivatedAt)
	assert.Nil(t, proto.DeprecatedAt)
	assert.Empty(t, proto.ValuationMethods)
}

func TestValuationMethodToProto_WithSuccessor(t *testing.T) {
	successorID := uuid.New()
	vmt := &accounttype.ValuationMethodTemplate{
		ID:                     uuid.New(),
		AccountTypeID:          uuid.New(),
		InputInstrument:        "GBP",
		ValuationMethodID:      uuid.New(),
		ValuationMethodVersion: 1,
		Status:                 accounttype.StatusActive,
		SuccessorID:            &successorID,
	}
	proto := valuationMethodToProto(vmt)
	assert.Equal(t, successorID.String(), proto.SuccessorId)
}

// --- validateInstrumentRefs with nil instrumentRegistry ---

func TestValidateInstrumentRefs_NilInstrumentRegistry(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	errs := svc.validateInstrumentRefs(context.Background(), &pb.AccountTypeDefinition{
		InstrumentCode: "GBP",
	})
	assert.Empty(t, errs)
}

// --- instrumentSuggestions ---

func TestInstrumentSuggestions_NilRegistry(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	suggestions := svc.instrumentSuggestions(context.Background(), "GBP")
	assert.Nil(t, suggestions)
}

// --- CreateDraft error paths ---

func TestAccountTypeService_CreateDraft_InvalidConversionMethodPair(t *testing.T) {
	svc, _ := newTestAccountTypeService(t)
	_, err := svc.CreateDraft(context.Background(), &pb.CreateDraftRequest{
		Code:                           "TEST",
		NormalBalance:                  pb.NormalBalance_NORMAL_BALANCE_DEBIT,
		BehaviorClass:                  pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER,
		DefaultConversionMethodId:      "bad-uuid",
		DefaultConversionMethodVersion: 1,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestAccountTypeService_CreateDraft_RegistryError(t *testing.T) {
	svc, reg := newTestAccountTypeService(t)
	reg.createErr = accounttype.ErrActiveCodeExists

	_, err := svc.CreateDraft(context.Background(), &pb.CreateDraftRequest{
		Code:          "DUPLICATE",
		NormalBalance: pb.NormalBalance_NORMAL_BALANCE_DEBIT,
		BehaviorClass: pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

// --- Utility functions ---

func TestToRawJSON(t *testing.T) {
	assert.Nil(t, toRawJSON(""))
	assert.Equal(t, `{"key":"val"}`, string(toRawJSON(`{"key":"val"}`)))
}

func TestStringMapToAnyMap(t *testing.T) {
	assert.Nil(t, stringMapToAnyMap(nil))
	result := stringMapToAnyMap(map[string]string{"a": "1", "b": "2"})
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "2", result["b"])
}

func TestAnyMapToStringMap(t *testing.T) {
	assert.Nil(t, anyMapToStringMap(nil))
	result := anyMapToStringMap(map[string]any{"a": 1, "b": "text"})
	assert.Equal(t, "1", result["a"])
	assert.Equal(t, "text", result["b"])
}

// --- validateAttributeSchema ---

func TestValidateAttributeSchema_Empty(t *testing.T) {
	valid, errs := validateAttributeSchema("")
	assert.False(t, valid)
	assert.Nil(t, errs)
}

func TestValidateAttributeSchema_InvalidJSON(t *testing.T) {
	valid, errs := validateAttributeSchema("not json")
	assert.False(t, valid)
	require.Len(t, errs, 1)
	assert.Equal(t, "INVALID_JSON", errs[0].ErrorCode)
}

func TestValidateAttributeSchema_ValidSchema(t *testing.T) {
	valid, errs := validateAttributeSchema(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	assert.True(t, valid)
	assert.Empty(t, errs)
}

// --- validateAttrsAgainstSchema ---

func TestValidateAttrsAgainstSchema_SchemaInvalid(t *testing.T) {
	errs := validateAttrsAgainstSchema(&pb.AccountTypeDefinition{
		Attributes: map[string]string{"key": "val"},
	}, false)
	assert.Nil(t, errs)
}

func TestValidateAttrsAgainstSchema_NoAttributes(t *testing.T) {
	errs := validateAttrsAgainstSchema(&pb.AccountTypeDefinition{}, true)
	assert.Nil(t, errs)
}

// --- findSuggestions edge case ---

func TestFindSuggestions_ExactMatchExcluded(t *testing.T) {
	// Exact match (distance 0) is excluded, but close matches are included
	result := findSuggestions("GBP", []string{"GBP"})
	assert.Empty(t, result) // only the exact match, no close alternatives

	// When there are close alternatives too, those are returned
	result = findSuggestions("GBP", []string{"GBP", "GBQ"})
	assert.NotContains(t, result, "GBP") // exact match excluded
	assert.Contains(t, result, "GBQ")    // close alternative included
}
