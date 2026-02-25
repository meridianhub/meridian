package service

import (
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProtoToAccountStatus_Unknown(t *testing.T) {
	// Unknown enum values should return an error
	_, err := protoToAccountStatus(pb.InternalAccountStatus(9999))
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrUnknownAccountStatus)
}

func TestProtoToAccountStatus_Valid(t *testing.T) {
	tests := []struct {
		name     string
		proto    pb.InternalAccountStatus
		expected domain.AccountStatus
	}{
		{"ACTIVE", pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, domain.AccountStatusActive},
		{"SUSPENDED", pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED, domain.AccountStatusSuspended},
		{"CLOSED", pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED, domain.AccountStatusClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoToAccountStatus(tt.proto)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProtoToAccountStatus_Unspecified(t *testing.T) {
	_, err := protoToAccountStatus(pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrUnspecifiedEnum)
}

func TestAccountStatusToProto_RoundTrip(t *testing.T) {
	tests := []domain.AccountStatus{
		domain.AccountStatusActive,
		domain.AccountStatusSuspended,
		domain.AccountStatusClosed,
	}

	for _, as := range tests {
		t.Run(string(as), func(t *testing.T) {
			proto := accountStatusToProto(as)
			result, err := protoToAccountStatus(proto)
			require.NoError(t, err)
			assert.Equal(t, as, result)
		})
	}
}

func TestCorrespondentTypeFromAccountType(t *testing.T) {
	tests := []struct {
		name     string
		input    domain.AccountType
		expected pb.CorrespondentType
	}{
		{"NOSTRO", domain.AccountTypeNostro, pb.CorrespondentType_CORRESPONDENT_TYPE_NOSTRO},
		{"VOSTRO", domain.AccountTypeVostro, pb.CorrespondentType_CORRESPONDENT_TYPE_VOSTRO},
		{"CLEARING", domain.AccountTypeClearing, pb.CorrespondentType_CORRESPONDENT_TYPE_UNSPECIFIED},
		{"HOLDING", domain.AccountTypeHolding, pb.CorrespondentType_CORRESPONDENT_TYPE_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := correspondentTypeFromAccountType(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestMapDomainErrorToGRPC(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode codes.Code
	}{
		{"AccountNotFound", domain.ErrAccountNotFound, codes.NotFound},
		{"AccountClosed", domain.ErrAccountClosed, codes.FailedPrecondition},
		{"AccountSuspended", domain.ErrAccountSuspended, codes.FailedPrecondition},
		{"InvalidAccountType", domain.ErrInvalidAccountType, codes.InvalidArgument},
		{"InvalidStatusTransition", domain.ErrInvalidStatusTransition, codes.FailedPrecondition},
		{"CorrespondentRequired", domain.ErrCorrespondentRequired, codes.InvalidArgument},
		{"CorrespondentNotAllowed", domain.ErrCorrespondentNotAllowed, codes.InvalidArgument},
		{"DuplicateAccountCode", domain.ErrDuplicateAccountCode, codes.AlreadyExists},
		{"VersionMismatch", domain.ErrVersionMismatch, codes.Aborted},
		{"AccountIDRequired", domain.ErrAccountIDRequired, codes.InvalidArgument},
		{"AccountCodeRequired", domain.ErrAccountCodeRequired, codes.InvalidArgument},
		{"NameRequired", domain.ErrNameRequired, codes.InvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapDomainErrorToGRPC(tt.err)
			st, ok := status.FromError(result)
			require.True(t, ok)
			assert.Equal(t, tt.expectedCode, st.Code())
		})
	}
}

func TestToProtoFacility(t *testing.T) {
	// Create a domain account
	account, err := domain.NewInternalAccount(
		"IBA-001",
		"CLR-001",
		"Test Clearing Account",
		domain.AccountTypeClearing,
		domain.ClearingPurposeDeposit,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	facility := toProtoFacility(account)

	assert.Equal(t, "IBA-001", facility.AccountId)
	assert.Equal(t, "CLR-001", facility.AccountCode)
	assert.Equal(t, "Test Clearing Account", facility.Name)
	assert.Equal(t, "CLEARING", facility.BehaviorClass)
	assert.Equal(t, pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT, facility.ClearingPurpose)
	assert.Equal(t, pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE, facility.AccountStatus)
	assert.Equal(t, "USD", facility.InstrumentCode)
	assert.Nil(t, facility.CorrespondentDetails)
	assert.NotNil(t, facility.CreatedAt)
	assert.NotNil(t, facility.UpdatedAt)
	assert.Equal(t, int32(1), facility.Version)
}

func TestToProtoFacility_WithCorrespondent(t *testing.T) {
	// Create a NOSTRO account with correspondent
	account, err := domain.NewInternalAccount(
		"IBA-002",
		"NOSTRO-USD-HSBC",
		"HSBC USD Nostro",
		domain.AccountTypeNostro,
		domain.ClearingPurposeUnspecified,
		"USD",
		"CURRENCY",
	)
	require.NoError(t, err)

	// Add correspondent details
	correspondent, err := domain.NewCorrespondentDetailsWithOptions(
		"HSBC001",
		"HSBC Bank",
		"12345678",
		"HSBCGB2L",
		nil,
	)
	require.NoError(t, err)

	account, err = account.UpdateCorrespondent(correspondent)
	require.NoError(t, err)

	facility := toProtoFacility(account)

	assert.NotNil(t, facility.CorrespondentDetails)
	assert.Equal(t, "HSBC001", facility.CorrespondentDetails.BankId)
	assert.Equal(t, "HSBC Bank", facility.CorrespondentDetails.BankName)
	assert.Equal(t, "12345678", facility.CorrespondentDetails.ExternalAccountRef)
	assert.Equal(t, "HSBCGB2L", facility.CorrespondentDetails.SwiftCode)
	assert.Equal(t, pb.CorrespondentType_CORRESPONDENT_TYPE_NOSTRO, facility.CorrespondentDetails.CorrespondentType)
}

func TestProtoToClearingPurpose(t *testing.T) {
	testCases := []struct {
		name     string
		proto    pb.ClearingPurpose
		expected domain.ClearingPurpose
		wantErr  bool
	}{
		{"Unspecified", pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED, domain.ClearingPurposeUnspecified, false},
		{"Deposit", pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT, domain.ClearingPurposeDeposit, false},
		{"Withdrawal", pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL, domain.ClearingPurposeWithdrawal, false},
		{"Settlement", pb.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT, domain.ClearingPurposeSettlement, false},
		{"General", pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL, domain.ClearingPurposeGeneral, false},
		{"Unknown", pb.ClearingPurpose(999), "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := protoToClearingPurpose(tc.proto)
			if tc.wantErr {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrUnknownClearingPurpose)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestClearingPurposeToProto(t *testing.T) {
	testCases := []struct {
		name     string
		domain   domain.ClearingPurpose
		expected pb.ClearingPurpose
	}{
		{"Unspecified", domain.ClearingPurposeUnspecified, pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED},
		{"Deposit", domain.ClearingPurposeDeposit, pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT},
		{"Withdrawal", domain.ClearingPurposeWithdrawal, pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL},
		{"Settlement", domain.ClearingPurposeSettlement, pb.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT},
		{"General", domain.ClearingPurposeGeneral, pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL},
		{"Unknown", domain.ClearingPurpose("UNKNOWN"), pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := clearingPurposeToProto(tc.domain)
			assert.Equal(t, tc.expected, result)
		})
	}
}
