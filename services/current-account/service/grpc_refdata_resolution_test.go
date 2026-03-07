package service

// Integration tests for Reference Data resolution during account creation.
// Validates that InitiateCurrentAccount resolves instrument properties (dimension, precision)
// from the InstrumentGetter (Reference Data service) and rejects unknown instruments.

import (
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestInitiateCurrentAccount_RefDataResolution_CurrencyGBP validates account creation
// with a CURRENCY instrument resolved from Reference Data.
func TestInitiateCurrentAccount_RefDataResolution_CurrencyGBP(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            uuid.New().String(),
		InstrumentCode:     "GBP",
	}

	resp, err := svc.InitiateCurrentAccount(ctx, req)
	require.NoError(t, err, "GBP account creation should succeed")
	assert.NotEmpty(t, resp.AccountId)
	assert.Equal(t, "GBP", resp.Facility.InstrumentCode)

	// Verify persisted account has correct dimension
	account, err := repo.FindByID(ctx, resp.AccountId)
	require.NoError(t, err)
	assert.Equal(t, "GBP", account.InstrumentCode())
	assert.Equal(t, "CURRENCY", account.Dimension())
	assert.Equal(t, 2, account.Balance().Precision(), "GBP should have precision 2")
}

// TestInitiateCurrentAccount_RefDataResolution_EnergyKWH validates account creation
// with an ENERGY instrument resolved from Reference Data with precision 6.
func TestInitiateCurrentAccount_RefDataResolution_EnergyKWH(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// The defaultInstrumentGetter provides KWH with ENERGY dimension and precision 6
	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "KWH-IDENT-001",
		PartyId:            uuid.New().String(),
		InstrumentCode:     "KWH",
	}

	resp, err := svc.InitiateCurrentAccount(ctx, req)
	require.NoError(t, err, "KWH account creation should succeed")
	assert.NotEmpty(t, resp.AccountId)

	// Verify persisted account has correct energy dimension and precision
	account, err := repo.FindByID(ctx, resp.AccountId)
	require.NoError(t, err)
	assert.Equal(t, "KWH", account.InstrumentCode())
	assert.Equal(t, "ENERGY", account.Dimension())
	assert.Equal(t, 6, account.Balance().Precision(), "KWH should have precision 6")
}

// TestInitiateCurrentAccount_RefDataResolution_UnknownInstrument validates that
// account creation fails with InvalidArgument for instruments not in Reference Data.
func TestInitiateCurrentAccount_RefDataResolution_UnknownInstrument(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "UNKNOWN-IDENT-001",
		PartyId:            uuid.New().String(),
		InstrumentCode:     "IMAGINARY_COIN",
	}

	_, err := svc.InitiateCurrentAccount(ctx, req)
	require.Error(t, err, "unknown instrument should be rejected")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code(), "unknown instrument should return InvalidArgument")
	assert.Contains(t, st.Message(), "unknown instrument_code")
}

// TestInitiateCurrentAccount_RefDataResolution_NoInstrumentGetter validates that
// account creation fails with FailedPrecondition when Reference Data is not configured.
func TestInitiateCurrentAccount_RefDataResolution_NoInstrumentGetter(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc, err := NewService(repo, nil)
	require.NoError(t, err)
	// Deliberately do NOT set instrumentGetter

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "GB82WEST12345698765432",
		PartyId:            uuid.New().String(),
		InstrumentCode:     "GBP",
	}

	_, err = svc.InitiateCurrentAccount(ctx, req)
	require.Error(t, err, "should fail without instrumentGetter")

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code(), "missing Reference Data should return FailedPrecondition")
	assert.Contains(t, st.Message(), "Reference Data")
}

// TestInitiateCurrentAccount_RefDataResolution_CustomPrecision validates that
// custom precision from Reference Data is correctly propagated to the account.
func TestInitiateCurrentAccount_RefDataResolution_CustomPrecision(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// Override the default instrument getter with one that includes CARBON_CREDIT
	svc.instrumentGetter = &mockInstrumentGetter{
		instruments: map[string]*cache.CachedInstrument{
			"CARBON_CREDIT": {Definition: &registry.InstrumentDefinition{
				Code: "CARBON_CREDIT", Dimension: registry.DimensionCarbon, Precision: 4, Version: 1,
			}},
		},
	}

	req := &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: "CC-IDENT-001",
		PartyId:            uuid.New().String(),
		InstrumentCode:     "CARBON_CREDIT",
	}

	resp, err := svc.InitiateCurrentAccount(ctx, req)
	require.NoError(t, err, "CARBON_CREDIT account creation should succeed")

	// Verify persisted account has correct carbon dimension and precision
	account, err := repo.FindByID(ctx, resp.AccountId)
	require.NoError(t, err)
	assert.Equal(t, "CARBON_CREDIT", account.InstrumentCode())
	assert.Equal(t, "CARBON", account.Dimension())
	assert.Equal(t, 4, account.Balance().Precision(), "CARBON_CREDIT should have precision 4")
}
