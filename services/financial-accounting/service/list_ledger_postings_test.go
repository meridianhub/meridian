package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// TestListLedgerPostings_DefensiveTests implements ADR-0008 defensive testing.
// Rationale: List operations must handle pagination, filtering, and edge cases gracefully.
func TestListLedgerPostings_DefensiveTests(t *testing.T) {
	tests := []struct {
		name      string
		setupRepo func(*gorm.DB) uuid.UUID // Returns booking log ID for filtering tests
		request   *financialaccountingv1.ListLedgerPostingsRequest
		wantCode  codes.Code
		wantErr   bool
		wantCount int
		rationale string
	}{
		// Happy path
		{
			name: "list all ledger postings with default pagination",
			setupRepo: func(db *gorm.DB) uuid.UUID {
				bookingLogID := uuid.New()
				// Create booking log first
				bookingLog := persistence.FinancialBookingLogEntity{
					ID:                      bookingLogID,
					FinancialAccountType:    "ASSET",
					ProductServiceReference: "PROD-001",
					BusinessUnitReference:   "BU-001",
					ChartOfAccountsRules:    "Standard rules",
					BaseCurrency:            "GBP",
					Status:                  "PENDING",
					IdempotencyKey:          uuid.New().String(),
					CreatedAt:               time.Now().UTC(),
					UpdatedAt:               time.Now().UTC(),
					Version:                 1,
				}
				db.Create(&bookingLog)

				// Create 3 postings
				for i := 0; i < 3; i++ {
					posting := persistence.LedgerPostingEntity{
						ID:                    uuid.New(),
						FinancialBookingLogID: bookingLogID,
						PostingDirection:      "DEBIT",
						AmountCents:           100000, // £1000.00
						Currency:              "GBP",
						AccountID:             "ACC-001",
						ValueDate:             time.Now().UTC(),
						PostingResult:         "",
						Status:                "PENDING",
						CorrelationID:         uuid.New().String(),
						CreatedAt:             time.Now().UTC(),
					}
					db.Create(&posting)
				}
				return bookingLogID
			},
			request: &financialaccountingv1.ListLedgerPostingsRequest{
				Pagination: &commonv1.Pagination{PageSize: 50},
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 3,
			rationale: "Default pagination should return all postings",
		},
		{
			name: "list with booking log ID filter",
			setupRepo: func(db *gorm.DB) uuid.UUID {
				bookingLogID1 := uuid.New()
				bookingLogID2 := uuid.New()

				// Create two booking logs
				for _, id := range []uuid.UUID{bookingLogID1, bookingLogID2} {
					bookingLog := persistence.FinancialBookingLogEntity{
						ID:                      id,
						FinancialAccountType:    "ASSET",
						ProductServiceReference: "PROD-002",
						BusinessUnitReference:   "BU-002",
						ChartOfAccountsRules:    "Standard rules",
						BaseCurrency:            "USD",
						Status:                  "PENDING",
						IdempotencyKey:          uuid.New().String(),
						CreatedAt:               time.Now().UTC(),
						UpdatedAt:               time.Now().UTC(),
						Version:                 1,
					}
					db.Create(&bookingLog)
				}

				// Create 2 postings for first log, 1 for second
				for i := 0; i < 2; i++ {
					posting := persistence.LedgerPostingEntity{
						ID:                    uuid.New(),
						FinancialBookingLogID: bookingLogID1,
						PostingDirection:      "DEBIT",
						AmountCents:           50000,
						Currency:              "USD",
						AccountID:             "ACC-002",
						ValueDate:             time.Now().UTC(),
						Status:                "PENDING",
						CorrelationID:         uuid.New().String(),
						CreatedAt:             time.Now().UTC(),
					}
					db.Create(&posting)
				}
				posting := persistence.LedgerPostingEntity{
					ID:                    uuid.New(),
					FinancialBookingLogID: bookingLogID2,
					PostingDirection:      "CREDIT",
					AmountCents:           75000,
					Currency:              "USD",
					AccountID:             "ACC-003",
					ValueDate:             time.Now().UTC(),
					Status:                "PENDING",
					CorrelationID:         uuid.New().String(),
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&posting)

				return bookingLogID1
			},
			request: func(bookingLogID uuid.UUID) *financialaccountingv1.ListLedgerPostingsRequest {
				return &financialaccountingv1.ListLedgerPostingsRequest{
					Pagination:            &commonv1.Pagination{PageSize: 50},
					FinancialBookingLogId: bookingLogID.String(),
				}
			}(uuid.Nil), // Will be set in test
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 2,
			rationale: "Booking log filter should return only postings for that log",
		},
		{
			name: "list with posting direction filter",
			setupRepo: func(db *gorm.DB) uuid.UUID {
				bookingLogID := uuid.New()
				bookingLog := persistence.FinancialBookingLogEntity{
					ID:                      bookingLogID,
					FinancialAccountType:    "ASSET",
					ProductServiceReference: "PROD-003",
					BusinessUnitReference:   "BU-003",
					ChartOfAccountsRules:    "Standard rules",
					BaseCurrency:            "EUR",
					Status:                  "PENDING",
					IdempotencyKey:          uuid.New().String(),
					CreatedAt:               time.Now().UTC(),
					UpdatedAt:               time.Now().UTC(),
					Version:                 1,
				}
				db.Create(&bookingLog)

				// Create 2 DEBIT and 1 CREDIT posting
				for i := 0; i < 2; i++ {
					posting := persistence.LedgerPostingEntity{
						ID:                    uuid.New(),
						FinancialBookingLogID: bookingLogID,
						PostingDirection:      "DEBIT",
						AmountCents:           100000,
						Currency:              "EUR",
						AccountID:             "ACC-004",
						ValueDate:             time.Now().UTC(),
						Status:                "PENDING",
						CorrelationID:         uuid.New().String(),
						CreatedAt:             time.Now().UTC(),
					}
					db.Create(&posting)
				}
				posting := persistence.LedgerPostingEntity{
					ID:                    uuid.New(),
					FinancialBookingLogID: bookingLogID,
					PostingDirection:      "CREDIT",
					AmountCents:           200000,
					Currency:              "EUR",
					AccountID:             "ACC-005",
					ValueDate:             time.Now().UTC(),
					Status:                "PENDING",
					CorrelationID:         uuid.New().String(),
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&posting)

				return bookingLogID
			},
			request: &financialaccountingv1.ListLedgerPostingsRequest{
				Pagination:       &commonv1.Pagination{PageSize: 50},
				PostingDirection: commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 2,
			rationale: "Direction filter should return only DEBIT postings",
		},

		// Unhappy paths - Invalid input
		{
			name:      "invalid page size - too small",
			setupRepo: func(_ *gorm.DB) uuid.UUID { return uuid.Nil },
			request: &financialaccountingv1.ListLedgerPostingsRequest{
				Pagination: &commonv1.Pagination{PageSize: 0},
			},
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Page size of 0 should be rejected",
		},
		{
			name:      "invalid page size - too large",
			setupRepo: func(_ *gorm.DB) uuid.UUID { return uuid.Nil },
			request: &financialaccountingv1.ListLedgerPostingsRequest{
				Pagination: &commonv1.Pagination{PageSize: 1001},
			},
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Page size over 1000 should be rejected",
		},
		{
			name:      "invalid booking log ID format",
			setupRepo: func(_ *gorm.DB) uuid.UUID { return uuid.Nil },
			request: &financialaccountingv1.ListLedgerPostingsRequest{
				Pagination:            &commonv1.Pagination{PageSize: 50},
				FinancialBookingLogId: "not-a-uuid",
			},
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Invalid UUID format should be rejected",
		},

		// Edge cases
		{
			name:      "empty result set",
			setupRepo: func(_ *gorm.DB) uuid.UUID { return uuid.Nil },
			request: &financialaccountingv1.ListLedgerPostingsRequest{
				Pagination: &commonv1.Pagination{PageSize: 50},
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 0,
			rationale: "Empty database should return empty list, not error",
		},
		{
			name: "list with date range filter",
			setupRepo: func(db *gorm.DB) uuid.UUID {
				bookingLogID := uuid.New()
				bookingLog := persistence.FinancialBookingLogEntity{
					ID:                      bookingLogID,
					FinancialAccountType:    "ASSET",
					ProductServiceReference: "PROD-DATE",
					BusinessUnitReference:   "BU-DATE",
					ChartOfAccountsRules:    "Standard rules",
					BaseCurrency:            "GBP",
					Status:                  "PENDING",
					IdempotencyKey:          uuid.New().String(),
					CreatedAt:               time.Now().UTC(),
					UpdatedAt:               time.Now().UTC(),
					Version:                 1,
				}
				db.Create(&bookingLog)

				// Create postings with different value dates
				now := time.Now().UTC()
				dates := []time.Time{
					now.AddDate(0, 0, -2), // 2 days ago
					now.AddDate(0, 0, -1), // 1 day ago (should match filter)
					now,                   // Today (should match filter)
					now.AddDate(0, 0, 1),  // Tomorrow
				}
				for _, date := range dates {
					posting := persistence.LedgerPostingEntity{
						ID:                    uuid.New(),
						FinancialBookingLogID: bookingLogID,
						PostingDirection:      "DEBIT",
						AmountCents:           100000,
						Currency:              "GBP",
						AccountID:             "ACC-DATE",
						ValueDate:             date,
						Status:                "PENDING",
						CorrelationID:         uuid.New().String(),
						CreatedAt:             time.Now().UTC(),
					}
					db.Create(&posting)
				}

				return bookingLogID
			},
			request: func() *financialaccountingv1.ListLedgerPostingsRequest {
				now := time.Now().UTC()
				return &financialaccountingv1.ListLedgerPostingsRequest{
					Pagination:    &commonv1.Pagination{PageSize: 50},
					ValueDateFrom: timestamppb.New(now.AddDate(0, 0, -2).Add(1 * time.Hour)), // Include yesterday posting
					ValueDateTo:   timestamppb.New(now.Add(1 * time.Hour)),                   // Include today posting
				}
			}(),
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 2,
			rationale: "Date range filter should return postings within range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			db, ctx, cleanup := setupTestDB(t)
			defer cleanup()
			bookingLogID := tt.setupRepo(db)

			// Update request with booking log ID if needed (for filter tests)
			if tt.name == "list with booking log ID filter" {
				tt.request.FinancialBookingLogId = bookingLogID.String()
			}

			repo := persistence.NewLedgerRepository(db)
			publisher := &mockEventPublisher{}
			idempotencySvc := &mockIdempotencyService{}

			service := NewFinancialAccountingService(repo, publisher, idempotencySvc)

			// Act
			resp, err := service.ListLedgerPostings(ctx, tt.request)

			// Assert
			if tt.wantErr {
				assert.Error(t, err, tt.rationale)
				st, ok := status.FromError(err)
				assert.True(t, ok, "Error should be a gRPC status error")
				assert.Equal(t, tt.wantCode, st.Code(), tt.rationale)
				assert.Nil(t, resp, "Response should be nil on error")
			} else {
				assert.NoError(t, err, tt.rationale)
				assert.NotNil(t, resp, tt.rationale)
				assert.NotNil(t, resp.LedgerPostings, "Postings array should not be nil")
				assert.Equal(t, tt.wantCount, len(resp.LedgerPostings), tt.rationale)
				assert.NotNil(t, resp.Pagination, "Pagination should be present")
			}
		})
	}
}

// TestListLedgerPostings_MultipleFilters tests combining multiple filters.
func TestListLedgerPostings_MultipleFilters(t *testing.T) {
	// Arrange
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	bookingLogID := uuid.New()
	bookingLog := persistence.FinancialBookingLogEntity{
		ID:                      bookingLogID,
		FinancialAccountType:    "ASSET",
		ProductServiceReference: "PROD-MULTI",
		BusinessUnitReference:   "BU-MULTI",
		ChartOfAccountsRules:    "Standard rules",
		BaseCurrency:            "USD",
		Status:                  "PENDING",
		IdempotencyKey:          uuid.New().String(),
		CreatedAt:               time.Now().UTC(),
		UpdatedAt:               time.Now().UTC(),
		Version:                 1,
	}
	db.Create(&bookingLog)

	// Create diverse postings
	testCases := []struct {
		direction string
		account   string
		currency  string
		status    string
	}{
		{"DEBIT", "ACC-001", "USD", "PENDING"},  // Should match
		{"DEBIT", "ACC-001", "USD", "POSTED"},   // Wrong status
		{"DEBIT", "ACC-002", "USD", "PENDING"},  // Wrong account
		{"CREDIT", "ACC-001", "USD", "PENDING"}, // Wrong direction
		{"DEBIT", "ACC-001", "GBP", "PENDING"},  // Wrong currency
	}

	for _, tc := range testCases {
		posting := persistence.LedgerPostingEntity{
			ID:                    uuid.New(),
			FinancialBookingLogID: bookingLogID,
			PostingDirection:      tc.direction,
			AmountCents:           100000,
			Currency:              tc.currency,
			AccountID:             tc.account,
			ValueDate:             time.Now().UTC(),
			Status:                tc.status,
			CorrelationID:         uuid.New().String(),
			CreatedAt:             time.Now().UTC(),
		}
		db.Create(&posting)
	}

	repo := persistence.NewLedgerRepository(db)
	publisher := &mockEventPublisher{}
	idempotencySvc := &mockIdempotencyService{}
	service := NewFinancialAccountingService(repo, publisher, idempotencySvc)

	req := &financialaccountingv1.ListLedgerPostingsRequest{
		Pagination:            &commonv1.Pagination{PageSize: 50},
		FinancialBookingLogId: bookingLogID.String(),
		AccountId:             "ACC-001",
		PostingDirection:      commonv1.PostingDirection_POSTING_DIRECTION_DEBIT,
		Currency:              "USD",
		Status:                commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
	}

	// Act
	resp, err := service.ListLedgerPostings(ctx, req)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, len(resp.LedgerPostings), "Multiple filters should return only exact match")
	if len(resp.LedgerPostings) > 0 {
		posting := resp.LedgerPostings[0]
		assert.Equal(t, "ACC-001", posting.AccountId)
		assert.Equal(t, commonv1.PostingDirection_POSTING_DIRECTION_DEBIT, posting.PostingDirection)
		assert.Equal(t, "USD", posting.PostingAmount.CurrencyCode)
		assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING, posting.Status)
	}
}
