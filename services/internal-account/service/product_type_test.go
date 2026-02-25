package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/cel-go/cel"
	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testTenantIDForPT = "test_tenant_product_type"

// mockAccountTypeLoader implements cache.AccountTypeLoader for testing.
type mockAccountTypeLoader struct {
	definitions map[string]*accounttype.Definition
}

func (m *mockAccountTypeLoader) LoadAccountType(_ context.Context, code string) (*accounttype.Definition, error) {
	def, ok := m.definitions[code]
	if !ok {
		return nil, fmt.Errorf("account type not found: %s", code)
	}
	return def, nil
}

func (m *mockAccountTypeLoader) ListActiveAccountTypes(_ context.Context) ([]*accounttype.Definition, error) {
	defs := make([]*accounttype.Definition, 0, len(m.definitions))
	for _, def := range m.definitions {
		defs = append(defs, def)
	}
	return defs, nil
}

// mockCELCompiler implements cache.AccountTypeCELCompiler for testing.
type mockCELCompiler struct{}

func (m *mockCELCompiler) CompileValidation(_ string) (cel.Program, error) {
	return nil, nil
}

func (m *mockCELCompiler) CompileBucketKey(_ string) (cel.Program, error) {
	return nil, nil
}

func (m *mockCELCompiler) CompileEligibility(_ string) (cel.Program, error) {
	return nil, nil
}

// newTestCacheWithDefinitions creates a LocalAccountTypeCache with the given definitions.
func newTestCacheWithDefinitions(defs map[string]*accounttype.Definition) *cache.LocalAccountTypeCache {
	loader := &mockAccountTypeLoader{definitions: defs}
	compiler := &mockCELCompiler{}
	return cache.NewLocalAccountTypeCache(loader, compiler)
}

// ptTestCtx returns a context with the test tenant ID.
func ptTestCtx() context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(testTenantIDForPT))
}

// --- Tests: product_type_code required ---

func TestInitiate_ProductTypeCode_WithCache_Accepted(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"CUSTOM_PRODUCT": {
			Code:           "CUSTOM_PRODUCT",
			Version:        1,
			BehaviorClass:  accounttype.BehaviorClassClearing,
			EligibilityCEL: "true",
			Status:         accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := ptTestCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-GBP-OVERRIDE",
		Name:            "GBP Clearing Override",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
		ProductTypeCode: "CUSTOM_PRODUCT",
	})
	require.NoError(t, err)

	assert.Equal(t, "CUSTOM_PRODUCT", resp.Facility.ProductTypeCode)
}

func TestInitiate_ProductTypeCode_NoCacheReturnsError(t *testing.T) {
	// When product_type_code is provided but cache is not configured, return FailedPrecondition
	repo := newMockRepository()
	svc, err := NewService(repo)
	require.NoError(t, err)

	ctx := context.Background()
	_, err = svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-GBP",
		Name:            "GBP Clearing",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
		ProductTypeCode: "CUSTOM_PRODUCT",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "product type resolution not available")
}

// --- Tests: BehaviorClass gating with cache ---

func TestInitiate_WithCache_ClearingBehaviorClassAccepted(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"CLEARING_GBP": {
			Code:           "CLEARING_GBP",
			Version:        1,
			BehaviorClass:  accounttype.BehaviorClassClearing,
			EligibilityCEL: "true",
			Status:         accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := ptTestCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-GBP",
		Name:            "GBP Clearing",
		ProductTypeCode: "CLEARING_GBP",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "GBP",
	})
	require.NoError(t, err)
	assert.Equal(t, "CLEARING_GBP", resp.Facility.ProductTypeCode)
	assert.Equal(t, int32(1), resp.Facility.ProductTypeVersion)
}

func TestInitiate_WithCache_CustomerBehaviorClassRejected(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"CUSTOMER_SAVINGS": {
			Code:          "CUSTOMER_SAVINGS",
			Version:       1,
			BehaviorClass: accounttype.BehaviorClassCustomer,
			Status:        accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := ptTestCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CUST-001",
		Name:            "Customer Account",
		ProductTypeCode: "CUSTOMER_SAVINGS",
		InstrumentCode:  "GBP",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "CUSTOMER")
}

func TestInitiate_WithCache_InventoryBehaviorClassAccepted(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"INVENTORY_CARBON": {
			Code:           "INVENTORY_CARBON",
			Version:        2,
			BehaviorClass:  accounttype.BehaviorClassInventory,
			EligibilityCEL: "true",
			Status:         accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := ptTestCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "INV-001",
		Name:            "Carbon Inventory",
		ProductTypeCode: "INVENTORY_CARBON",
		InstrumentCode:  "CARBON",
	})
	require.NoError(t, err)
	assert.Equal(t, "INVENTORY_CARBON", resp.Facility.ProductTypeCode)
	assert.Equal(t, int32(2), resp.Facility.ProductTypeVersion)
}

func TestInitiate_WithCache_AllInternalBehaviorClasses(t *testing.T) {
	behaviorClasses := []struct {
		class accounttype.BehaviorClass
		code  string
	}{
		{accounttype.BehaviorClassClearing, "BC_CLEARING"},
		{accounttype.BehaviorClassNostro, "BC_NOSTRO"},
		{accounttype.BehaviorClassVostro, "BC_VOSTRO"},
		{accounttype.BehaviorClassHolding, "BC_HOLDING"},
		{accounttype.BehaviorClassSuspense, "BC_SUSPENSE"},
		{accounttype.BehaviorClassRevenue, "BC_REVENUE"},
		{accounttype.BehaviorClassExpense, "BC_EXPENSE"},
		{accounttype.BehaviorClassInventory, "BC_INVENTORY"},
	}

	for _, bc := range behaviorClasses {
		t.Run(string(bc.class), func(t *testing.T) {
			defs := map[string]*accounttype.Definition{
				bc.code: {
					Code:           bc.code,
					Version:        1,
					BehaviorClass:  bc.class,
					EligibilityCEL: "true",
					Status:         accounttype.StatusActive,
				},
			}
			testCache := newTestCacheWithDefinitions(defs)

			repo := newMockRepository()
			svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
			require.NoError(t, err)

			ctx := ptTestCtx()
			req := &pb.InitiateInternalAccountRequest{
				AccountCode:     fmt.Sprintf("ACC-%s", bc.code),
				Name:            fmt.Sprintf("Test %s", bc.class),
				ProductTypeCode: bc.code,
				InstrumentCode:  "USD",
			}

			// Add clearing purpose for CLEARING behavior
			if bc.class == accounttype.BehaviorClassClearing {
				req.ClearingPurpose = pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL
			}
			// Add correspondent for NOSTRO/VOSTRO
			if bc.class == accounttype.BehaviorClassNostro || bc.class == accounttype.BehaviorClassVostro {
				req.CorrespondentDetails = &pb.CorrespondentBankDetails{
					BankId:             "BANK001",
					BankName:           "Test Bank",
					ExternalAccountRef: "REF-123",
				}
			}

			resp, err := svc.InitiateInternalAccount(ctx, req)
			require.NoError(t, err, "BehaviorClass %s should be accepted", bc.class)
			assert.Equal(t, bc.code, resp.Facility.ProductTypeCode)
		})
	}
}

// --- Tests: Product type not found ---

func TestInitiate_WithCache_ProductTypeNotFound(t *testing.T) {
	defs := map[string]*accounttype.Definition{} // empty
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := ptTestCtx()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "MISSING-001",
		Name:            "Missing Product Type",
		ProductTypeCode: "NONEXISTENT",
		InstrumentCode:  "USD",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "product type not found")
}

// --- Tests: Tenant context required ---

func TestInitiate_WithCache_RequiresTenantContext(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"CLEARING_USD": {
			Code:          "CLEARING_USD",
			Version:       1,
			BehaviorClass: accounttype.BehaviorClassClearing,
			Status:        accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	// Context without tenant
	ctx := context.Background()
	resp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "CLR-USD",
		Name:            "USD Clearing",
		ProductTypeCode: "CLEARING_USD",
		ClearingPurpose: pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL,
		InstrumentCode:  "USD",
	})
	assert.Error(t, err)
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "tenant context required")
}

// --- Tests: Product type immutability (product_type_code/version stored) ---

func TestInitiate_ProductTypeFieldsPersistedAndReturned(t *testing.T) {
	defs := map[string]*accounttype.Definition{
		"HOLDING_KWH": {
			Code:           "HOLDING_KWH",
			Version:        5,
			BehaviorClass:  accounttype.BehaviorClassHolding,
			EligibilityCEL: "true",
			Status:         accounttype.StatusActive,
		},
	}
	testCache := newTestCacheWithDefinitions(defs)

	repo := newMockRepository()
	svc, err := NewServiceFull(repo, nil, nil, nil, nil, WithAccountTypeCache(testCache))
	require.NoError(t, err)

	ctx := ptTestCtx()
	createResp, err := svc.InitiateInternalAccount(ctx, &pb.InitiateInternalAccountRequest{
		AccountCode:     "HOLD-KWH-001",
		Name:            "Energy Holding",
		ProductTypeCode: "HOLDING_KWH",
		InstrumentCode:  "KWH",
	})
	require.NoError(t, err)
	assert.Equal(t, "HOLDING_KWH", createResp.Facility.ProductTypeCode)
	assert.Equal(t, int32(5), createResp.Facility.ProductTypeVersion)

	// Retrieve the account and verify fields persist
	retrieveResp, err := svc.RetrieveInternalAccount(ctx, &pb.RetrieveInternalAccountRequest{
		AccountId: "HOLD-KWH-001",
	})
	require.NoError(t, err)
	assert.Equal(t, "HOLDING_KWH", retrieveResp.Facility.ProductTypeCode)
	assert.Equal(t, int32(5), retrieveResp.Facility.ProductTypeVersion)
}
