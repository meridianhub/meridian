// Package service provides integration tests for Product Type validation
// during account creation in the CurrentAccount service.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/santhosh-tekuri/jsonschema/v5"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	celutil "github.com/meridianhub/meridian/services/reference-data/cel"
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// mockAccountTypeCache implements AccountTypeCache for testing
type mockAccountTypeCache struct {
	entries map[string]*CachedAccountType
	err     error
}

func (m *mockAccountTypeCache) GetOrLoad(_ context.Context, _ tenant.TenantID, code string) (*CachedAccountType, error) {
	if m.err != nil {
		return nil, m.err
	}
	entry, ok := m.entries[code]
	if !ok {
		return nil, fmt.Errorf("account type %s not found", code)
	}
	return entry, nil
}

// compileTestEligibilityProgram compiles a CEL eligibility expression for testing.
func compileTestEligibilityProgram(t *testing.T, expression string) cel.Program {
	t.Helper()
	compiler, err := celutil.NewCompiler()
	require.NoError(t, err)
	prg, err := compiler.CompileEligibility(expression)
	require.NoError(t, err)
	return prg
}

// compileTestSchema compiles a JSON Schema for testing.
func compileTestSchema(t *testing.T, schemaJSON string) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	err := compiler.AddResource("schema.json", jsonReader(schemaJSON))
	require.NoError(t, err)
	schema, err := compiler.Compile("schema.json")
	require.NoError(t, err)
	return schema
}

func jsonReader(s string) *jsonStringReader {
	return &jsonStringReader{s: s, i: 0}
}

type jsonStringReader struct {
	s string
	i int
}

func (r *jsonStringReader) Read(p []byte) (n int, err error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n = copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}

const productTypeTestTenantID = "test_product_type_tenant"

func setupProductTypeTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.CurrentAccountEntity{},
		&vf.Entity{},
	})

	tid := tenant.TenantID(productTypeTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// AutoMigrate tables in tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.AutoMigrate(&persistence.CurrentAccountEntity{}, &vf.Entity{})
	require.NoError(t, err)

	// Create the partial unique index required by UpsertFeature's ON CONFLICT clause.
	// AutoMigrate only creates the table structure; partial indexes must be created manually.
	err = db.Exec(fmt.Sprintf(`CREATE UNIQUE INDEX IF NOT EXISTS idx_valuation_feature_account_instrument_active
		ON %s.valuation_features (account_id, instrument_code)
		WHERE lifecycle_status = 'ACTIVE' AND valid_to = '9999-12-31 23:59:59+00'`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tid)
	return db, ctx, cleanup
}

func newTestDefinition(code string, behaviorClass accounttype.BehaviorClass) *accounttype.Definition {
	return &accounttype.Definition{
		ID:             uuid.New(),
		Code:           code,
		Version:        1,
		DisplayName:    "Test " + code,
		BehaviorClass:  behaviorClass,
		InstrumentCode: "GBP",
		Status:         accounttype.StatusActive,
	}
}

// --- Test Cases ---

// TestInitiateCurrentAccount_WithProductType_Success verifies account creation
// with a valid CUSTOMER product type code.
func TestInitiateCurrentAccount_WithProductType_Success(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	def := newTestDefinition("CURRENT_GBP", accounttype.BehaviorClassCustomer)
	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{
			"CURRENT_GBP": {Definition: def},
		},
	}

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:             repo,
		partyClient:      mockParty,
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "CURRENT_GBP",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.AccountId)
	assert.Equal(t, "CURRENT_GBP", resp.Facility.ProductTypeCode)
	assert.Equal(t, int32(1), resp.Facility.ProductTypeVersion)
}

// TestInitiateCurrentAccount_WithProductType_VersionOverride verifies that
// the requested product_type_version overrides the latest version.
func TestInitiateCurrentAccount_WithProductType_VersionOverride(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	def := newTestDefinition("CURRENT_GBP", accounttype.BehaviorClassCustomer)
	def.Version = 3 // latest version is 3
	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{
			"CURRENT_GBP": {Definition: def},
		},
	}

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:             repo,
		partyClient:      mockParty,
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	requestedVersion := int32(2)
	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "CURRENT_GBP",
		ProductTypeVersion: &requestedVersion,
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(2), resp.Facility.ProductTypeVersion)
}

// TestInitiateCurrentAccount_WithProductType_NotFound verifies that account creation
// fails when the product type code is not found in the cache.
func TestInitiateCurrentAccount_WithProductType_NotFound(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{}, // empty - no definitions
	}

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:             repo,
		partyClient:      mockParty,
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "NONEXISTENT",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "product type not found")
}

// TestInitiateCurrentAccount_WithProductType_NonCustomerBehaviorClass verifies
// that account creation fails when the product type has a non-CUSTOMER behavior class.
func TestInitiateCurrentAccount_WithProductType_NonCustomerBehaviorClass(t *testing.T) {
	tests := []struct {
		name          string
		behaviorClass accounttype.BehaviorClass
	}{
		{"CLEARING", accounttype.BehaviorClassClearing},
		{"NOSTRO", accounttype.BehaviorClassNostro},
		{"SUSPENSE", accounttype.BehaviorClassSuspense},
		{"REVENUE", accounttype.BehaviorClassRevenue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, ctx, cleanup := setupProductTypeTestDB(t)
			defer cleanup()

			repo := persistence.NewRepository(db)

			def := newTestDefinition("INTERNAL_"+tt.name, tt.behaviorClass)
			cache := &mockAccountTypeCache{
				entries: map[string]*CachedAccountType{
					"INTERNAL_" + tt.name: {Definition: def},
				},
			}

			mockParty := &mockPartyClient{
				partyExists: true,
				partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
			}

			svc := &Service{
				repo:             repo,
				partyClient:      mockParty,
				accountTypeCache: cache,
				logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
			}

			req := &pb.InitiateCurrentAccountRequest{
				ExternalIdentifier: "GB82WEST12345698765432",
				PartyId:            newTestPartyID(),
				InstrumentCode:     "GBP",
				ProductTypeCode:    "INTERNAL_" + tt.name,
			}
			resp, err := svc.InitiateCurrentAccount(ctx, req)

			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
			assert.Contains(t, st.Message(), "behavior class")
			assert.Contains(t, st.Message(), "expected CUSTOMER")
		})
	}
}

// TestInitiateCurrentAccount_WithProductType_CELEligibility verifies
// CEL eligibility evaluation with party context.
func TestInitiateCurrentAccount_WithProductType_CELEligibility(t *testing.T) {
	t.Run("eligible party passes", func(t *testing.T) {
		db, ctx, cleanup := setupProductTypeTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)

		// CEL expression that requires party type to be PERSON
		prg := compileTestEligibilityProgram(t, `party.type == "PERSON"`)

		def := newTestDefinition("PERSONAL_CURRENT", accounttype.BehaviorClassCustomer)
		def.EligibilityCEL = `party.type == "PERSON"`
		cache := &mockAccountTypeCache{
			entries: map[string]*CachedAccountType{
				"PERSONAL_CURRENT": {
					Definition:         def,
					EligibilityProgram: prg,
				},
			},
		}

		mockParty := &mockPartyClient{
			partyExists: true,
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		}
		// Override GetParty to return PERSON type
		mockParty.partyTypeOverride = partyv1.PartyType_PARTY_TYPE_PERSON

		svc := &Service{
			repo:             repo,
			partyClient:      mockParty,
			accountTypeCache: cache,
			logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
		}

		req := &pb.InitiateCurrentAccountRequest{
			ExternalIdentifier: "GB82WEST12345698765432",
			PartyId:            newTestPartyID(),
			InstrumentCode:     "GBP",
			ProductTypeCode:    "PERSONAL_CURRENT",
		}
		resp, err := svc.InitiateCurrentAccount(ctx, req)

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "PERSONAL_CURRENT", resp.Facility.ProductTypeCode)
	})

	t.Run("ineligible party rejected", func(t *testing.T) {
		db, ctx, cleanup := setupProductTypeTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)

		// CEL expression that requires party type to be PERSON
		prg := compileTestEligibilityProgram(t, `party.type == "PERSON"`)

		def := newTestDefinition("PERSONAL_CURRENT", accounttype.BehaviorClassCustomer)
		def.EligibilityCEL = `party.type == "PERSON"`
		cache := &mockAccountTypeCache{
			entries: map[string]*CachedAccountType{
				"PERSONAL_CURRENT": {
					Definition:         def,
					EligibilityProgram: prg,
				},
			},
		}

		// Party is ORGANIZATION, not PERSON - should fail eligibility
		mockParty := &mockPartyClient{
			partyExists: true,
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		}
		mockParty.partyTypeOverride = partyv1.PartyType_PARTY_TYPE_ORGANIZATION

		svc := &Service{
			repo:             repo,
			partyClient:      mockParty,
			accountTypeCache: cache,
			logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
		}

		req := &pb.InitiateCurrentAccountRequest{
			ExternalIdentifier: "GB82WEST12345698765432",
			PartyId:            newTestPartyID(),
			InstrumentCode:     "GBP",
			ProductTypeCode:    "PERSONAL_CURRENT",
		}
		resp, err := svc.InitiateCurrentAccount(ctx, req)

		require.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
		assert.Contains(t, st.Message(), "not eligible")
	})
}

// TestInitiateCurrentAccount_WithProductType_EligibilityRequiresPartyClient verifies
// that account creation fails fast when an eligibility program is configured but
// the party client is not available.
func TestInitiateCurrentAccount_WithProductType_EligibilityRequiresPartyClient(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	prg := compileTestEligibilityProgram(t, `party.type == "PERSON"`)

	def := newTestDefinition("PERSONAL_CURRENT", accounttype.BehaviorClassCustomer)
	def.EligibilityCEL = `party.type == "PERSON"`
	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{
			"PERSONAL_CURRENT": {
				Definition:         def,
				EligibilityProgram: prg,
			},
		},
	}

	svc := &Service{
		repo:             repo,
		partyClient:      nil, // No party client configured
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "PERSONAL_CURRENT",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "party service is required")
}

// TestInitiateCurrentAccount_WithProductType_VersionExceedsLatest verifies
// that requesting a version higher than the latest cached version is rejected.
func TestInitiateCurrentAccount_WithProductType_VersionExceedsLatest(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	def := newTestDefinition("CURRENT_GBP", accounttype.BehaviorClassCustomer)
	def.Version = 3 // latest version is 3
	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{
			"CURRENT_GBP": {Definition: def},
		},
	}

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:             repo,
		partyClient:      mockParty,
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	requestedVersion := int32(5) // higher than latest version 3
	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "CURRENT_GBP",
		ProductTypeVersion: &requestedVersion,
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "exceeds latest version")
}

// TestInitiateCurrentAccount_WithProductType_AttributeValidation verifies
// JSON Schema attribute validation.
func TestInitiateCurrentAccount_WithProductType_AttributeValidation(t *testing.T) {
	schemaJSON := `{
		"type": "object",
		"properties": {
			"risk_level": {
				"type": "string",
				"enum": ["LOW", "MEDIUM", "HIGH"]
			}
		},
		"required": ["risk_level"]
	}`

	t.Run("valid attributes pass", func(t *testing.T) {
		db, ctx, cleanup := setupProductTypeTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)

		def := newTestDefinition("RISK_CURRENT", accounttype.BehaviorClassCustomer)
		def.AttributeSchema = json.RawMessage(schemaJSON)
		compiledSchema := compileTestSchema(t, schemaJSON)

		cache := &mockAccountTypeCache{
			entries: map[string]*CachedAccountType{
				"RISK_CURRENT": {
					Definition:     def,
					CompiledSchema: compiledSchema,
				},
			},
		}

		mockParty := &mockPartyClient{
			partyExists: true,
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		}

		svc := &Service{
			repo:             repo,
			partyClient:      mockParty,
			accountTypeCache: cache,
			logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
		}

		req := &pb.InitiateCurrentAccountRequest{
			ExternalIdentifier: "GB82WEST12345698765432",
			PartyId:            newTestPartyID(),
			InstrumentCode:     "GBP",
			ProductTypeCode:    "RISK_CURRENT",
			Attributes:         map[string]string{"risk_level": "LOW"},
		}
		resp, err := svc.InitiateCurrentAccount(ctx, req)

		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("invalid attributes rejected", func(t *testing.T) {
		db, ctx, cleanup := setupProductTypeTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)

		def := newTestDefinition("RISK_CURRENT", accounttype.BehaviorClassCustomer)
		def.AttributeSchema = json.RawMessage(schemaJSON)
		compiledSchema := compileTestSchema(t, schemaJSON)

		cache := &mockAccountTypeCache{
			entries: map[string]*CachedAccountType{
				"RISK_CURRENT": {
					Definition:     def,
					CompiledSchema: compiledSchema,
				},
			},
		}

		mockParty := &mockPartyClient{
			partyExists: true,
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		}

		svc := &Service{
			repo:             repo,
			partyClient:      mockParty,
			accountTypeCache: cache,
			logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
		}

		req := &pb.InitiateCurrentAccountRequest{
			ExternalIdentifier: "GB82WEST12345698765432",
			PartyId:            newTestPartyID(),
			InstrumentCode:     "GBP",
			ProductTypeCode:    "RISK_CURRENT",
			Attributes:         map[string]string{"risk_level": "EXTREME"}, // Invalid enum value
		}
		resp, err := svc.InitiateCurrentAccount(ctx, req)

		require.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "attribute validation failed")
	})

	t.Run("missing required attributes rejected", func(t *testing.T) {
		db, ctx, cleanup := setupProductTypeTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)

		def := newTestDefinition("RISK_CURRENT", accounttype.BehaviorClassCustomer)
		def.AttributeSchema = json.RawMessage(schemaJSON)
		compiledSchema := compileTestSchema(t, schemaJSON)

		cache := &mockAccountTypeCache{
			entries: map[string]*CachedAccountType{
				"RISK_CURRENT": {
					Definition:     def,
					CompiledSchema: compiledSchema,
				},
			},
		}

		mockParty := &mockPartyClient{
			partyExists: true,
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
		}

		svc := &Service{
			repo:             repo,
			partyClient:      mockParty,
			accountTypeCache: cache,
			logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
		}

		req := &pb.InitiateCurrentAccountRequest{
			ExternalIdentifier: "GB82WEST12345698765432",
			PartyId:            newTestPartyID(),
			InstrumentCode:     "GBP",
			ProductTypeCode:    "RISK_CURRENT",
			Attributes:         map[string]string{}, // Missing required risk_level
		}
		resp, err := svc.InitiateCurrentAccount(ctx, req)

		require.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

// TestInitiateCurrentAccount_WithProductType_ValuationFeatureSeeding verifies
// that ValuationFeatures are seeded from product type templates.
func TestInitiateCurrentAccount_WithProductType_ValuationFeatureSeeding(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	vfRepo := vf.NewRepository(db)

	methodID := uuid.New()
	def := newTestDefinition("MULTI_CCY_CURRENT", accounttype.BehaviorClassCustomer)
	def.ValuationMethods = []accounttype.ValuationMethodTemplate{
		{
			ID:                     uuid.New(),
			AccountTypeID:          def.ID,
			InputInstrument:        "USD",
			ValuationMethodID:      methodID,
			ValuationMethodVersion: 1,
			Status:                 accounttype.StatusActive,
			Parameters:             map[string]any{"source": "ECB", "frequency": "daily"},
		},
		{
			ID:                     uuid.New(),
			AccountTypeID:          def.ID,
			InputInstrument:        "EUR",
			ValuationMethodID:      methodID,
			ValuationMethodVersion: 1,
			Status:                 accounttype.StatusActive,
			Parameters:             map[string]any{"source": "ECB", "frequency": "daily"},
		},
	}

	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{
			"MULTI_CCY_CURRENT": {Definition: def},
		},
	}

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:                 repo,
		partyClient:          mockParty,
		accountTypeCache:     cache,
		valuationFeatureRepo: vfRepo,
		logger:               slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "MULTI_CCY_CURRENT",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify the account was created
	account, err := repo.FindByID(ctx, resp.AccountId)
	require.NoError(t, err)

	// Retrieve seeded ValuationFeatures
	activeStatus := vf.LifecycleStatusActive
	features, err := vfRepo.FindByAccountID(ctx, account.ID(), &activeStatus)
	require.NoError(t, err)
	assert.Len(t, features, 2, "Should have seeded 2 valuation features")

	// Verify instruments and parameters
	instruments := make(map[string]bool)
	for _, f := range features {
		instruments[f.InstrumentCode] = true
		assert.Equal(t, methodID, f.ValuationMethodID)
		assert.Equal(t, 1, f.ValuationMethodVersion)
		assert.True(t, f.IsActive(), "Seeded features should be ACTIVE")
		assert.Equal(t, "ECB", f.Parameters["source"], "Template parameters should be propagated")
		assert.Equal(t, "daily", f.Parameters["frequency"], "Template parameters should be propagated")
	}
	assert.True(t, instruments["USD"], "Should have USD feature")
	assert.True(t, instruments["EUR"], "Should have EUR feature")
}

// TestInitiateCurrentAccount_BackwardsCompatibility verifies that account creation
// still works without product_type_code (legacy path).
func TestInitiateCurrentAccount_BackwardsCompatibility(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// No product_type_code - legacy behavior
	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.NotEmpty(t, resp.AccountId)
	// Product type fields should be empty/zero for legacy accounts
	assert.Empty(t, resp.Facility.ProductTypeCode)
	assert.Equal(t, int32(0), resp.Facility.ProductTypeVersion)
}

// TestInitiateCurrentAccount_WithProductType_NoCacheConfigured verifies that
// product type code is silently ignored when no cache is configured.
func TestInitiateCurrentAccount_WithProductType_NoCacheConfigured(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		// accountTypeCache is nil - not configured
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "CURRENT_GBP", // Provided but will be ignored
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, resp)
	// Product type is not resolved when cache is nil
	assert.Empty(t, resp.Facility.ProductTypeCode)
}

// TestInitiateCurrentAccount_WithProductType_MissingTenantContext verifies
// that product type resolution fails gracefully when tenant context is missing.
func TestInitiateCurrentAccount_WithProductType_MissingTenantContext(t *testing.T) {
	db, _, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	cache := &mockAccountTypeCache{
		entries: map[string]*CachedAccountType{},
	}

	// Use context WITHOUT tenant
	ctx := context.Background()

	svc := &Service{
		repo:             repo,
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "CURRENT_GBP",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "tenant context")
}

// TestInitiateCurrentAccount_WithProductType_CacheError verifies
// handling of cache/loader errors.
func TestInitiateCurrentAccount_WithProductType_CacheError(t *testing.T) {
	db, ctx, cleanup := setupProductTypeTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	cache := &mockAccountTypeCache{
		err: errors.New("cache connection refused"),
	}

	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:             repo,
		partyClient:      mockParty,
		accountTypeCache: cache,
		logger:           slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            newTestPartyID(),
		InstrumentCode:     "GBP",
		ProductTypeCode:    "CURRENT_GBP",
	}
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "product type not found")
}
