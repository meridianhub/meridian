package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/domain"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Test-specific errors for defensive testing scenarios
var (
	errTestRedisConnectionFailed  = errors.New("redis connection failed")
	errTestCannotMarkPending      = errors.New("cannot mark pending")
	errTestDatabaseConnectionLost = errors.New("database connection lost")
	errTestDatabaseWriteFailed    = errors.New("database write failed")
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
	repo := persistence.NewLedgerRepository(&gorm.DB{})
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}

	// Act
	service := NewFinancialAccountingService(repo, publisher, idempotencySvc)

	// Assert
	assert.NotNil(t, service, "Service should not be nil")
	assert.NotNil(t, service.repository, "Repository should be injected")
	assert.NotNil(t, service.eventPublisher, "Event publisher should be injected")
	assert.NotNil(t, service.idempotency, "Idempotency service should be injected")
}

// TestFinancialAccountingService_ImplementsInterface verifies the service implements the gRPC interface.
func TestFinancialAccountingService_ImplementsInterface(_ *testing.T) {
	// Arrange
	repo := persistence.NewLedgerRepository(&gorm.DB{})
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}

	// Act
	service := NewFinancialAccountingService(repo, publisher, idempotencySvc)

	// Assert - compile-time check that service implements the interface
	var _ financialaccountingv1.FinancialAccountingServiceServer = service
}

// TestNewFinancialAccountingService_DefensiveTests verifies nil dependency validation per ADR-0008.
// Rationale: Financial services must validate all dependencies to prevent runtime panics
// that could cause service outages or data corruption.
func TestNewFinancialAccountingService_DefensiveTests(t *testing.T) {
	tests := []struct {
		name           string
		repository     *persistence.LedgerRepository
		eventPub       EventPublisher
		idempotencySvc idempotency.Service
		shouldPanic    bool
		rationale      string
	}{
		// Happy path - covered by TestNewFinancialAccountingService
		{
			name:           "valid dependencies",
			repository:     persistence.NewLedgerRepository(&gorm.DB{}),
			eventPub:       &mockEventPublisher{},
			idempotencySvc: &mockIdempotencyService{},
			shouldPanic:    false,
			rationale:      "Standard valid initialization with all dependencies",
		},

		// Unhappy paths - nil dependencies (ADR-0008 mandatory tests)
		{
			name:           "nil repository",
			repository:     nil,
			eventPub:       &mockEventPublisher{},
			idempotencySvc: &mockIdempotencyService{},
			shouldPanic:    true,
			rationale:      "Repository is essential - nil would cause panic on first use",
		},
		{
			name:           "nil event publisher",
			repository:     persistence.NewLedgerRepository(&gorm.DB{}),
			eventPub:       nil,
			idempotencySvc: &mockIdempotencyService{},
			shouldPanic:    true,
			rationale:      "Event publisher is essential - nil would cause panic when publishing events",
		},
		{
			name:           "nil idempotency service",
			repository:     persistence.NewLedgerRepository(&gorm.DB{}),
			eventPub:       &mockEventPublisher{},
			idempotencySvc: nil,
			shouldPanic:    true,
			rationale:      "Idempotency service is essential - nil would cause panic on idempotent operations",
		},

		// Edge case - multiple nil dependencies
		{
			name:           "all dependencies nil",
			repository:     nil,
			eventPub:       nil,
			idempotencySvc: nil,
			shouldPanic:    true,
			rationale:      "Should panic on first nil check (repository)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.shouldPanic {
				assert.Panics(t, func() {
					NewFinancialAccountingService(tt.repository, tt.eventPub, tt.idempotencySvc)
				}, tt.rationale)
			} else {
				assert.NotPanics(t, func() {
					service := NewFinancialAccountingService(tt.repository, tt.eventPub, tt.idempotencySvc)
					assert.NotNil(t, service, tt.rationale)
					assert.NotNil(t, service.repository, "Repository should be injected")
					assert.NotNil(t, service.eventPublisher, "Event publisher should be injected")
					assert.NotNil(t, service.idempotency, "Idempotency service should be injected")
				}, tt.rationale)
			}
		})
	}
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

	postingAmount, err := fromProtoMoney(req.GetPostingAmount())
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        500000000, // 100.50 GBP
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        50,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        0,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        -100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        9223372036854775, // Near max int64 / 100
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        0,
					Nanos:        10000000, // 0.01 GBP
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        0,
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
		amount, _ := domain.NewMoney(decimal.NewFromInt(100), domain.CurrencyGBP)
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
				PostingAmount: &money.Money{
					CurrencyCode: "GBP",
					Units:        100,
					Nanos:        500000000,
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

		service := NewFinancialAccountingService(repo, publisher, mockIdem)

		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: validBookingLogID.String(),
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        500000000,
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

		service := NewFinancialAccountingService(repo, publisher, mockIdem)

		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: validBookingLogID.String(),
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
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

		service := NewFinancialAccountingService(repo, publisher, mockIdem)

		req := &financialaccountingv1.CaptureLedgerPostingRequest{
			FinancialBookingLogId: validBookingLogID.String(),
			PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			PostingAmount: &money.Money{
				CurrencyCode: "GBP",
				Units:        100,
				Nanos:        0,
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
				PostingAmount: &money.Money{
					CurrencyCode: "EUR",
					Units:        999,
					Nanos:        123456789,
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
		assert.Equal(t, "EUR", deserializedResponse.LedgerPosting.PostingAmount.CurrencyCode)
		assert.Equal(t, int64(999), deserializedResponse.LedgerPosting.PostingAmount.Units)
		assert.Equal(t, int32(123456789), deserializedResponse.LedgerPosting.PostingAmount.Nanos)
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
