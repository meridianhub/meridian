package provisioning

import (
	"context"
	"errors"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Test sentinel errors for simulating failures.
var (
	errDatabaseUnavailable = errors.New("database unavailable")
	errTimeout             = errors.New("timeout")
	errSomeError           = errors.New("some error")
)

// mockService implements InternalBankAccountService for testing.
type mockService struct {
	// createdAccounts tracks all InitiateInternalBankAccount calls
	createdAccounts []*pb.InitiateInternalBankAccountRequest

	// existingCodes simulates accounts that already exist
	existingCodes map[string]bool

	// failOnCodes simulates failures for specific account codes
	failOnCodes map[string]error
}

func newMockService() *mockService {
	return &mockService{
		createdAccounts: make([]*pb.InitiateInternalBankAccountRequest, 0),
		existingCodes:   make(map[string]bool),
		failOnCodes:     make(map[string]error),
	}
}

func (m *mockService) InitiateInternalBankAccount(_ context.Context, req *pb.InitiateInternalBankAccountRequest) (*pb.InitiateInternalBankAccountResponse, error) {
	// Check for simulated failure
	if err, ok := m.failOnCodes[req.AccountCode]; ok {
		return nil, err
	}

	// Check if account already exists
	if m.existingCodes[req.AccountCode] {
		return nil, status.Error(codes.AlreadyExists, "account code already exists")
	}

	// Record the creation
	m.createdAccounts = append(m.createdAccounts, req)
	m.existingCodes[req.AccountCode] = true

	return &pb.InitiateInternalBankAccountResponse{
		AccountId: "IBA-test-" + req.AccountCode,
		Facility: &pb.InternalBankAccountFacility{
			AccountId:      "IBA-test-" + req.AccountCode,
			AccountCode:    req.AccountCode,
			Name:           req.Name,
			AccountType:    req.AccountType,
			AccountStatus:  pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE,
			InstrumentCode: req.InstrumentCode,
		},
	}, nil
}

func TestAccountTemplate_Validation(t *testing.T) {
	// Verify all templates have required fields
	for i, template := range DefaultAccounts {
		assert.NotEmpty(t, template.Code, "template %d: Code required", i)
		assert.NotEmpty(t, template.Name, "template %d: Name required", i)
		assert.NotEqual(t, pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_UNSPECIFIED, template.Type,
			"template %d: Type must be specified", i)
		assert.NotEmpty(t, template.InstrumentCode, "template %d: InstrumentCode required", i)
		assert.NotEmpty(t, template.Dimension, "template %d: Dimension required", i)
	}
}

func TestDefaultAccounts_Count(t *testing.T) {
	// Verify expected number of default accounts
	// 6 clearing + 3 revenue + 1 expense + 1 suspense = 11
	assert.Equal(t, 11, len(DefaultAccounts), "expected 11 default accounts")
}

func TestDefaultAccounts_Uniqueness(t *testing.T) {
	// Verify all account codes are unique
	codes := make(map[string]bool)
	for _, template := range DefaultAccounts {
		if codes[template.Code] {
			t.Errorf("duplicate account code: %s", template.Code)
		}
		codes[template.Code] = true
	}
}

func TestDefaultAccounts_CoveringRequiredTypes(t *testing.T) {
	// Track which clearing accounts we have
	clearingAccounts := make(map[string]bool)

	// Required clearing accounts
	requiredClearing := []string{
		"CLR-GBP-DEPOSIT", "CLR-GBP-WITHDRAW",
		"CLR-USD-DEPOSIT", "CLR-USD-WITHDRAW",
		"CLR-EUR-DEPOSIT", "CLR-EUR-WITHDRAW",
	}

	for _, template := range DefaultAccounts {
		clearingAccounts[template.Code] = true
	}

	for _, required := range requiredClearing {
		assert.True(t, clearingAccounts[required], "missing required clearing account: %s", required)
	}
}

func TestDefaultAccounts_HasRequiredAccountTypes(t *testing.T) {
	typeCount := make(map[pb.InternalAccountType]int)
	for _, template := range DefaultAccounts {
		typeCount[template.Type]++
	}

	// Verify we have at least one of each required type
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING], 0, "missing CLEARING accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE], 0, "missing REVENUE accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE], 0, "missing EXPENSE accounts")
	assert.Greater(t, typeCount[pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE], 0, "missing SUSPENSE accounts")
}

func TestProvisionDefaultAccounts_NewTenant(t *testing.T) {
	mock := newMockService()
	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	result, err := provisioner.ProvisionDefaultAccounts(context.Background(), tenantID)

	require.NoError(t, err)
	assert.Equal(t, tenantID, result.TenantID)
	assert.Equal(t, 11, result.Created, "should create all 11 accounts")
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.Errors)

	// Verify all accounts were created with correct tenant-scoped idempotency keys
	for _, created := range mock.createdAccounts {
		assert.NotNil(t, created.IdempotencyKey)
		assert.Contains(t, created.IdempotencyKey.Key, string(tenantID))
		assert.Contains(t, created.IdempotencyKey.Key, created.AccountCode)
	}
}

func TestProvisionDefaultAccounts_Idempotent(t *testing.T) {
	mock := newMockService()
	// Pre-populate some existing accounts
	mock.existingCodes["CLR-GBP-DEPOSIT"] = true
	mock.existingCodes["REV-TRANSACTION-FEE"] = true
	mock.existingCodes["SUS-GENERAL"] = true

	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	result, err := provisioner.ProvisionDefaultAccounts(context.Background(), tenantID)

	require.NoError(t, err)
	assert.Equal(t, 8, result.Created, "should create 8 new accounts")
	assert.Equal(t, 3, result.Skipped, "should skip 3 existing accounts")
	assert.Equal(t, 0, result.Failed)
	assert.Empty(t, result.Errors)
}

func TestProvisionDefaultAccounts_PartialFailure(t *testing.T) {
	mock := newMockService()
	// Simulate failure for specific accounts
	mock.failOnCodes["REV-TRANSACTION-FEE"] = errDatabaseUnavailable
	mock.failOnCodes["EXP-PAYMENT-PROCESSING"] = errTimeout

	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_test_bank")

	result, err := provisioner.ProvisionDefaultAccounts(context.Background(), tenantID)

	// Provisioning continues despite failures
	require.NoError(t, err)
	assert.Equal(t, 9, result.Created, "should create 9 accounts")
	assert.Equal(t, 0, result.Skipped)
	assert.Equal(t, 2, result.Failed, "2 accounts should fail")
	assert.Len(t, result.Errors, 2)
}

func TestProvisionDefaultAccounts_NilService(t *testing.T) {
	provisioner := NewProvisioner(nil, nil)
	tenantID := tenant.TenantID("org_test_bank")

	result, err := provisioner.ProvisionDefaultAccounts(context.Background(), tenantID)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "not configured")
}

func TestProvisionFromTemplates_CustomTemplates(t *testing.T) {
	mock := newMockService()
	provisioner := NewProvisioner(mock, nil)
	tenantID := tenant.TenantID("org_energy_company")

	// Custom templates for energy company
	energyTemplates := []AccountTemplate{
		{
			Code:           "CLR-KWH-DELIVERY",
			Name:           "KWH Delivery Clearing",
			Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING,
			InstrumentCode: "KWH",
			Dimension:      DimensionEnergy,
			Description:    "Clearing account for energy delivery",
		},
		{
			Code:           "REV-ENERGY-SALES",
			Name:           "Energy Sales Revenue",
			Type:           pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE,
			InstrumentCode: "GBP",
			Dimension:      DimensionCurrency,
			Description:    "Revenue from energy sales",
		},
	}

	result, err := provisioner.ProvisionFromTemplates(context.Background(), tenantID, energyTemplates)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Created)
	assert.Equal(t, 0, result.Skipped)

	// Verify the energy account was created
	var foundEnergy bool
	for _, created := range mock.createdAccounts {
		if created.AccountCode == "CLR-KWH-DELIVERY" {
			foundEnergy = true
			assert.Equal(t, "KWH", created.InstrumentCode)
		}
	}
	assert.True(t, foundEnergy, "energy clearing account should be created")
}

func TestIsDuplicateError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "AlreadyExists status",
			err:      status.Error(codes.AlreadyExists, "account exists"),
			expected: true,
		},
		{
			name:     "NotFound status",
			err:      status.Error(codes.NotFound, "not found"),
			expected: false,
		},
		{
			name:     "Internal status",
			err:      status.Error(codes.Internal, "internal error"),
			expected: false,
		},
		{
			name:     "plain error",
			err:      errSomeError,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isDuplicateError(tc.err))
		})
	}
}

func TestDefaultAccounts_ValidDimensions(t *testing.T) {
	// Verify all dimensions are valid per database constraint
	validDimensions := map[string]bool{
		DimensionCurrency: true,
		DimensionEnergy:   true,
		DimensionMass:     true,
		DimensionVolume:   true,
		DimensionTime:     true,
		DimensionCompute:  true,
		DimensionCarbon:   true,
		DimensionData:     true,
		DimensionCount:    true,
	}

	for _, template := range DefaultAccounts {
		assert.True(t, validDimensions[template.Dimension],
			"template %s has invalid dimension: %s", template.Code, template.Dimension)
	}
}
