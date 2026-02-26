// Package service provides integration tests for Party Service validation
// during account creation in the CurrentAccount service.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// Test sentinel errors for party validation
var (
	errPartyServiceUnavailable = errors.New("party service unavailable")
	errPartyServiceTimeout     = errors.New("party service timeout")
)

// mockPartyClient implements clients.PartyClient for testing
type mockPartyClient struct {
	mu                sync.Mutex
	validateCalls     int
	getCalls          int
	validateError     error
	getError          error
	partyStatus       partyv1.PartyStatus
	partyTypeOverride partyv1.PartyType // Override party type in GetParty response
	extRefType        partyv1.ExternalReferenceType
	partyExists       bool
	simulateTimeout   bool
	timeoutDuration   time.Duration
	closeCalls        int
	lastValidatedID   string
	lastRetrievedID   string
}

func (m *mockPartyClient) ValidateParty(ctx context.Context, partyID string) error {
	m.mu.Lock()
	m.validateCalls++
	m.lastValidatedID = partyID
	simulateTimeout := m.simulateTimeout
	timeoutDuration := m.timeoutDuration
	validateError := m.validateError
	partyExists := m.partyExists
	partyStatus := m.partyStatus
	m.mu.Unlock()

	// Simulate timeout if configured
	if simulateTimeout {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(timeoutDuration):
			return errPartyServiceTimeout
		}
	}

	if validateError != nil {
		return validateError
	}

	if !partyExists {
		return ErrPartyNotFound
	}

	if partyStatus != partyv1.PartyStatus_PARTY_STATUS_ACTIVE {
		return ErrPartyNotActive
	}

	return nil
}

func (m *mockPartyClient) GetParty(_ context.Context, partyID string) (*partyv1.Party, error) {
	m.mu.Lock()
	m.getCalls++
	m.lastRetrievedID = partyID
	getError := m.getError
	partyExists := m.partyExists
	partyStatus := m.partyStatus
	partyType := m.partyTypeOverride
	extRefType := m.extRefType
	m.mu.Unlock()

	if getError != nil {
		return nil, getError
	}

	if !partyExists {
		return nil, ErrPartyNotFound
	}

	return &partyv1.Party{
		PartyId:               partyID,
		LegalName:             "Test Party",
		PartyType:             partyType,
		Status:                partyStatus,
		ExternalReferenceType: extRefType,
	}, nil
}

func (m *mockPartyClient) Close() error {
	m.mu.Lock()
	m.closeCalls++
	m.mu.Unlock()
	return nil
}

func setupPartyIntegrationTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db := openSharedDB(t)

	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	quotedSchema := pq.QuoteIdentifier(schemaName)

	// Create the tenant schema
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", quotedSchema)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", quotedSchema)).Error
	require.NoError(t, err)

	// AutoMigrate the account entity in the tenant schema
	err = db.AutoMigrate(&persistence.CurrentAccountEntity{})
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	cleanup := func() {
		db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", quotedSchema))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	}

	return db, ctx, cleanup
}

func createInitiateAccountRequest(partyID, iban string) *pb.InitiateCurrentAccountRequest {
	return &pb.InitiateCurrentAccountRequest{
		ExternalIdentifier: iban,
		PartyId:            partyID,
		InstrumentCode:     "GBP",
	}
}

// newTestPartyID generates a valid UUID string for use as a party ID
func newTestPartyID() string {
	return uuid.New().String()
}

// Test Suite: Party Validation During Account Creation

// TestInitiateCurrentAccount_WithPartyValidation_Success verifies successful account creation
// when the party exists and is active.
//
// Flow verified:
// 1. Party validation is called with the provided party ID
// 2. Party exists and has ACTIVE status
// 3. Account is created successfully
// 4. Response contains valid account details
func TestInitiateCurrentAccount_WithPartyValidation_Success(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupPartyIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create mock party client that returns active party
	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	// Create service with party client
	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute account creation with valid UUID party ID
	partyID := newTestPartyID()
	req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	// Verify success
	require.NoError(t, err, "Account creation should succeed with active party")
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.AccountId)
	assert.NotNil(t, resp.Facility)
	assert.Equal(t, "GBP", resp.Facility.InstrumentCode)

	// Verify party validation was called
	assert.Equal(t, 1, mockParty.validateCalls, "Party validation should be called once")
	assert.Equal(t, partyID, mockParty.lastValidatedID, "Should validate the correct party ID")
}

// TestInitiateCurrentAccount_PartyNotFound verifies that account creation fails
// with InvalidArgument when the party does not exist.
//
// Expected behavior:
// 1. Party validation returns ErrPartyNotFound
// 2. Account creation fails with InvalidArgument error code
// 3. Error message contains the party ID
// 4. No account is created in the database
func TestInitiateCurrentAccount_PartyNotFound(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupPartyIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create mock party client that returns party not found
	mockParty := &mockPartyClient{
		partyExists: false,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute account creation with valid UUID party ID (even though party doesn't exist)
	partyID := newTestPartyID()
	req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	// Verify failure
	require.Error(t, err, "Account creation should fail when party not found")
	assert.Nil(t, resp)

	// Verify error details
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Should return InvalidArgument for party not found")
	assert.Contains(t, st.Message(), "party not found", "Error message should mention party not found")
	assert.Contains(t, st.Message(), partyID, "Error message should contain party ID")

	// Verify party validation was called
	assert.Equal(t, 1, mockParty.validateCalls)
}

// TestInitiateCurrentAccount_InactiveParty verifies that account creation fails
// with FailedPrecondition when the party exists but is not active.
//
// Expected behavior:
// 1. Party exists but has non-ACTIVE status (e.g., RESTRICTED, TERMINATED)
// 2. Account creation fails with FailedPrecondition error code
// 3. Error message indicates party is not active
func TestInitiateCurrentAccount_InactiveParty(t *testing.T) {
	tests := []struct {
		name        string
		partyStatus partyv1.PartyStatus
	}{
		{
			name:        "restricted party",
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_RESTRICTED,
		},
		{
			name:        "terminated party",
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_TERMINATED,
		},
		{
			name:        "unspecified party status",
			partyStatus: partyv1.PartyStatus_PARTY_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			db, ctx, cleanup := setupPartyIntegrationTestDB(t)
			defer cleanup()

			repo := persistence.NewRepository(db)

			// Create mock party client with inactive status
			mockParty := &mockPartyClient{
				partyExists: true,
				partyStatus: tt.partyStatus,
			}

			svc := &Service{
				repo:        repo,
				partyClient: mockParty,
				logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
			}

			// Execute account creation with valid UUID party ID
			partyID := newTestPartyID()
			req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
			resp, err := svc.InitiateCurrentAccount(ctx, req)

			// Verify failure
			require.Error(t, err, "Account creation should fail with inactive party")
			assert.Nil(t, resp)

			// Verify error details
			st, ok := status.FromError(err)
			require.True(t, ok, "Error should be gRPC status error")
			assert.Equal(t, codes.FailedPrecondition, st.Code(),
				"Should return FailedPrecondition for inactive party")
			assert.Contains(t, st.Message(), "party not active",
				"Error message should mention party not active")
		})
	}
}

// TestInitiateCurrentAccount_PartyServiceUnavailable verifies proper error handling
// when the Party Service is unavailable.
//
// Expected behavior:
// 1. Party validation fails with a generic error (not ErrPartyNotFound or ErrPartyNotActive)
// 2. Account creation fails with Internal error code
// 3. Error message indicates party validation failed
func TestInitiateCurrentAccount_PartyServiceUnavailable(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupPartyIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create mock party client that simulates service unavailable
	mockParty := &mockPartyClient{
		validateError: errPartyServiceUnavailable,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute account creation with valid UUID party ID
	partyID := newTestPartyID()
	req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	// Verify failure
	require.Error(t, err, "Account creation should fail when party service unavailable")
	assert.Nil(t, resp)

	// Verify error details
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(),
		"Should return Internal for service unavailability")
	assert.Contains(t, st.Message(), "party validation failed",
		"Error message should mention party validation failed")
}

// TestInitiateCurrentAccount_PartyServiceTimeout verifies proper handling
// when the Party Service times out.
//
// Expected behavior:
// 1. Party validation times out
// 2. Account creation fails with appropriate error
// 3. Context deadline exceeded is properly handled
func TestInitiateCurrentAccount_PartyServiceTimeout(t *testing.T) {
	// Setup
	db, baseCtx, cleanup := setupPartyIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create mock party client that simulates timeout
	mockParty := &mockPartyClient{
		simulateTimeout: true,
		timeoutDuration: 100 * time.Millisecond,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Create context with short timeout, preserving tenant from baseCtx
	ctx, cancel := context.WithTimeout(baseCtx, 50*time.Millisecond)
	defer cancel()

	// Execute account creation with valid UUID party ID
	partyID := newTestPartyID()
	req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
	resp, err := svc.InitiateCurrentAccount(ctx, req)

	// Verify failure
	require.Error(t, err, "Account creation should fail on timeout")
	assert.Nil(t, resp)

	// Verify error is related to timeout/deadline
	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be gRPC status error")
	assert.Equal(t, codes.Internal, st.Code(),
		"Should return Internal error for timeout")
}

// TestInitiateCurrentAccount_ConcurrentCreationSameParty verifies that
// multiple accounts can be created for the same party concurrently.
func TestInitiateCurrentAccount_ConcurrentCreationSameParty(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupPartyIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create mock party client
	mockParty := &mockPartyClient{
		partyExists: true,
		partyStatus: partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Create multiple accounts concurrently for the same party
	const numAccounts = 5
	results := make(chan error, numAccounts)
	partyID := newTestPartyID()

	for i := 0; i < numAccounts; i++ {
		go func(idx int) {
			req := &pb.InitiateCurrentAccountRequest{
				ExternalIdentifier: "GB82WEST1234569876543" + string(rune('0'+idx)),
				PartyId:            partyID,
				InstrumentCode:     "GBP",
			}
			_, err := svc.InitiateCurrentAccount(ctx, req)
			results <- err
		}(i)
	}

	// Collect results
	var successCount int
	for i := 0; i < numAccounts; i++ {
		err := <-results
		if err == nil {
			successCount++
		}
	}

	// All accounts should be created successfully
	assert.Equal(t, numAccounts, successCount, "All concurrent account creations should succeed")

	// Verify party validation was called for each account
	assert.Equal(t, numAccounts, mockParty.validateCalls,
		"Party validation should be called once per account creation")
}

// TestInitiateCurrentAccount_PartyValidationCalledBeforeAccountCreation verifies that
// party validation attempts are properly recorded for observability.
func TestInitiateCurrentAccount_PartyValidationCalledBeforeAccountCreation(t *testing.T) {
	// Setup
	db, ctx, cleanup := setupPartyIntegrationTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)

	// Create mock party client that fails
	mockParty := &mockPartyClient{
		partyExists: false,
	}

	svc := &Service{
		repo:        repo,
		partyClient: mockParty,
		logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}

	// Execute account creation (will fail)
	partyID := newTestPartyID()
	req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
	_, err := svc.InitiateCurrentAccount(ctx, req)

	// Verify party validation was called
	require.Error(t, err)
	assert.Equal(t, 1, mockParty.validateCalls, "Party validation should be called")

	// Verify no account was saved (party validation failed before account creation)
	// This confirms validation happens BEFORE any database operations
	_, findErr := repo.FindByID(ctx, "GB82WEST12345698765432")
	assert.Error(t, findErr, "No account should exist since party validation failed")
}

// TestInitiateCurrentAccount_TableDriven provides comprehensive table-driven tests
// for various party validation scenarios.
func TestInitiateCurrentAccount_TableDriven(t *testing.T) {
	tests := []struct {
		name           string
		partyExists    bool
		partyStatus    partyv1.PartyStatus
		validateError  error
		expectedCode   codes.Code
		expectedErrMsg string
		shouldSucceed  bool
	}{
		{
			name:          "valid active party",
			partyExists:   true,
			partyStatus:   partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
			shouldSucceed: true,
		},
		{
			name:           "party not found",
			partyExists:    false,
			expectedCode:   codes.InvalidArgument,
			expectedErrMsg: "party not found",
			shouldSucceed:  false,
		},
		{
			name:           "restricted party",
			partyExists:    true,
			partyStatus:    partyv1.PartyStatus_PARTY_STATUS_RESTRICTED,
			expectedCode:   codes.FailedPrecondition,
			expectedErrMsg: "party not active",
			shouldSucceed:  false,
		},
		{
			name:           "terminated party",
			partyExists:    true,
			partyStatus:    partyv1.PartyStatus_PARTY_STATUS_TERMINATED,
			expectedCode:   codes.FailedPrecondition,
			expectedErrMsg: "party not active",
			shouldSucceed:  false,
		},
		{
			name:           "party service error",
			validateError:  errPartyServiceUnavailable,
			expectedCode:   codes.Internal,
			expectedErrMsg: "party validation failed",
			shouldSucceed:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			db, ctx, cleanup := setupPartyIntegrationTestDB(t)
			defer cleanup()

			repo := persistence.NewRepository(db)

			mockParty := &mockPartyClient{
				partyExists:   tt.partyExists,
				partyStatus:   tt.partyStatus,
				validateError: tt.validateError,
			}

			svc := &Service{
				repo:        repo,
				partyClient: mockParty,
				logger:      slog.New(slog.NewTextHandler(os.Stdout, nil)),
			}

			// Execute with valid UUID party ID
			partyID := newTestPartyID()
			req := createInitiateAccountRequest(partyID, "GB82WEST12345698765432")
			resp, err := svc.InitiateCurrentAccount(ctx, req)

			// Verify
			if tt.shouldSucceed {
				require.NoError(t, err)
				assert.NotNil(t, resp)
				assert.NotEmpty(t, resp.AccountId)
			} else {
				require.Error(t, err)
				assert.Nil(t, resp)

				st, ok := status.FromError(err)
				require.True(t, ok)
				assert.Equal(t, tt.expectedCode, st.Code())
				assert.Contains(t, st.Message(), tt.expectedErrMsg)
			}

			// Always verify party validation was attempted
			assert.Equal(t, 1, mockParty.validateCalls)
		})
	}
}
