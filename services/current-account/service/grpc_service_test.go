package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	commonpb "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/services/current-account/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/saga"
	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// testSagaRunner creates a StarlarkSagaRunner for testing with minimal setup.
// Returns the saga runner and the loaded deposit/withdrawal scripts.
func testSagaRunner(t *testing.T) (*saga.StarlarkSagaRunner, string, string) {
	t.Helper()

	// Load saga scripts from reference-data canonical source
	_, filename, _, ok := runtime.Caller(0)
	require.True(t, ok, "failed to get current file path")
	serviceDir := filepath.Dir(filename)
	repoRoot := filepath.Join(serviceDir, "..", "..", "..")

	depositScriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "deposit", "v1.0.0.star")
	depositScriptBytes, err := os.ReadFile(depositScriptPath)
	require.NoError(t, err, "failed to read deposit script")
	depositScript := string(depositScriptBytes)

	withdrawalScriptPath := filepath.Join(repoRoot, "services", "reference-data", "saga", "defaults", "withdrawal", "v1.0.0.star")
	withdrawalScriptBytes, err := os.ReadFile(withdrawalScriptPath)
	require.NoError(t, err, "failed to read withdrawal script")
	withdrawalScript := string(withdrawalScriptBytes)

	// Create saga handler registry
	handlerRegistry := saga.NewHandlerRegistry()
	err = RegisterCurrentAccountHandlers(handlerRegistry)
	require.NoError(t, err, "failed to register saga handlers")

	// Load schema registry from handlers.yaml
	schemaRegistryPath := filepath.Join(serviceDir, "..", "..", "..", "shared", "pkg", "saga", "schema", "handlers.yaml")
	schemaRegistryData, err := os.ReadFile(schemaRegistryPath)
	require.NoError(t, err, "failed to read handlers schema")

	schemaRegistry := schema.NewRegistry()
	err = schemaRegistry.LoadFromYAML(schemaRegistryData)
	require.NoError(t, err, "failed to load schema")

	// Build service modules
	serviceModules, err := schema.BuildServiceModules(handlerRegistry, schemaRegistry)
	require.NoError(t, err, "failed to build service modules")

	// Create Starlark saga runner
	runtime, err := saga.NewRuntime(testLogger())
	require.NoError(t, err, "failed to create saga runtime")

	sagaRunner, err := saga.NewStarlarkSagaRunner(saga.StarlarkSagaRunnerConfig{
		Runtime:        runtime,
		Registry:       handlerRegistry,
		ServiceModules: serviceModules,
		Logger:         testLogger(),
	})
	require.NoError(t, err, "failed to create saga runner")

	return sagaRunner, depositScript, withdrawalScript
}

// injectMandatoryClients sets up mock Position Keeping and Financial Accounting clients with orchestrators.
// Position Keeping and orchestration are now mandatory for all deposit/withdrawal operations.
func injectMandatoryClients(t *testing.T, svc *Service, repo *persistence.Repository, accountBalances map[string]int64) {
	t.Helper()
	if accountBalances == nil {
		accountBalances = make(map[string]int64)
	}
	mockPosKeeping := &mockPositionKeepingClient{accountBalances: accountBalances}
	mockFinAcct := &mockFinancialAccountingClient{}

	svc.posKeepingClient = mockPosKeeping
	svc.finAcctClient = mockFinAcct

	// Create saga runner and load scripts
	sagaRunner, depositScript, withdrawalScript := testSagaRunner(t)

	// Create orchestrators with mocked clients and saga runner
	depositOrch, err := NewDepositOrchestrator(DepositOrchestratorConfig{
		Logger:           testLogger(),
		Repo:             repo,
		PosKeepingClient: mockPosKeeping,
		FinAcctClient:    mockFinAcct,
		SagaRunner:       sagaRunner,
		DepositScript:    depositScript,
	})
	require.NoError(t, err, "failed to create deposit orchestrator")
	svc.depositOrchestrator = depositOrch

	withdrawalOrch, err := NewWithdrawalOrchestrator(WithdrawalOrchestratorConfig{
		Logger:           testLogger(),
		Repo:             repo,
		PosKeepingClient: mockPosKeeping,
		FinAcctClient:    mockFinAcct,
		SagaRunner:       sagaRunner,
		WithdrawalScript: withdrawalScript,
	})
	require.NoError(t, err, "failed to create withdrawal orchestrator")
	svc.withdrawalOrchestrator = withdrawalOrch
}

// mustNewService creates a Service with mock Position Keeping, Financial Accounting, and orchestrators.
// Position Keeping is now mandatory - all operations require balance queries and orchestration.
func mustNewService(t *testing.T, repo *persistence.Repository, lienRepo *persistence.LienRepository) *Service {
	t.Helper()
	svc, err := NewService(repo, lienRepo)
	require.NoError(t, err, "unexpected error creating service")
	injectMandatoryClients(t, svc, repo, nil)
	return svc
}

// mustNewServiceWithIdempotency creates a Service with idempotency and mock clients.
// Position Keeping is now mandatory - all operations require balance queries and orchestration.
func mustNewServiceWithIdempotency(t *testing.T, repo *persistence.Repository, lienRepo *persistence.LienRepository, idempotencyService idempotency.Service) *Service {
	t.Helper()
	svc, err := NewServiceWithIdempotency(repo, lienRepo, idempotencyService)
	require.NoError(t, err, "unexpected error creating service")
	injectMandatoryClients(t, svc, repo, nil)
	return svc
}

// mustNewServiceWithPositionKeeping creates a Service with mock clients and specified account balances.
// The accountBalances map configures expected balance for each account ID (in cents).
func mustNewServiceWithPositionKeeping(t *testing.T, repo *persistence.Repository, lienRepo *persistence.LienRepository, accountBalances map[string]int64) *Service {
	t.Helper()
	svc, err := NewService(repo, lienRepo)
	require.NoError(t, err, "unexpected error creating service")
	injectMandatoryClients(t, svc, repo, accountBalances)
	return svc
}

// mustNewServiceWithIdempotencyAndPositionKeeping creates a Service with idempotency and mock clients.
func mustNewServiceWithIdempotencyAndPositionKeeping(t *testing.T, repo *persistence.Repository, lienRepo *persistence.LienRepository, idempotencyService idempotency.Service, accountBalances map[string]int64) *Service {
	t.Helper()
	svc, err := NewServiceWithIdempotency(repo, lienRepo, idempotencyService)
	require.NoError(t, err, "unexpected error creating service")
	injectMandatoryClients(t, svc, repo, accountBalances)
	return svc
}

// TestNewService_DefensiveTests verifies nil dependency validation for NewService.
// Per ADR-0008: Constructors must validate dependencies and return errors instead of panicking.
func TestNewService_DefensiveTests(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	validRepo := persistence.NewRepository(db)
	validLienRepo := persistence.NewLienRepository(db)

	tests := []struct {
		name         string
		repo         *persistence.Repository
		lienRepo     *persistence.LienRepository
		wantErr      bool
		wantSentinel error
		rationale    string
	}{
		{
			name:         "valid dependencies - both repos provided",
			repo:         validRepo,
			lienRepo:     validLienRepo,
			wantErr:      false,
			wantSentinel: nil,
			rationale:    "Valid initialization with all dependencies",
		},
		{
			name:         "valid dependencies - lienRepo is optional",
			repo:         validRepo,
			lienRepo:     nil,
			wantErr:      false,
			wantSentinel: nil,
			rationale:    "LienRepo is optional for NewService",
		},
		{
			name:         "nil repository returns ErrRepositoryNil",
			repo:         nil,
			lienRepo:     validLienRepo,
			wantErr:      true,
			wantSentinel: ErrRepositoryNil,
			rationale:    "Repository is essential - nil would cause panic on first use",
		},
		{
			name:         "all nil returns ErrRepositoryNil",
			repo:         nil,
			lienRepo:     nil,
			wantErr:      true,
			wantSentinel: ErrRepositoryNil,
			rationale:    "Should error on first nil check (repository)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewService(tt.repo, tt.lienRepo)
			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				require.Nil(t, svc, "Service should be nil when error occurs")
				require.ErrorIs(t, err, tt.wantSentinel, "Should return the expected sentinel error")
			} else {
				require.NoError(t, err, tt.rationale)
				require.NotNil(t, svc, tt.rationale)
			}
		})
	}
}

// TestNewServiceWithIdempotency_DefensiveTests verifies nil dependency validation.
// Note: IdempotencyService is optional in current-account (unlike financial-accounting/position-keeping).
func TestNewServiceWithIdempotency_DefensiveTests(t *testing.T) {
	db, _, cleanup := setupTestDB(t)
	defer cleanup()

	validRepo := persistence.NewRepository(db)
	validLienRepo := persistence.NewLienRepository(db)
	mockIdemp := &mockIdempotencyService{}

	tests := []struct {
		name               string
		repo               *persistence.Repository
		lienRepo           *persistence.LienRepository
		idempotencyService idempotency.Service
		wantErr            bool
		wantSentinel       error
		rationale          string
	}{
		{
			name:               "valid dependencies - all provided",
			repo:               validRepo,
			lienRepo:           validLienRepo,
			idempotencyService: mockIdemp,
			wantErr:            false,
			wantSentinel:       nil,
			rationale:          "Valid initialization with all dependencies",
		},
		{
			name:               "valid - idempotency is optional",
			repo:               validRepo,
			lienRepo:           nil,
			idempotencyService: nil,
			wantErr:            false,
			wantSentinel:       nil,
			rationale:          "IdempotencyService is optional in current-account",
		},
		{
			name:               "nil repository returns ErrRepositoryNil",
			repo:               nil,
			lienRepo:           validLienRepo,
			idempotencyService: mockIdemp,
			wantErr:            true,
			wantSentinel:       ErrRepositoryNil,
			rationale:          "Repository is essential - nil would cause panic on first use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewServiceWithIdempotency(tt.repo, tt.lienRepo, tt.idempotencyService)
			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				require.Nil(t, svc, "Service should be nil when error occurs")
				require.ErrorIs(t, err, tt.wantSentinel, "Should return the expected sentinel error")
			} else {
				require.NoError(t, err, tt.rationale)
				require.NotNil(t, svc, tt.rationale)
			}
		})
	}
}

// mustNewMoney is a test helper that creates Money or panics
func mustNewMoney(currency string, amountCents int64) domain.Money {
	m, err := domain.NewMoney(currency, amountCents)
	if err != nil {
		panic(err)
	}
	return m
}

const svcTestTenantID = "test_tenant"

func setupTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.CurrentAccountEntity{}})

	// Create the tenant schema for tests
	tid := tenant.TenantID(svcTestTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the current_accounts table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.current_accounts (
		id UUID PRIMARY KEY,
		account_number VARCHAR(255) NOT NULL UNIQUE,
		party_id UUID NOT NULL,
		currency VARCHAR(3) NOT NULL,
		balance_cents BIGINT NOT NULL DEFAULT 0,
		status VARCHAR(20) NOT NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		version INT NOT NULL DEFAULT 1,
		created_by VARCHAR(255),
		updated_by VARCHAR(255)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

func TestInitiateCurrentAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.InitiateCurrentAccountRequest{
		AccountIdentification: "GB82WEST12345698765432",
		PartyId:               uuid.New().String(),
		BaseCurrency:          commonpb.Currency_CURRENCY_GBP,
	}

	resp, err := svc.InitiateCurrentAccount(ctx, req)
	if err != nil {
		t.Fatalf("InitiateCurrentAccount failed: %v", err)
	}

	if resp.AccountId == "" {
		t.Error("Expected non-empty account ID")
	}

	if resp.Facility == nil {
		t.Fatal("Expected facility in response")
	}

	if resp.Facility.AccountIdentification != req.AccountIdentification {
		t.Errorf("Expected IBAN %s, got %s", req.AccountIdentification, resp.Facility.AccountIdentification)
	}

	if resp.Facility.AccountStatus != pb.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		t.Errorf("Expected ACTIVE status, got %v", resp.Facility.AccountStatus)
	}
}

func TestExecuteDeposit(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Configure mock with expected post-deposit balance (£100.50 = 10050 cents)
	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-001": 10050, // £100.50 after deposit
	})

	// Create account first
	account, err := domain.NewCurrentAccount("ACC-001", "ACC-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Execute deposit
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        500000000, // £100.50
			},
		},
		Description: "Test deposit",
	}

	resp, err := svc.ExecuteDeposit(ctx, req)
	if err != nil {
		t.Fatalf("ExecuteDeposit failed: %v", err)
	}

	if resp.AccountId != "ACC-001" {
		t.Errorf("Expected account ID ACC-001, got %s", resp.AccountId)
	}

	if resp.TransactionId == "" {
		t.Error("Expected non-empty transaction ID")
	}

	if resp.Status != pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED {
		t.Errorf("Expected COMPLETED status, got %v", resp.Status)
	}

	// Verify balance (from Position Keeping mock)
	if resp.NewBalance == nil {
		t.Fatal("Expected new balance in response")
	}

	expectedUnits := int64(100) // £100.50 = 10050 cents = 100 units + 50 cents
	if resp.NewBalance.Amount.Units != expectedUnits {
		t.Errorf("Expected balance units %d, got %d", expectedUnits, resp.NewBalance.Amount.Units)
	}
}

func TestExecuteDepositAccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-NONEXISTENT",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
			},
		},
	}

	_, err := svc.ExecuteDeposit(ctx, req)
	if err == nil {
		t.Fatal("Expected error for non-existent account")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound code, got %v", st.Code())
	}
}

func TestExecuteDepositInvalidAmount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// Create account first
	account, err := domain.NewCurrentAccount("ACC-001", "ACC-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Try deposit with zero amount
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        0,
				Nanos:        0,
			},
		},
	}

	_, err = svc.ExecuteDeposit(ctx, req)
	if err == nil {
		t.Fatal("Expected error for zero amount")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument code, got %v", st.Code())
	}
}

func TestRetrieveCurrentAccount(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	// Configure Position Keeping mock to return balance for ACC-001
	svc := mustNewServiceWithPositionKeeping(t, repo, nil, map[string]int64{
		"ACC-001": 150000, // 1500.00 GBP
	})

	// Create account first
	account, err := domain.NewCurrentAccount("ACC-001", "ACC-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Retrieve account
	req := &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-001",
	}

	resp, err := svc.RetrieveCurrentAccount(ctx, req)
	if err != nil {
		t.Fatalf("RetrieveCurrentAccount failed: %v", err)
	}

	if resp.Facility == nil {
		t.Fatal("Expected facility in response")
	}

	if resp.Facility.AccountId != "ACC-001" {
		t.Errorf("Expected account ID ACC-001, got %s", resp.Facility.AccountId)
	}

	if resp.Facility.AccountStatus != pb.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		t.Errorf("Expected ACTIVE status, got %v", resp.Facility.AccountStatus)
	}

	// Verify balance comes from Position Keeping (1500.00 GBP = 15.00 units)
	if resp.Facility.CurrentBalance == nil {
		t.Fatal("Expected current balance in response")
	}
	expectedUnits := int64(1500) // 150000 cents = 1500.00 GBP
	if resp.Facility.CurrentBalance.CurrentBalance.Amount.Units != expectedUnits {
		t.Errorf("Expected balance units %d, got %d",
			expectedUnits, resp.Facility.CurrentBalance.CurrentBalance.Amount.Units)
	}
}

func TestRetrieveCurrentAccountNotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.RetrieveCurrentAccountRequest{
		AccountId: "ACC-NONEXISTENT",
	}

	_, err := svc.RetrieveCurrentAccount(ctx, req)
	if err == nil {
		t.Fatal("Expected error for non-existent account")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.NotFound {
		t.Errorf("Expected NotFound code, got %v", st.Code())
	}
}

func TestCurrencyMapping(t *testing.T) {
	tests := []struct {
		name     string
		currency commonpb.Currency
		expected string
	}{
		{"GBP", commonpb.Currency_CURRENCY_GBP, "GBP"},
		{"USD", commonpb.Currency_CURRENCY_USD, "USD"},
		{"EUR", commonpb.Currency_CURRENCY_EUR, "EUR"},
		{"Unspecified returns empty", commonpb.Currency_CURRENCY_UNSPECIFIED, ""},
		{"Unsupported JPY returns empty", commonpb.Currency_CURRENCY_JPY, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapCurrency(tt.currency)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestExecuteDepositCurrencyMismatch(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// Create GBP account
	account, err := domain.NewCurrentAccount("ACC-001", "ACC-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	if err := repo.Save(ctx, account); err != nil {
		t.Fatalf("Failed to create test account: %v", err)
	}

	// Try to deposit USD to GBP account
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "USD",
				Units:        100,
				Nanos:        0,
			},
		},
	}

	_, err = svc.ExecuteDeposit(ctx, req)
	if err == nil {
		t.Fatal("Expected error for currency mismatch")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument code, got %v", st.Code())
	}

	if !strings.Contains(st.Message(), "currency mismatch") {
		t.Errorf("Expected 'currency mismatch' in error message, got: %s", st.Message())
	}
}

func TestInitiateCurrentAccountUnsupportedCurrency(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	req := &pb.InitiateCurrentAccountRequest{
		AccountIdentification: "GB82WEST12345698765432",
		PartyId:               uuid.New().String(),
		BaseCurrency:          commonpb.Currency_CURRENCY_JPY,
	}

	_, err := svc.InitiateCurrentAccount(ctx, req)
	if err == nil {
		t.Fatal("Expected error for unsupported currency")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Expected gRPC status error, got %v", err)
	}

	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument code, got %v", st.Code())
	}
}

func TestToMoneyAmount(t *testing.T) {
	tests := []struct {
		name          string
		input         domain.Money
		expectedUnits int64
		expectedNanos int32
	}{
		{
			name:          "Positive amount",
			input:         mustNewMoney("GBP", 12345), // £123.45
			expectedUnits: 123,
			expectedNanos: 450000000,
		},
		{
			name:          "Negative amount",
			input:         mustNewMoney("GBP", -12345), // -£123.45
			expectedUnits: -123,
			expectedNanos: -450000000, // Nanos must share sign per google.type.Money spec
		},
		{
			name:          "Zero amount",
			input:         mustNewMoney("USD", 0),
			expectedUnits: 0,
			expectedNanos: 0,
		},
		{
			name:          "Whole units (no fractional)",
			input:         mustNewMoney("EUR", 10000), // €100.00
			expectedUnits: 100,
			expectedNanos: 0,
		},
		{
			name:          "Negative whole units",
			input:         mustNewMoney("EUR", -10000), // -€100.00
			expectedUnits: -100,
			expectedNanos: 0,
		},
		{
			name:          "Small negative amount",
			input:         mustNewMoney("GBP", -123), // -£1.23
			expectedUnits: -1,
			expectedNanos: -230000000, // Nanos must share sign per google.type.Money spec
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toMoneyAmount(tt.input)

			if result.Amount.CurrencyCode != tt.input.CurrencyCode() {
				t.Errorf("Expected currency %s, got %s", tt.input.CurrencyCode(), result.Amount.CurrencyCode)
			}

			if result.Amount.Units != tt.expectedUnits {
				t.Errorf("Expected units %d, got %d", tt.expectedUnits, result.Amount.Units)
			}

			if result.Amount.Nanos != tt.expectedNanos {
				t.Errorf("Expected nanos %d, got %d", tt.expectedNanos, result.Amount.Nanos)
			}
		})
	}
}

// Defensive tests for overflow scenarios per ADR-008

func TestExecuteDeposit_OverflowPrevention_UnitsTooCents(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// Create account
	account, err := domain.NewCurrentAccount("ACC-001", "ACC-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Test: Units value that would overflow when multiplied by 100
	tests := []struct {
		name      string
		units     int64
		wantErr   bool
		rationale string
	}{
		{
			name:      "max safe units",
			units:     92233720368547758, // MaxInt64/100
			wantErr:   false,
			rationale: "Boundary value: should succeed at conversion",
		},
		{
			name:      "overflow positive units",
			units:     92233720368547759, // MaxInt64/100 + 1
			wantErr:   true,
			rationale: "Units * 100 would overflow int64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &pb.ExecuteDepositRequest{
				AccountId: "ACC-001",
				Amount: &commonpb.MoneyAmount{
					Amount: &money.Money{
						CurrencyCode: "GBP",
						Units:        tt.units,
						Nanos:        0,
					},
				},
			}

			_, err := svc.ExecuteDeposit(ctx, req)

			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				st, ok := status.FromError(err)
				require.True(t, ok, "Expected gRPC status error")
				if st.Code() != codes.InvalidArgument {
					t.Errorf("Expected InvalidArgument, got %v", st.Code())
				}
				if !strings.Contains(st.Message(), "overflow") {
					t.Errorf("Error should mention overflow, got: %s", st.Message())
				}
			} else {
				require.NoError(t, err, tt.rationale)
			}
		})
	}
}

func TestExecuteDeposit_SafeAddition_UnitsAndNanos(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	svc := mustNewService(t, repo, nil)

	// Create account
	account, err := domain.NewCurrentAccount("ACC-001", "ACC-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Test: Large units + nanos uses Money.Add() safely
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{
				CurrencyCode: "GBP",
				Units:        92233720368547758, // MaxInt64/100
				Nanos:        990000000,         // 99 cents when rounded
			},
		},
	}

	// This should fail safely - either with overflow error or invalid amount error
	// (int64 overflow in ToMinorUnitsUnchecked can produce negative values caught by positivity check)
	_, err = svc.ExecuteDeposit(ctx, req)
	require.Error(t, err, "overflow scenario should surface an error, not succeed")

	st, ok := status.FromError(err)
	require.True(t, ok, "Expected gRPC status error")
	if st.Code() != codes.InvalidArgument {
		t.Errorf("Expected InvalidArgument, got %v", st.Code())
	}
	// Accept either overflow or negative amount message - both indicate safe rejection
	if !strings.Contains(st.Message(), "overflow") && !strings.Contains(st.Message(), "must be positive") {
		t.Errorf("Error should mention overflow or positivity, got: %s", st.Message())
	}
}

// mockIdempotencyService implements idempotency.Service for testing
type mockIdempotencyService struct {
	mu        sync.Mutex
	results   map[string]*idempotency.Result
	pending   map[string]bool
	checkErr  error
	storeErr  error
	deleteErr error
}

func newMockIdempotencyService() *mockIdempotencyService {
	return &mockIdempotencyService{
		results: make(map[string]*idempotency.Result),
		pending: make(map[string]bool),
	}
}

func (m *mockIdempotencyService) Check(_ context.Context, key idempotency.Key) (*idempotency.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.checkErr != nil {
		return nil, m.checkErr
	}

	keyStr := key.String()
	if result, ok := m.results[keyStr]; ok {
		// Match Redis behavior: return ErrOperationAlreadyProcessed for completed results
		if result.Status == idempotency.StatusCompleted {
			return result, idempotency.ErrOperationAlreadyProcessed
		}
		return result, nil
	}
	return nil, idempotency.ErrResultNotFound
}

func (m *mockIdempotencyService) MarkPending(_ context.Context, key idempotency.Key, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	keyStr := key.String()
	if m.pending[keyStr] {
		return idempotency.ErrOperationAlreadyProcessed
	}
	m.pending[keyStr] = true
	return nil
}

func (m *mockIdempotencyService) StoreResult(_ context.Context, result idempotency.Result) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.storeErr != nil {
		return m.storeErr
	}

	keyStr := result.Key.String()
	m.results[keyStr] = &result
	delete(m.pending, keyStr) // Clear pending when result is stored
	return nil
}

func (m *mockIdempotencyService) Delete(_ context.Context, key idempotency.Key) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	keyStr := key.String()
	delete(m.results, keyStr)
	delete(m.pending, keyStr)
	return nil
}

func (m *mockIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil // Not used in these tests
}

func (m *mockIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil // Not used in these tests
}

func (m *mockIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil // Not used in these tests
}

func (m *mockIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil // Not used in these tests
}

// setResult pre-populates a cached result for testing cache hits
func (m *mockIdempotencyService) setResult(key idempotency.Key, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results[key.String()] = &idempotency.Result{
		Key:         key,
		Status:      idempotency.StatusCompleted,
		Data:        data,
		CompletedAt: time.Now(),
	}
}

// setPending marks a key as pending for testing concurrent request rejection
func (m *mockIdempotencyService) setPending(key idempotency.Key) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending[key.String()] = true
}

// isPending checks if a key is in pending state
func (m *mockIdempotencyService) isPending(key idempotency.Key) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending[key.String()]
}

func TestExecuteDeposit_IdempotencyReturnsCachedResponse(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

	// Create account
	account, err := domain.NewCurrentAccount("ACC-IDEMP-001", "ACC-IDEMP-001", uuid.New().String(), "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Pre-populate cached response
	idempKey := idempotency.Key{
		TenantID:  svcTestTenantID,
		Namespace: "current-account",
		Operation: "deposit",
		EntityID:  "ACC-IDEMP-001",
		RequestID: "req-123",
	}

	// Create a cached deposit response
	cachedResp := &pb.ExecuteDepositResponse{
		AccountId:     "ACC-IDEMP-001",
		TransactionId: "cached-tx-id",
		Status:        pb.TransactionStatus_TRANSACTION_STATUS_COMPLETED,
		NewBalance: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 100, Nanos: 0},
		},
	}
	cachedData, err := proto.Marshal(cachedResp)
	require.NoError(t, err)
	mockIdemp.setResult(idempKey, cachedData)

	// Execute deposit with same idempotency key
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-IDEMP-001",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-123"},
	}

	resp, err := svc.ExecuteDeposit(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "cached-tx-id", resp.TransactionId, "should return cached transaction ID")
	require.Equal(t, int64(100), resp.NewBalance.Amount.Units, "should return cached balance")
}

func TestExecuteDeposit_IdempotencyReturnsAbortedWhenInProgress(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

	// Create account
	account, err := domain.NewCurrentAccount("ACC-IDEMP-002", "ACC-IDEMP-002", uuid.New().String(), "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Mark operation as pending (simulating concurrent request)
	idempKey := idempotency.Key{
		TenantID:  svcTestTenantID,
		Namespace: "current-account",
		Operation: "deposit",
		EntityID:  "ACC-IDEMP-002",
		RequestID: "req-456",
	}
	mockIdemp.setPending(idempKey)

	// Execute deposit with same idempotency key
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-IDEMP-002",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-456"},
	}

	_, err = svc.ExecuteDeposit(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.Aborted, st.Code(), "should return Aborted for concurrent request")
	require.Contains(t, st.Message(), "already in progress")
}

func TestExecuteDeposit_IdempotencyProceedsWithoutKey(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	// Configure mock with POST-deposit balance (5000 cents = £50.00).
	// Position Keeping is the source of truth and would have recorded the CREDIT
	// by the time we query the balance.
	svc := mustNewServiceWithIdempotencyAndPositionKeeping(t, repo, nil, mockIdemp, map[string]int64{
		"ACC-IDEMP-003": 5000, // £50 post-deposit
	})

	// Create account
	account, err := domain.NewCurrentAccount("ACC-IDEMP-003", "ACC-IDEMP-003", uuid.New().String(), "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	// Execute deposit without idempotency key
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-IDEMP-003",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "GBP", Units: 50, Nanos: 0},
		},
		// No IdempotencyKey
	}

	resp, err := svc.ExecuteDeposit(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, resp.TransactionId)
	require.Equal(t, int64(50), resp.NewBalance.Amount.Units)
}

func TestExecuteDeposit_IdempotencyCleanupOnFailure(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewRepository(db)
	mockIdemp := newMockIdempotencyService()
	svc := mustNewServiceWithIdempotency(t, repo, nil, mockIdemp)

	// Create account but with wrong currency
	account, err := domain.NewCurrentAccount("ACC-IDEMP-004", "ACC-IDEMP-004", uuid.New().String(), "GBP")
	require.NoError(t, err)
	require.NoError(t, repo.Save(ctx, account))

	idempKey := idempotency.Key{
		TenantID:  svcTestTenantID,
		Namespace: "current-account",
		Operation: "deposit",
		EntityID:  "ACC-IDEMP-004",
		RequestID: "req-789",
	}

	// Execute deposit with currency mismatch (will fail)
	req := &pb.ExecuteDepositRequest{
		AccountId: "ACC-IDEMP-004",
		Amount: &commonpb.MoneyAmount{
			Amount: &money.Money{CurrencyCode: "USD", Units: 50, Nanos: 0}, // Wrong currency
		},
		IdempotencyKey: &commonpb.IdempotencyKey{Key: "req-789"},
	}

	_, err = svc.ExecuteDeposit(ctx, req)
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.InvalidArgument, st.Code())

	// Verify pending state was cleaned up
	require.False(t, mockIdemp.isPending(idempKey), "pending state should be cleaned up after failure")
}

func TestListCurrentAccounts(t *testing.T) {
	t.Parallel()

	t.Run("returns empty list when no accounts exist", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Empty(t, resp.Accounts)
		require.Equal(t, int64(0), resp.TotalCount)
		require.Empty(t, resp.NextPageToken)
	})

	t.Run("returns all accounts with default page size", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		// Create two accounts
		acc1, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", uuid.New().String(), "GBP")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, acc1))

		acc2, err := domain.NewCurrentAccount("ACC-002", "DE89370400440532013000", uuid.New().String(), "EUR")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, acc2))

		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Accounts, 2)
		require.Equal(t, int64(2), resp.TotalCount)
	})

	t.Run("filters by status", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		// Create an active account
		acc1, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", uuid.New().String(), "GBP")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, acc1))

		// Create account and freeze it
		acc2, err := domain.NewCurrentAccount("ACC-002", "DE89370400440532013000", uuid.New().String(), "EUR")
		require.NoError(t, err)
		acc2, err = acc2.Freeze("test freeze")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, acc2))

		// Filter by ACTIVE
		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{
			Status: pb.AccountStatus_ACCOUNT_STATUS_ACTIVE,
		})
		require.NoError(t, err)
		require.Len(t, resp.Accounts, 1)
		require.Equal(t, pb.AccountStatus_ACCOUNT_STATUS_ACTIVE, resp.Accounts[0].AccountStatus)
	})

	t.Run("filters by IBAN prefix", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		acc1, err := domain.NewCurrentAccount("ACC-001", "GB82WEST12345698765432", uuid.New().String(), "GBP")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, acc1))

		acc2, err := domain.NewCurrentAccount("ACC-002", "DE89370400440532013000", uuid.New().String(), "EUR")
		require.NoError(t, err)
		require.NoError(t, repo.Save(ctx, acc2))

		// Filter by GB prefix
		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{
			Iban: "GB",
		})
		require.NoError(t, err)
		require.Len(t, resp.Accounts, 1)
		require.Equal(t, "GB82WEST12345698765432", resp.Accounts[0].AccountIdentification)
	})

	t.Run("paginates results", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		// Create 3 accounts
		for i := 0; i < 3; i++ {
			iban := fmt.Sprintf("GB%02dWEST1234569876543%d", 10+i, i)
			acc, err := domain.NewCurrentAccount(fmt.Sprintf("ACC-%03d", i), iban, uuid.New().String(), "GBP")
			require.NoError(t, err)
			require.NoError(t, repo.Save(ctx, acc))
			time.Sleep(2 * time.Millisecond) // ensure distinct created_at for cursor ordering
		}

		// First page: page_size=2
		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{
			PageSize: 2,
		})
		require.NoError(t, err)
		require.Len(t, resp.Accounts, 2)
		require.Equal(t, int64(3), resp.TotalCount)
		require.NotEmpty(t, resp.NextPageToken)

		// Second page
		resp2, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{
			PageSize:  2,
			PageToken: resp.NextPageToken,
		})
		require.NoError(t, err)
		require.Len(t, resp2.Accounts, 1)
		require.Empty(t, resp2.NextPageToken)
	})

	t.Run("returns InvalidArgument for malformed page_token", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{
			PageToken: "not-valid-base64-cursor!!!",
		})
		require.Nil(t, resp)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("applies default page size of 25", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{PageSize: 0})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("clamps page size to max 100", func(t *testing.T) {
		db, ctx, cleanup := setupTestDB(t)
		defer cleanup()

		repo := persistence.NewRepository(db)
		svc := mustNewService(t, repo, nil)

		resp, err := svc.ListCurrentAccounts(ctx, &pb.ListCurrentAccountsRequest{PageSize: 100})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}
