package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Test-specific errors for defensive testing scenarios
var (
	errTestRedisConnectionFailed     = errors.New("redis connection failed")
	errTestCannotMarkPending         = errors.New("cannot mark pending")
	errTestDatabaseConnectionLost    = errors.New("database connection lost")
	errTestDatabaseWriteFailed       = errors.New("database write failed")
	errTestRegistryConnectionRefused = errors.New("registry connection refused")
	errTestRegistryShouldNotCall     = errors.New("registry should not be called")
)

// mockEventPublisher is a test double for EventPublisher
type mockEventPublisher struct{}

func (m *mockEventPublisher) Publish(_ context.Context, _ DomainEvent) error {
	return nil
}

func (m *mockEventPublisher) PublishBatch(_ context.Context, _ []DomainEvent) error {
	return nil
}

// TestNewFinancialAccountingService verifies the constructor creates a valid service instance.
func TestNewFinancialAccountingService(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Act
	service, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)

	// Assert
	require.NoError(t, err, "Should create service without error")
	assert.NotNil(t, service, "Service should not be nil")
	assert.NotNil(t, service.repository, "Repository should be injected")
	assert.NotNil(t, service.eventPublisher, "Event publisher should be injected")
	assert.NotNil(t, service.idempotency, "Idempotency service should be injected")
	assert.NotNil(t, service.outboxPublisher, "Outbox publisher should be injected")
	assert.NotNil(t, service.outboxRepo, "Outbox repository should be injected")
}

// TestFinancialAccountingService_ImplementsInterface verifies the service implements the gRPC interface.
func TestFinancialAccountingService_ImplementsInterface(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Act
	service, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	// Assert - compile-time check that service implements the interface
	var _ financialaccountingv1.FinancialAccountingServiceServer = service
}

// TestNewFinancialAccountingService_DefensiveTests verifies nil dependency validation per ADR-0008.
// Rationale: Financial services must validate all dependencies to prevent runtime panics
// that could cause service outages or data corruption.
func TestNewFinancialAccountingService_DefensiveTests(t *testing.T) {
	db := &gorm.DB{}
	validRepo := persistence.NewLedgerRepository(db)
	validEventPub := &mockEventPublisher{}
	validIdempotencySvc := &mockIdempotencyService{}
	validOutboxPublisher := events.NewOutboxPublisher("financial-accounting")
	validOutboxRepo := events.NewPostgresOutboxRepository(db)

	tests := []struct {
		name            string
		repository      *persistence.LedgerRepository
		eventPub        EventPublisher
		idempotencySvc  idempotency.Service
		outboxPublisher *events.OutboxPublisher
		outboxRepo      *events.PostgresOutboxRepository
		wantErr         bool
		wantSentinel    error // Expected sentinel error for errors.Is() verification
		rationale       string
	}{
		// Happy path - covered by TestNewFinancialAccountingService
		{
			name:            "valid dependencies",
			repository:      validRepo,
			eventPub:        validEventPub,
			idempotencySvc:  validIdempotencySvc,
			outboxPublisher: validOutboxPublisher,
			outboxRepo:      validOutboxRepo,
			wantErr:         false,
			wantSentinel:    nil,
			rationale:       "Standard valid initialization with all dependencies",
		},

		// Unhappy paths - nil dependencies (ADR-0008 mandatory tests)
		{
			name:            "nil repository",
			repository:      nil,
			eventPub:        validEventPub,
			idempotencySvc:  validIdempotencySvc,
			outboxPublisher: validOutboxPublisher,
			outboxRepo:      validOutboxRepo,
			wantErr:         true,
			wantSentinel:    ErrRepositoryNil,
			rationale:       "Repository is essential - nil would cause panic on first use",
		},
		{
			name:            "nil event publisher",
			repository:      validRepo,
			eventPub:        nil,
			idempotencySvc:  validIdempotencySvc,
			outboxPublisher: validOutboxPublisher,
			outboxRepo:      validOutboxRepo,
			wantErr:         true,
			wantSentinel:    ErrEventPublisherNil,
			rationale:       "Event publisher is essential - nil would cause panic when publishing events",
		},
		{
			name:            "nil idempotency service",
			repository:      validRepo,
			eventPub:        validEventPub,
			idempotencySvc:  nil,
			outboxPublisher: validOutboxPublisher,
			outboxRepo:      validOutboxRepo,
			wantErr:         true,
			wantSentinel:    ErrIdempotencyServiceNil,
			rationale:       "Idempotency service is essential - nil would cause panic on idempotent operations",
		},
		{
			name:            "nil outbox publisher",
			repository:      validRepo,
			eventPub:        validEventPub,
			idempotencySvc:  validIdempotencySvc,
			outboxPublisher: nil,
			outboxRepo:      validOutboxRepo,
			wantErr:         true,
			wantSentinel:    ErrOutboxPublisherNil,
			rationale:       "Outbox publisher is essential for transactional outbox pattern",
		},
		{
			name:            "nil outbox repository",
			repository:      validRepo,
			eventPub:        validEventPub,
			idempotencySvc:  validIdempotencySvc,
			outboxPublisher: validOutboxPublisher,
			outboxRepo:      nil,
			wantErr:         true,
			wantSentinel:    ErrOutboxRepositoryNil,
			rationale:       "Outbox repository is essential for transactional outbox pattern",
		},

		// Edge case - multiple nil dependencies
		{
			name:            "all dependencies nil",
			repository:      nil,
			eventPub:        nil,
			idempotencySvc:  nil,
			outboxPublisher: nil,
			outboxRepo:      nil,
			wantErr:         true,
			wantSentinel:    ErrRepositoryNil,
			rationale:       "Should error on first nil check (repository)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, err := NewFinancialAccountingService(tt.repository, tt.eventPub, tt.idempotencySvc, tt.outboxPublisher, tt.outboxRepo)
			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				assert.Nil(t, service, "Service should be nil when error occurs")
				// Verify the specific sentinel error using errors.Is()
				assert.ErrorIs(t, err, tt.wantSentinel, "Should return the expected sentinel error")
			} else {
				require.NoError(t, err, tt.rationale)
				assert.NotNil(t, service, tt.rationale)
				assert.NotNil(t, service.repository, "Repository should be injected")
				assert.NotNil(t, service.eventPublisher, "Event publisher should be injected")
				assert.NotNil(t, service.idempotency, "Idempotency service should be injected")
				assert.NotNil(t, service.outboxPublisher, "Outbox publisher should be injected")
				assert.NotNil(t, service.outboxRepo, "Outbox repository should be injected")
			}
		})
	}
}

// mustNewFinancialAccountingService creates a service and fails the test if an error occurs.
// Use this for tests where the service should always be created successfully.
// It creates default outbox publisher and repository for tests that don't need to mock them.
func mustNewFinancialAccountingService(t *testing.T, repo *persistence.LedgerRepository, publisher EventPublisher, idempotencySvc idempotency.Service) *FinancialAccountingService {
	t.Helper()
	db := repo.DB()
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	service, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err, "unexpected error creating service")
	return service
}

// mockIdempotencyService is a test double for idempotency.Service
type mockIdempotencyService struct {
	checkResult  *idempotency.Result
	checkErr     error
	markErr      error
	storeErr     error
	storedResult *idempotency.Result // Captured for verification in tests
}

// Checker interface methods
func (m *mockIdempotencyService) Check(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
	if m.checkErr != nil {
		return m.checkResult, m.checkErr
	}
	return nil, idempotency.ErrResultNotFound
}

func (m *mockIdempotencyService) MarkPending(_ context.Context, _ idempotency.Key, _ time.Duration) error {
	return m.markErr
}

func (m *mockIdempotencyService) StoreResult(_ context.Context, result idempotency.Result) error {
	m.storedResult = &result
	return m.storeErr
}

func (m *mockIdempotencyService) Delete(_ context.Context, _ idempotency.Key) error {
	return nil
}

// Locker interface methods
func (m *mockIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (m *mockIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (m *mockIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (m *mockIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

// testLedgerRepository is a test implementation of repository methods
type testLedgerRepository struct {
	saveErr      error
	getResult    *domain.LedgerPosting
	getErr       error
	updateErr    error
	savedPosting *domain.LedgerPosting // Capture last saved posting
}

func (t *testLedgerRepository) SavePosting(_ context.Context, posting *domain.LedgerPosting) error {
	t.savedPosting = posting
	return t.saveErr
}

func (t *testLedgerRepository) GetPosting(_ context.Context, _ uuid.UUID) (*domain.LedgerPosting, error) {
	return t.getResult, t.getErr
}

func (t *testLedgerRepository) UpdatePosting(_ context.Context, posting *domain.LedgerPosting) error {
	if t.updateErr != nil {
		return t.updateErr
	}
	t.savedPosting = posting
	return nil
}

func (t *testLedgerRepository) GetPostingsByBookingLogID(_ context.Context, _ uuid.UUID) ([]*domain.LedgerPosting, error) {
	return nil, nil
}

func (t *testLedgerRepository) SavePostingsInTransaction(_ context.Context, _ []*domain.LedgerPosting) error {
	return nil
}

// testFinancialAccountingService wraps the real service but allows injecting test repository
type testFinancialAccountingService struct {
	testRepo       *testLedgerRepository
	eventPublisher EventPublisher
	idempotency    idempotency.Service
}

func (s *testFinancialAccountingService) CaptureLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.CaptureLedgerPostingRequest,
) (*financialaccountingv1.CaptureLedgerPostingResponse, error) {
	// Temporarily create a real service with a wrapper repository that uses our test methods
	// Since we can't easily mock the concrete repository type, we'll need to work around it

	// Check idempotency
	var idempotencyKey idempotency.Key
	if req.IdempotencyKey != nil && req.IdempotencyKey.Key != "" {
		idempotencyKey = idempotency.Key{
			Namespace: "financial-accounting",
			Operation: "capture-posting",
			EntityID:  req.GetFinancialBookingLogId(),
			RequestID: req.IdempotencyKey.Key,
		}

		result, err := s.idempotency.Check(ctx, idempotencyKey)
		if err != nil && !errors.Is(err, idempotency.ErrResultNotFound) {
			if errors.Is(err, idempotency.ErrOperationAlreadyProcessed) {
				if result != nil && result.Status == idempotency.StatusCompleted {
					return nil, status.Error(codes.AlreadyExists, "request with this idempotency key already processed")
				}
			}
			return nil, status.Errorf(codes.Internal, "failed to check idempotency: %v", err)
		}

		if err := s.idempotency.MarkPending(ctx, idempotencyKey, 3600*time.Second); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to mark operation as pending: %v", err)
		}
	}

	// Parse and validate - same as real service
	bookingLogID, err := parseUUID(req.GetFinancialBookingLogId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid financial_booking_log_id: %v", err)
	}

	postingAmount, err := fromProtoInstrumentAmount(req.GetPostingAmount())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting_amount: %v", err)
	}

	if req.PostingDirection == commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "posting_direction must be specified")
	}
	direction := fromProtoPostingDirection(req.PostingDirection)

	if req.AccountId == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}

	if req.ValueDate == nil {
		return nil, status.Error(codes.InvalidArgument, "value_date is required")
	}
	valueDate := req.ValueDate.AsTime()

	correlationID := ""
	if req.IdempotencyKey != nil {
		correlationID = req.IdempotencyKey.Key
	}

	posting, err := domain.NewLedgerPosting(
		bookingLogID,
		direction,
		postingAmount,
		req.AccountId,
		valueDate,
		correlationID,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid posting data: %v", err)
	}

	// Use test repository
	if err := s.testRepo.SavePosting(ctx, posting); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to save posting: %v", err)
	}

	response := &financialaccountingv1.CaptureLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}

	return response, nil
}

func (s *testFinancialAccountingService) UpdateLedgerPosting(
	ctx context.Context,
	req *financialaccountingv1.UpdateLedgerPostingRequest,
) (*financialaccountingv1.UpdateLedgerPostingResponse, error) {
	postingID, err := parseUUID(req.GetId())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid id: %v", err)
	}

	if req.Status == commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument, "status must be specified")
	}
	newStatus := fromProtoTransactionStatus(req.Status)

	posting, err := s.testRepo.GetPosting(ctx, postingID)
	if err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve posting: %v", err)
	}

	postingResult := req.PostingResult
	if postingResult == "" {
		postingResult = posting.PostingResult
	}

	switch newStatus {
	case domain.TransactionStatusPosted:
		if err := posting.Post(postingResult); err != nil {
			if errors.Is(err, domain.ErrAlreadyPosted) {
				return nil, status.Error(codes.FailedPrecondition, "posting already posted")
			}
			return nil, status.Errorf(codes.InvalidArgument, "cannot post: %v", err)
		}
	case domain.TransactionStatusFailed:
		if err := posting.Fail(postingResult); err != nil {
			if errors.Is(err, domain.ErrCannotFailPosted) {
				return nil, status.Error(codes.FailedPrecondition, "cannot fail a posted transaction")
			}
			return nil, status.Errorf(codes.InvalidArgument, "cannot fail: %v", err)
		}
	case domain.TransactionStatusPending:
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusCancelled:
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	case domain.TransactionStatusReversed:
		posting.Status = newStatus
		if postingResult != "" {
			posting.PostingResult = postingResult
		}
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unsupported status: %v", newStatus)
	}

	if err := s.testRepo.UpdatePosting(ctx, posting); err != nil {
		if errors.Is(err, persistence.ErrPostingNotFound) {
			return nil, status.Errorf(codes.NotFound, "ledger posting not found: %s", postingID)
		}
		return nil, status.Errorf(codes.Internal, "failed to update posting: %v", err)
	}

	return &financialaccountingv1.UpdateLedgerPostingResponse{
		LedgerPosting: toProtoLedgerPosting(posting),
	}, nil
}

// TestCaptureLedgerPosting_DefensiveTests tests CaptureLedgerPosting following ADR-0008 defensive testing standards.
// Tests cover happy paths, unhappy paths, edge cases, and negative scenarios per financial domain requirements.
func TestCaptureLedgerPosting_DefensiveTests(t *testing.T) {
	validBookingLogID := uuid.New()
	validAccountID := "ACC-123"
	validValueDate := time.Now()

	tests := []struct {
		name      string
		req       *financialaccountingv1.CaptureLedgerPostingRequest
		repo      *testLedgerRepository
		mockIdem  *mockIdempotencyService
		wantErr   bool
		wantCode  codes.Code
		rationale string
	}{
		// Happy path tests
		{
			name: "valid posting creation with all required fields",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100.5",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key:        "test-key-001",
					TtlSeconds: 3600,
				},
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "Standard valid posting creation should succeed and return posting details",
		},
		{
			name: "valid posting with credit direction",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "50",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "Credit direction is equally valid as debit for double-entry bookkeeping",
		},
		{
			name: "valid posting without idempotency key",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "Idempotency key is optional - non-idempotent requests should still work",
		},

		// Unhappy paths - invalid booking log ID
		{
			name: "empty booking log ID",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: "",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Empty booking log ID must be rejected - postings must be associated with a booking log",
		},
		{
			name: "malformed booking log UUID",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: "not-a-uuid",
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Malformed UUIDs must be rejected to prevent data corruption",
		},

		// Unhappy paths - invalid amount
		{
			name: "zero amount",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "0",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Zero amount postings are meaningless in double-entry bookkeeping and must be rejected",
		},
		{
			name: "negative amount",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "-100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Negative amounts are invalid - use posting direction (debit/credit) instead",
		},
		{
			name: "nil posting amount",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount:         nil,
				AccountId:             validAccountID,
				ValueDate:             timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Nil amount must be rejected - amount is a required field for postings",
		},

		// Unhappy paths - invalid direction
		{
			name: "unspecified posting direction",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_UNSPECIFIED,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Direction must be specified - ambiguous postings violate double-entry principles",
		},

		// Unhappy paths - invalid account ID
		{
			name: "empty account ID",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: "",
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Empty account ID must be rejected - postings must be associated with an account",
		},

		// Unhappy paths - invalid value date
		{
			name: "nil value date",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: nil,
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Value date is required for financial postings to determine when the transaction takes effect",
		},

		// Unhappy paths - idempotency scenarios
		{
			name: "idempotency key already processed",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key: "duplicate-key",
				},
			},
			repo: &testLedgerRepository{},
			mockIdem: &mockIdempotencyService{
				checkResult: &idempotency.Result{
					Status: idempotency.StatusCompleted,
				},
				checkErr: idempotency.ErrOperationAlreadyProcessed,
			},
			wantErr:   true,
			wantCode:  codes.AlreadyExists,
			rationale: "Duplicate idempotency keys must return AlreadyExists to prevent duplicate postings",
		},
		{
			name: "idempotency check internal error",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key: "test-key",
				},
			},
			repo: &testLedgerRepository{},
			mockIdem: &mockIdempotencyService{
				checkErr: errTestRedisConnectionFailed,
			},
			wantErr:   true,
			wantCode:  codes.Internal,
			rationale: "Infrastructure failures during idempotency checks must fail safely to prevent data loss",
		},
		{
			name: "idempotency mark pending fails",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key: "test-key",
				},
			},
			repo: &testLedgerRepository{},
			mockIdem: &mockIdempotencyService{
				markErr: errTestCannotMarkPending,
			},
			wantErr:   true,
			wantCode:  codes.Internal,
			rationale: "Failure to mark pending prevents duplicate processing detection and must fail safely",
		},

		// Edge cases - very large amounts
		{
			name: "very large amount (near int64 max)",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "9223372036854775",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "System should handle very large amounts gracefully for institutional transactions",
		},
		{
			name: "minimum valid amount (0.01)",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "0.01",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "Even fractional currency units are valid postings and must be supported",
		},

		// Edge cases - various date scenarios
		{
			name: "future value date",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(time.Now().Add(30 * 24 * time.Hour)),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "Future value dates are valid for scheduled transactions and forward dating",
		},
		{
			name: "past value date",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(time.Now().Add(-365 * 24 * time.Hour)),
			},
			repo:      &testLedgerRepository{},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   false,
			rationale: "Past value dates are valid for backdated corrections and historical transactions",
		},

		// Unhappy paths - repository errors
		{
			name: "repository save fails",
			req: &financialaccountingv1.CaptureLedgerPostingRequest{
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
			},
			repo: &testLedgerRepository{
				saveErr: errTestDatabaseConnectionLost,
			},
			mockIdem:  &mockIdempotencyService{},
			wantErr:   true,
			wantCode:  codes.Internal,
			rationale: "Database failures must be surfaced as internal errors to allow client retry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			service := &testFinancialAccountingService{
				testRepo:       tt.repo,
				eventPublisher: &mockEventPublisher{},
				idempotency:    tt.mockIdem,
			}

			// Act
			resp, err := service.CaptureLedgerPosting(context.Background(), tt.req)

			// Assert
			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				st, ok := status.FromError(err)
				require.True(t, ok, "error should be a gRPC status error")
				assert.Equal(t, tt.wantCode, st.Code(), tt.rationale)
				assert.Nil(t, resp, "response should be nil on error")
			} else {
				require.NoError(t, err, tt.rationale)
				require.NotNil(t, resp, tt.rationale)
				assert.NotNil(t, resp.LedgerPosting, "response should contain ledger posting")
				assert.NotEmpty(t, resp.LedgerPosting.Id, "posting should have an ID")
				assert.Equal(t, tt.req.FinancialBookingLogId, resp.LedgerPosting.FinancialBookingLogId)
				assert.Equal(t, tt.req.PostingDirection, resp.LedgerPosting.PostingDirection)
				assert.Equal(t, tt.req.AccountId, resp.LedgerPosting.AccountId)
				assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, resp.LedgerPosting.Status)
			}
		})
	}
}

// TestUpdateLedgerPosting_DefensiveTests tests UpdateLedgerPosting following ADR-0008 defensive testing standards.
// Tests cover happy paths, unhappy paths, edge cases, and state transition validation.
func TestUpdateLedgerPosting_DefensiveTests(t *testing.T) {
	validPostingID := uuid.New()
	validBookingLogID := uuid.New()
	validAccountID := "ACC-123"
	validValueDate := time.Now()

	// Helper to create a posting in a specific state
	createPosting := func(status domain.TransactionStatus) *domain.LedgerPosting {
		gbpInstrument := domain.MustCurrencyToInstrument(domain.CurrencyGBP)
		amount := domain.NewMoney(decimal.NewFromInt(100), gbpInstrument)
		posting := &domain.LedgerPosting{
			ID:                    validPostingID,
			FinancialBookingLogID: validBookingLogID,
			Direction:             domain.PostingDirectionDebit,
			Amount:                amount,
			AccountID:             validAccountID,
			ValueDate:             validValueDate,
			Status:                status,
			PostingResult:         "",
			CorrelationID:         "",
			CreatedAt:             time.Now(),
		}
		return posting
	}

	tests := []struct {
		name      string
		req       *financialaccountingv1.UpdateLedgerPostingRequest
		repo      *testLedgerRepository
		wantErr   bool
		wantCode  codes.Code
		rationale string
	}{
		// Happy path tests - valid transitions
		{
			name: "valid update: PENDING to POSTED",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "Posted successfully",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Standard workflow: posting moves from pending to posted when successfully processed",
		},
		{
			name: "valid update: PENDING to FAILED",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
				PostingResult: "Insufficient funds",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Failed transactions are valid outcomes - system must handle posting failures gracefully",
		},
		{
			name: "valid update: status change with posting result",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "Ledger entry created: 12345",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Posting result provides audit trail and troubleshooting information",
		},
		{
			name: "valid update: PENDING to CANCELLED",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED,
				PostingResult: "User cancelled transaction",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Cancellation is a valid operation for pending transactions",
		},

		// Unhappy paths - invalid posting ID
		{
			name: "empty posting ID",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            "",
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "test",
			},
			repo:      &testLedgerRepository{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Empty posting ID must be rejected - cannot update without identifying the target",
		},
		{
			name: "malformed posting UUID",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            "not-a-uuid",
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "test",
			},
			repo:      &testLedgerRepository{},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Malformed UUIDs must be rejected to prevent incorrect updates",
		},
		{
			name: "posting not found",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            uuid.New().String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "test",
			},
			repo: &testLedgerRepository{
				getErr: persistence.ErrPostingNotFound,
			},
			wantErr:   true,
			wantCode:  codes.NotFound,
			rationale: "Non-existent postings must return NotFound for clear client feedback",
		},

		// Unhappy paths - invalid status
		{
			name: "unspecified status",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_UNSPECIFIED,
				PostingResult: "test",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   true,
			wantCode:  codes.InvalidArgument,
			rationale: "Unspecified status is ambiguous and must be rejected",
		},

		// Unhappy paths - invalid state transitions
		{
			name: "invalid transition: POSTED to FAILED",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED,
				PostingResult: "Attempting to fail already posted",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPosted),
			},
			wantErr:   true,
			wantCode:  codes.FailedPrecondition,
			rationale: "Cannot fail a posted transaction - this violates accounting immutability principles",
		},
		{
			name: "invalid transition: attempting to post already posted",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "Re-posting",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPosted),
			},
			wantErr:   true,
			wantCode:  codes.FailedPrecondition,
			rationale: "Idempotency check: attempting to post an already posted transaction should fail gracefully",
		},

		// Edge cases - update with empty posting result
		{
			name: "update without posting result preserves existing",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "",
			},
			repo: &testLedgerRepository{
				getResult: func() *domain.LedgerPosting {
					posting := createPosting(domain.TransactionStatusPending)
					posting.PostingResult = "existing result"
					return posting
				}(),
			},
			wantErr:   false,
			rationale: "Empty posting result should preserve existing result, not overwrite with empty string",
		},

		// Edge cases - various status transitions
		{
			name: "valid transition: PENDING to REVERSED",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_REVERSED,
				PostingResult: "Reversal transaction created",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Reversal is a valid accounting operation for correcting errors",
		},

		// Unhappy paths - repository errors
		{
			name: "repository get fails",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "test",
			},
			repo: &testLedgerRepository{
				getErr: errTestDatabaseConnectionLost,
			},
			wantErr:   true,
			wantCode:  codes.Internal,
			rationale: "Database read failures must be surfaced as internal errors",
		},
		{
			name: "repository update fails",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "test",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
				updateErr: errTestDatabaseWriteFailed,
			},
			wantErr:   true,
			wantCode:  codes.Internal,
			rationale: "Database write failures must be surfaced to allow client retry",
		},
		{
			name: "repository update returns not found",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "test",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
				updateErr: persistence.ErrPostingNotFound,
			},
			wantErr:   true,
			wantCode:  codes.NotFound,
			rationale: "Concurrent deletion: posting existed during GET but was deleted before UPDATE",
		},

		// Edge cases - posting result variations
		{
			name: "valid update with very long posting result",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:     validPostingID.String(),
				Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "Very detailed posting result with comprehensive information about the transaction processing: " +
					"ledger entry 123456, account balance after posting: 10000.00 GBP, timestamp: 2024-01-01T00:00:00Z, " +
					"processed by: system-service, correlation-id: abc-def-ghi-jkl, additional metadata follows...",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Long posting results containing audit information should be supported",
		},
		{
			name: "valid update with special characters in posting result",
			req: &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:            validPostingID.String(),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "Posting €100.00 with 'quotes' and \"double quotes\" and symbols: @#$%^&*()",
			},
			repo: &testLedgerRepository{
				getResult: createPosting(domain.TransactionStatusPending),
			},
			wantErr:   false,
			rationale: "Special characters in posting results should be handled correctly for internationalization",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			service := &testFinancialAccountingService{
				testRepo:       tt.repo,
				eventPublisher: &mockEventPublisher{},
				idempotency:    &mockIdempotencyService{},
			}

			// Act
			resp, err := service.UpdateLedgerPosting(context.Background(), tt.req)

			// Assert
			if tt.wantErr {
				require.Error(t, err, tt.rationale)
				st, ok := status.FromError(err)
				require.True(t, ok, "error should be a gRPC status error")
				assert.Equal(t, tt.wantCode, st.Code(), tt.rationale)
				assert.Nil(t, resp, "response should be nil on error")
			} else {
				require.NoError(t, err, tt.rationale)
				require.NotNil(t, resp, tt.rationale)
				assert.NotNil(t, resp.LedgerPosting, "response should contain ledger posting")
				assert.Equal(t, tt.req.Id, resp.LedgerPosting.Id)

				// Verify status was updated
				expectedStatus := tt.req.Status
				assert.Equal(t, expectedStatus, resp.LedgerPosting.Status, "status should be updated")

				// Verify posting result handling
				if tt.req.PostingResult != "" {
					assert.NotEmpty(t, resp.LedgerPosting.PostingResult, "posting result should be set")
				}
			}
		})
	}
}

// TestCaptureLedgerPosting_IdempotencyResponseSerialization tests the protobuf serialization
// and deserialization of idempotent responses per tech-debt-cleanup#2.
func TestCaptureLedgerPosting_IdempotencyResponseSerialization(t *testing.T) {
	validBookingLogID := uuid.New()
	validAccountID := "ACC-123"
	validValueDate := time.Now()

	t.Run("cached response is deserialized and returned for duplicate request", func(t *testing.T) {
		// Arrange - Create a properly serialized cached response
		cachedPostingID := uuid.New()
		cachedResponse := &financialaccountingv1.CaptureLedgerPostingResponse{
			LedgerPosting: &financialaccountingv1.LedgerPosting{
				Id:                    cachedPostingID.String(),
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100.5",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId: validAccountID,
				ValueDate: timestamppb.New(validValueDate),
				Status:    commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
				CreatedAt: timestamppb.Now(),
			},
		}

		// Serialize the response just like the production code does
		cachedData, err := proto.Marshal(cachedResponse)
		require.NoError(t, err, "serialization should succeed")
		require.NotEmpty(t, cachedData, "serialized data should not be empty")

		// Set up the mock to return the cached result
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   cachedData,
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}

		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: validBookingLogID.String(),
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &quantityv1.InstrumentAmount{
				Amount:         "100.5",
				InstrumentCode: "GBP",
				Version:        1,
			},
			AccountId: validAccountID,
			ValueDate: timestamppb.New(validValueDate),
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "duplicate-key",
			},
		}

		// Act
		resp, err := service.CaptureLedgerPosting(context.Background(), req)

		// Assert - Should return the cached response, not an error
		require.NoError(t, err, "should return cached response without error")
		require.NotNil(t, resp, "response should not be nil")
		require.NotNil(t, resp.LedgerPosting, "ledger posting should not be nil")
		assert.Equal(t, cachedPostingID.String(), resp.LedgerPosting.Id, "should return the cached posting ID")
		assert.Equal(t, validBookingLogID.String(), resp.LedgerPosting.FinancialBookingLogId)
		assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, resp.LedgerPosting.PostingDirection)
	})

	t.Run("corrupted cached data returns AlreadyExists error", func(t *testing.T) {
		// Arrange - Use invalid protobuf data
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   []byte("invalid-protobuf-garbage-data"),
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}

		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: validBookingLogID.String(),
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "GBP",
				Version:        1,
			},
			AccountId: validAccountID,
			ValueDate: timestamppb.New(validValueDate),
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "corrupted-cache-key",
			},
		}

		// Act
		resp, err := service.CaptureLedgerPosting(context.Background(), req)

		// Assert - Should return AlreadyExists error when deserialization fails
		require.Error(t, err, "should return error for corrupted cache")
		require.Nil(t, resp, "response should be nil")
		st, ok := status.FromError(err)
		require.True(t, ok, "should be a gRPC status error")
		assert.Equal(t, codes.AlreadyExists, st.Code(), "should return AlreadyExists for corrupted cache")
		assert.Contains(t, st.Message(), "already processed", "message should indicate duplicate processing")
	})

	t.Run("empty cached data returns AlreadyExists error", func(t *testing.T) {
		// Arrange - Empty data but completed status
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   nil, // No cached data
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}

		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: validBookingLogID.String(),
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &quantityv1.InstrumentAmount{
				Amount:         "100",
				InstrumentCode: "GBP",
				Version:        1,
			},
			AccountId: validAccountID,
			ValueDate: timestamppb.New(validValueDate),
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "empty-cache-key",
			},
		}

		// Act
		resp, err := service.CaptureLedgerPosting(context.Background(), req)

		// Assert - Should return AlreadyExists error when no cached data
		require.Error(t, err, "should return error for empty cache")
		require.Nil(t, resp, "response should be nil")
		st, ok := status.FromError(err)
		require.True(t, ok, "should be a gRPC status error")
		assert.Equal(t, codes.AlreadyExists, st.Code(), "should return AlreadyExists for empty cache")
	})
}

// TestCaptureLedgerPosting_IdempotencySerializationRoundTrip verifies the full round-trip
// of response serialization by using proto.Marshal and proto.Unmarshal directly.
func TestCaptureLedgerPosting_IdempotencySerializationRoundTrip(t *testing.T) {
	t.Run("protobuf serialization round-trip preserves all fields", func(t *testing.T) {
		// Arrange - Create a response with all fields populated
		postingID := uuid.New()
		bookingLogID := uuid.New()
		now := time.Now()

		originalResponse := &financialaccountingv1.CaptureLedgerPostingResponse{
			LedgerPosting: &financialaccountingv1.LedgerPosting{
				Id:                    postingID.String(),
				FinancialBookingLogId: bookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_CREDIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "999.123",
					InstrumentCode: "EUR",
					Version:        1,
				},
				AccountId:     "ACC-456",
				ValueDate:     timestamppb.New(now),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
				PostingResult: "test-result",
				CreatedAt:     timestamppb.New(now),
			},
		}

		// Act - Serialize
		data, err := proto.Marshal(originalResponse)
		require.NoError(t, err, "serialization should succeed")
		require.NotEmpty(t, data, "serialized data should not be empty")

		// Act - Deserialize
		var deserializedResponse financialaccountingv1.CaptureLedgerPostingResponse
		err = proto.Unmarshal(data, &deserializedResponse)
		require.NoError(t, err, "deserialization should succeed")

		// Assert - All fields preserved
		require.NotNil(t, deserializedResponse.LedgerPosting, "posting should not be nil")
		assert.Equal(t, postingID.String(), deserializedResponse.LedgerPosting.Id)
		assert.Equal(t, bookingLogID.String(), deserializedResponse.LedgerPosting.FinancialBookingLogId)
		assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_CREDIT, deserializedResponse.LedgerPosting.PostingDirection)
		assert.Equal(t, "EUR", deserializedResponse.LedgerPosting.PostingAmount.InstrumentCode)
		assert.Equal(t, "999.123", deserializedResponse.LedgerPosting.PostingAmount.Amount)
		assert.Equal(t, "ACC-456", deserializedResponse.LedgerPosting.AccountId)
		assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, deserializedResponse.LedgerPosting.Status)
		assert.Equal(t, "test-result", deserializedResponse.LedgerPosting.PostingResult)
	})

	t.Run("protobuf serialization handles empty response gracefully", func(t *testing.T) {
		// Arrange - Empty response
		originalResponse := &financialaccountingv1.CaptureLedgerPostingResponse{
			LedgerPosting: &financialaccountingv1.LedgerPosting{},
		}

		// Act - Serialize
		data, err := proto.Marshal(originalResponse)
		require.NoError(t, err, "serialization of empty response should succeed")

		// Act - Deserialize
		var deserializedResponse financialaccountingv1.CaptureLedgerPostingResponse
		err = proto.Unmarshal(data, &deserializedResponse)
		require.NoError(t, err, "deserialization of empty response should succeed")

		// Assert
		assert.NotNil(t, deserializedResponse.LedgerPosting, "posting should not be nil")
	})
}

// TestUpdateLedgerPosting_IdempotencyRequired tests that idempotency key is required for UpdateLedgerPosting.
// Per task 14: Add idempotency protection to state-machine update operations.
func TestUpdateLedgerPosting_IdempotencyRequired(t *testing.T) {
	validPostingID := uuid.New()

	tests := []struct {
		name           string
		idempotencyKey *commonv1.IdempotencyKey
		wantCode       codes.Code
		rationale      string
	}{
		{
			name:           "nil idempotency key",
			idempotencyKey: nil,
			wantCode:       codes.InvalidArgument,
			rationale:      "State-machine mutations require idempotency to prevent duplicate transitions",
		},
		{
			name:           "empty idempotency key",
			idempotencyKey: &commonv1.IdempotencyKey{Key: ""},
			wantCode:       codes.InvalidArgument,
			rationale:      "Empty key is equivalent to no key - must be rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			repo := persistence.NewLedgerRepository(&gorm.DB{})
			publisher := &mockEventPublisher{}
			mockIdem := &mockIdempotencyService{}
			service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

			req := &financialaccountingv1.UpdateLedgerPostingRequest{
				Id:             validPostingID.String(),
				Status:         commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult:  "test",
				IdempotencyKey: tt.idempotencyKey,
			}

			// Act
			resp, err := service.UpdateLedgerPosting(context.Background(), req)

			// Assert
			require.Error(t, err, tt.rationale)
			require.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.wantCode, st.Code(), tt.rationale)
			assert.Contains(t, st.Message(), "idempotency_key is required")
		})
	}
}

// TestUpdateLedgerPosting_IdempotencyCaching tests cached response handling for UpdateLedgerPosting.
func TestUpdateLedgerPosting_IdempotencyCaching(t *testing.T) {
	validPostingID := uuid.New()
	validBookingLogID := uuid.New()
	now := time.Now()

	t.Run("cached response returned for duplicate request", func(t *testing.T) {
		// Arrange - Create cached response
		cachedResponse := &financialaccountingv1.UpdateLedgerPostingResponse{
			LedgerPosting: &financialaccountingv1.LedgerPosting{
				Id:                    validPostingID.String(),
				FinancialBookingLogId: validBookingLogID.String(),
				PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
				PostingAmount: &quantityv1.InstrumentAmount{
					Amount:         "100",
					InstrumentCode: "GBP",
					Version:        1,
				},
				AccountId:     "ACC-123",
				ValueDate:     timestamppb.New(now),
				Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				PostingResult: "Posted successfully",
				CreatedAt:     timestamppb.New(now),
			},
		}
		cachedData, err := proto.Marshal(cachedResponse)
		require.NoError(t, err)

		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   cachedData,
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateLedgerPostingRequest{
			Id:            validPostingID.String(),
			Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			PostingResult: "Posted successfully",
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "duplicate-update-key",
			},
		}

		// Act
		resp, err := service.UpdateLedgerPosting(context.Background(), req)

		// Assert
		require.NoError(t, err, "should return cached response without error")
		require.NotNil(t, resp)
		assert.Equal(t, validPostingID.String(), resp.LedgerPosting.Id)
		assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.LedgerPosting.Status)
	})

	t.Run("corrupted cached data returns AlreadyExists", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   []byte("invalid-protobuf-data"),
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateLedgerPostingRequest{
			Id:            validPostingID.String(),
			Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			PostingResult: "test",
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "corrupted-key",
			},
		}

		// Act
		resp, err := service.UpdateLedgerPosting(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("empty cached data returns AlreadyExists", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   nil,
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateLedgerPostingRequest{
			Id:            validPostingID.String(),
			Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			PostingResult: "test",
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "empty-data-key",
			},
		}

		// Act
		resp, err := service.UpdateLedgerPosting(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("idempotency check internal error returns Internal", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			checkErr: errTestRedisConnectionFailed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateLedgerPostingRequest{
			Id:            validPostingID.String(),
			Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			PostingResult: "test",
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "error-key",
			},
		}

		// Act
		resp, err := service.UpdateLedgerPosting(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("mark pending failure returns Internal", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			markErr: errTestCannotMarkPending,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateLedgerPostingRequest{
			Id:            validPostingID.String(),
			Status:        commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			PostingResult: "test",
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "pending-fail-key",
			},
		}

		// Act
		resp, err := service.UpdateLedgerPosting(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

// TestUpdateFinancialBookingLog_IdempotencyRequired tests that idempotency key is required for UpdateFinancialBookingLog.
// Per task 14: Add idempotency protection to state-machine update operations.
func TestUpdateFinancialBookingLog_IdempotencyRequired(t *testing.T) {
	validBookingLogID := uuid.New()

	tests := []struct {
		name           string
		idempotencyKey *commonv1.IdempotencyKey
		wantCode       codes.Code
		rationale      string
	}{
		{
			name:           "nil idempotency key",
			idempotencyKey: nil,
			wantCode:       codes.InvalidArgument,
			rationale:      "State-machine mutations require idempotency to prevent duplicate transitions",
		},
		{
			name:           "empty idempotency key",
			idempotencyKey: &commonv1.IdempotencyKey{Key: ""},
			wantCode:       codes.InvalidArgument,
			rationale:      "Empty key is equivalent to no key - must be rejected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			repo := persistence.NewLedgerRepository(&gorm.DB{})
			publisher := &mockEventPublisher{}
			mockIdem := &mockIdempotencyService{}
			service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

			req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
				Id:                   validBookingLogID.String(),
				Status:               commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				ChartOfAccountsRules: "GAAP-2024",
				IdempotencyKey:       tt.idempotencyKey,
			}

			// Act
			resp, err := service.UpdateFinancialBookingLog(context.Background(), req)

			// Assert
			require.Error(t, err, tt.rationale)
			require.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tt.wantCode, st.Code(), tt.rationale)
			assert.Contains(t, st.Message(), "idempotency_key is required")
		})
	}
}

// TestUpdateFinancialBookingLog_IdempotencyCaching tests cached response handling for UpdateFinancialBookingLog.
func TestUpdateFinancialBookingLog_IdempotencyCaching(t *testing.T) {
	validBookingLogID := uuid.New()
	now := time.Now()

	t.Run("cached response returned for duplicate request", func(t *testing.T) {
		// Arrange - Create cached response
		cachedResponse := &financialaccountingv1.UpdateFinancialBookingLogResponse{
			FinancialBookingLog: &financialaccountingv1.FinancialBookingLog{
				Id:                      validBookingLogID.String(),
				FinancialAccountType:    "CURRENT",
				ProductServiceReference: "DEPOSIT-001",
				BusinessUnitReference:   "RETAIL-UK",
				ChartOfAccountsRules:    "GAAP-2024",
				BaseInstrumentCode:      "GBP",
				Status:                  commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
				CreatedAt:               timestamppb.New(now),
				UpdatedAt:               timestamppb.New(now),
			},
		}
		cachedData, err := proto.Marshal(cachedResponse)
		require.NoError(t, err)

		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   cachedData,
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     validBookingLogID.String(),
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "duplicate-update-booking-log-key",
			},
		}

		// Act
		resp, err := service.UpdateFinancialBookingLog(context.Background(), req)

		// Assert
		require.NoError(t, err, "should return cached response without error")
		require.NotNil(t, resp)
		assert.Equal(t, validBookingLogID.String(), resp.FinancialBookingLog.Id)
		assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED, resp.FinancialBookingLog.Status)
	})

	t.Run("corrupted cached data returns AlreadyExists", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   []byte("invalid-protobuf-data"),
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     validBookingLogID.String(),
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "corrupted-booking-log-key",
			},
		}

		// Act
		resp, err := service.UpdateFinancialBookingLog(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("empty cached data returns AlreadyExists", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			checkResult: &idempotency.Result{
				Status: idempotency.StatusCompleted,
				Data:   nil,
			},
			checkErr: idempotency.ErrOperationAlreadyProcessed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     validBookingLogID.String(),
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "empty-data-booking-log-key",
			},
		}

		// Act
		resp, err := service.UpdateFinancialBookingLog(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("idempotency check internal error returns Internal", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			checkErr: errTestRedisConnectionFailed,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     validBookingLogID.String(),
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "error-booking-log-key",
			},
		}

		// Act
		resp, err := service.UpdateFinancialBookingLog(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("mark pending failure returns Internal", func(t *testing.T) {
		mockIdem := &mockIdempotencyService{
			markErr: errTestCannotMarkPending,
		}
		repo := persistence.NewLedgerRepository(&gorm.DB{})
		publisher := &mockEventPublisher{}
		service := mustNewFinancialAccountingService(t, repo, publisher, mockIdem)

		req := &financialaccountingv1.UpdateFinancialBookingLogRequest{
			Id:     validBookingLogID.String(),
			Status: commonv1.TransactionStatus_TRANSACTION_STATUS_POSTED,
			IdempotencyKey: &commonv1.IdempotencyKey{
				Key: "pending-fail-booking-log-key",
			},
		}

		// Act
		resp, err := service.UpdateFinancialBookingLog(context.Background(), req)

		// Assert
		require.Error(t, err)
		require.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

// TestExtractUserFromContext tests the auth context extraction helper function.
// This ensures correct user attribution for audit events when auth context is present or absent.
func TestExtractUserFromContext(t *testing.T) {
	t.Run("returns user ID when auth context is present", func(t *testing.T) {
		// Arrange - create context with user ID using auth package's context key
		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-12345")

		// Act
		result := extractUserFromContext(ctx)

		// Assert
		assert.Equal(t, "user-12345", result)
	})

	t.Run("returns system when auth context is missing", func(t *testing.T) {
		// Arrange - empty context without auth information
		ctx := context.Background()

		// Act
		result := extractUserFromContext(ctx)

		// Assert
		assert.Equal(t, "system", result)
	})

	t.Run("returns system when user ID is empty string", func(t *testing.T) {
		// Arrange - context with empty user ID
		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "")

		// Act
		result := extractUserFromContext(ctx)

		// Assert
		assert.Equal(t, "system", result)
	})

	t.Run("returns system when user ID has wrong type", func(t *testing.T) {
		// Arrange - context with wrong type for user ID (int instead of string)
		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, 12345)

		// Act
		result := extractUserFromContext(ctx)

		// Assert
		assert.Equal(t, "system", result)
	})

	t.Run("returns user ID with UUID format", func(t *testing.T) {
		// Arrange - typical production user ID format
		expectedUserID := "550e8400-e29b-41d4-a716-446655440000"
		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, expectedUserID)

		// Act
		result := extractUserFromContext(ctx)

		// Assert
		assert.Equal(t, expectedUserID, result)
	})
}

// =============================================================================
// Fungibility Validation Tests
// =============================================================================

// mockInstrumentRegistry implements InstrumentRegistry for testing.
type mockInstrumentRegistry struct {
	instrument InstrumentDefinition
	err        error
}

func (m *mockInstrumentRegistry) GetInstrument(_ context.Context, _ string, _ int) (InstrumentDefinition, error) {
	return m.instrument, m.err
}

// mockInstrumentDefinition implements InstrumentDefinition for testing.
type mockInstrumentDefinition struct {
	program domain.FungibilityKeyProgram
}

func (m *mockInstrumentDefinition) GetFungibilityKeyProgram() domain.FungibilityKeyProgram {
	return m.program
}

// mockFungibilityKeyProgram implements domain.FungibilityKeyProgram for testing.
type mockFungibilityKeyProgram struct {
	keyFunc func(attributes map[string]string) string
}

var _ domain.FungibilityKeyProgram = (*mockFungibilityKeyProgram)(nil)

func (m *mockFungibilityKeyProgram) Eval(activation interface{}) (interface{}, error) {
	if act, ok := activation.(map[string]interface{}); ok {
		if attrs, ok := act["attributes"].(map[string]string); ok {
			return m.keyFunc(attrs), nil
		}
	}
	return m.keyFunc(map[string]string{}), nil
}

// TestValidatePostingPair_NoRegistry tests that fungibility validation is skipped
// when no registry is configured (backward compatibility mode).
func TestValidatePostingPair_NoRegistry(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create service WITHOUT registry (backward compatibility)
	svc, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	// Create postings with different attributes that would fail fungibility validation
	usdInstrument, _ := domain.NewInstrument("USD", 1, "CURRENCY", 2)
	amount := domain.NewMoney(decimal.NewFromInt(100), usdInstrument)

	debit, err := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		amount,
		"ACC-1",
		time.Now(),
		"test-correlation",
	)
	require.NoError(t, err)
	debit.Attributes = map[string]string{"batch_id": "2024-A"}

	credit, err := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionCredit,
		amount,
		"ACC-2",
		time.Now(),
		"test-correlation",
	)
	require.NoError(t, err)
	credit.Attributes = map[string]string{"batch_id": "2024-B"} // Different batch!

	// Act - should pass because no registry means no fungibility check
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)

	// Assert
	assert.NoError(t, validationErr, "Without registry, fungibility validation should be skipped")
}

// TestValidatePostingPair_FullyFungibleInstrument tests that instruments without
// fungibility_key_expression are treated as fully fungible.
func TestValidatePostingPair_FullyFungibleInstrument(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create mock registry that returns instrument with NO fungibility program
	registry := &mockInstrumentRegistry{
		instrument: &mockInstrumentDefinition{
			program: nil, // No program = fully fungible
		},
		err: nil,
	}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
	)
	require.NoError(t, err)

	// Create postings with different attributes
	usdInstrument, _ := domain.NewInstrument("USD", 1, "CURRENCY", 2)
	amount := domain.NewMoney(decimal.NewFromInt(100), usdInstrument)

	debit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		amount,
		"ACC-1",
		time.Now(),
		"test",
	)
	debit.Attributes = map[string]string{"source": "bank-A"}

	credit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionCredit,
		amount,
		"ACC-2",
		time.Now(),
		"test",
	)
	credit.Attributes = map[string]string{"source": "bank-B"}

	// Act - should pass because USD is fully fungible
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)

	// Assert
	assert.NoError(t, validationErr, "Fully fungible instruments should accept any attributes")
}

// TestValidatePostingPair_FungibilityMismatch tests that transactions with
// incompatible attributes are rejected.
func TestValidatePostingPair_FungibilityMismatch(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create mock fungibility program that checks batch_id
	program := &mockFungibilityKeyProgram{
		keyFunc: func(attrs map[string]string) string {
			return "batch:" + attrs["batch_id"]
		},
	}

	registry := &mockInstrumentRegistry{
		instrument: &mockInstrumentDefinition{
			program: program,
		},
		err: nil,
	}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
	)
	require.NoError(t, err)

	// Create RICE-KG postings with DIFFERENT batch IDs
	riceInstrument, _ := domain.NewInstrument("RICE-KG", 1, "COMMODITY", 3)
	amount := domain.NewMoney(decimal.NewFromInt(100), riceInstrument)

	debit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		amount,
		"ACC-INVENTORY-A",
		time.Now(),
		"test",
	)
	debit.Attributes = map[string]string{"batch_id": "2024-A", "grade": "1"}

	credit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionCredit,
		amount,
		"ACC-INVENTORY-B",
		time.Now(),
		"test",
	)
	credit.Attributes = map[string]string{"batch_id": "2024-B", "grade": "2"} // Different batch!

	// Act
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)

	// Assert
	require.Error(t, validationErr, "Should reject mismatched fungibility keys")
	grpcStatus, ok := status.FromError(validationErr)
	require.True(t, ok, "Error should be gRPC status")
	assert.Equal(t, codes.InvalidArgument, grpcStatus.Code())
	assert.Contains(t, grpcStatus.Message(), "fungibility")
}

// TestValidatePostingPair_MatchingFungibilityKeys tests that transactions with
// compatible attributes are accepted.
func TestValidatePostingPair_MatchingFungibilityKeys(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create mock fungibility program that checks batch_id
	program := &mockFungibilityKeyProgram{
		keyFunc: func(attrs map[string]string) string {
			return "batch:" + attrs["batch_id"]
		},
	}

	registry := &mockInstrumentRegistry{
		instrument: &mockInstrumentDefinition{
			program: program,
		},
		err: nil,
	}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
	)
	require.NoError(t, err)

	// Create RICE-KG postings with SAME batch ID (different grades are OK)
	riceInstrument, _ := domain.NewInstrument("RICE-KG", 1, "COMMODITY", 3)
	amount := domain.NewMoney(decimal.NewFromInt(100), riceInstrument)

	debit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		amount,
		"ACC-INVENTORY-A",
		time.Now(),
		"test",
	)
	debit.Attributes = map[string]string{"batch_id": "2024-A", "grade": "1"}

	credit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionCredit,
		amount,
		"ACC-INVENTORY-B",
		time.Now(),
		"test",
	)
	// Same batch_id, different grade - should pass because program only checks batch_id
	credit.Attributes = map[string]string{"batch_id": "2024-A", "grade": "2"}

	// Act
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)

	// Assert
	assert.NoError(t, validationErr, "Matching fungibility keys should pass validation")
}

// TestValidatePostingPair_RegistryUnavailable tests fail-closed behavior
// when the registry cannot be reached.
func TestValidatePostingPair_RegistryUnavailable(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create mock registry that returns an error (simulating unavailability)
	registry := &mockInstrumentRegistry{
		instrument: nil,
		err:        errTestRegistryConnectionRefused,
	}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
	)
	require.NoError(t, err)

	// Create valid postings
	usdInstrument, _ := domain.NewInstrument("USD", 1, "CURRENCY", 2)
	amount := domain.NewMoney(decimal.NewFromInt(100), usdInstrument)

	debit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		amount,
		"ACC-1",
		time.Now(),
		"test",
	)

	credit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionCredit,
		amount,
		"ACC-2",
		time.Now(),
		"test",
	)

	// Act
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)

	// Assert - should fail-closed (reject transaction when registry unavailable)
	require.Error(t, validationErr, "Should reject transaction when registry is unavailable")
	grpcStatus, ok := status.FromError(validationErr)
	require.True(t, ok, "Error should be gRPC status")
	assert.Equal(t, codes.Unavailable, grpcStatus.Code())
	assert.Contains(t, grpcStatus.Message(), "registry")
}

// TestValidatePostingPair_DoubleEntryValidationFirst tests that double-entry
// validation happens before fungibility validation.
func TestValidatePostingPair_DoubleEntryValidationFirst(t *testing.T) {
	// Arrange
	db := &gorm.DB{}
	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create mock registry - should NOT be called if double-entry fails
	registry := &mockInstrumentRegistry{
		instrument: &mockInstrumentDefinition{},
		err:        errTestRegistryShouldNotCall,
	}

	svc, err := NewFinancialAccountingService(
		repo, publisher, idempotencySvc, outboxPublisher, outboxRepo,
		WithRegistry(registry),
	)
	require.NoError(t, err)

	// Create postings with MISMATCHED instruments (should fail double-entry validation)
	usdInstrument, _ := domain.NewInstrument("USD", 1, "CURRENCY", 2)
	eurInstrument, _ := domain.NewInstrument("EUR", 1, "CURRENCY", 2)

	debit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionDebit,
		domain.NewMoney(decimal.NewFromInt(100), usdInstrument),
		"ACC-USD",
		time.Now(),
		"test",
	)

	credit, _ := domain.NewLedgerPosting(
		uuid.New(),
		domain.PostingDirectionCredit,
		domain.NewMoney(decimal.NewFromInt(100), eurInstrument), // Different instrument!
		"ACC-EUR",
		time.Now(),
		"test",
	)

	// Act
	validationErr := svc.validatePostingPair(context.Background(), debit, credit)

	// Assert - should fail with double-entry error, not registry error
	require.Error(t, validationErr)
	grpcStatus, ok := status.FromError(validationErr)
	require.True(t, ok, "Error should be gRPC status")
	assert.Equal(t, codes.InvalidArgument, grpcStatus.Code())
	assert.Contains(t, grpcStatus.Message(), "double-entry")
}
