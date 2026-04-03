package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// TestRetrieveLedgerPosting_DefensiveTests implements ADR-0008 defensive testing.
// Rationale: Retrieve operations must handle invalid IDs, missing records, and system errors gracefully.
func TestRetrieveLedgerPosting_DefensiveTests(t *testing.T) {
	// Pre-generate a UUID for the happy path test
	validPostingID := uuid.New()

	tests := []struct {
		name      string
		setupRepo func(*gorm.DB)
		requestID string
		wantCode  codes.Code
		wantErr   bool
		rationale string
	}{
		// Happy path
		{
			name: "valid posting retrieval",
			setupRepo: func(db *gorm.DB) {
				bookingLogID := uuid.New()
				entity := persistence.LedgerPostingEntity{
					ID:                    validPostingID, // Use the pre-generated ID
					FinancialBookingLogID: bookingLogID,
					PostingDirection:      "DEBIT",
					AmountMinorUnits:      10000, // $100.00
					Currency:              "GBP",
					AccountID:             "ACC-123",
					ValueDate:             time.Now().UTC(),
					PostingResult:         "success",
					Status:                "POSTED",
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&entity)
			},
			requestID: validPostingID.String(), // Request the same ID that was created
			wantCode:  codes.OK,
			wantErr:   false,
			rationale: "Standard valid retrieval should succeed",
		},

		// Unhappy paths - Invalid input
		{
			name:      "empty posting ID",
			setupRepo: func(_ *gorm.DB) {},
			requestID: "",
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Empty ID must be rejected with InvalidArgument",
		},
		{
			name:      "invalid UUID format",
			setupRepo: func(_ *gorm.DB) {},
			requestID: "not-a-uuid",
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Malformed UUID must be rejected with InvalidArgument",
		},
		{
			name:      "invalid UUID with special characters",
			setupRepo: func(_ *gorm.DB) {},
			requestID: "550e8400-e29b-41d4-a716-44665544000@",
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "UUID with invalid characters must be rejected",
		},

		// Edge cases - Not found
		{
			name:      "nonexistent posting ID",
			setupRepo: func(_ *gorm.DB) {},
			requestID: uuid.New().String(),
			wantCode:  codes.NotFound,
			wantErr:   true,
			rationale: "Missing posting must return NotFound",
		},

		// Negative testing - Values that shouldn't occur but might
		{
			name:      "nil UUID",
			setupRepo: func(_ *gorm.DB) {},
			requestID: uuid.Nil.String(),
			wantCode:  codes.NotFound,
			wantErr:   true,
			rationale: "Nil UUID should result in NotFound (valid UUID format but won't exist)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			db, ctx, cleanup := setupTestDB(t)
			defer cleanup()
			tt.setupRepo(db)

			repo := persistence.NewLedgerRepository(db)
			publisher := &mockEventPublisher{}
			idempotencySvc := &mockIdempotencyService{}
			outboxPublisher := events.NewOutboxPublisher("financial-accounting")
			outboxRepo := events.NewPostgresOutboxRepository(db)

			service, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
			if err != nil {
				t.Fatalf("failed to create service: %v", err)
			}

			req := &financialaccountingv1.RetrieveLedgerPostingRequest{
				Id: tt.requestID,
			}

			// Act
			resp, err := service.RetrieveLedgerPosting(ctx, req)

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
				assert.NotNil(t, resp.LedgerPosting, "Posting should be returned")
				// Verify response structure
				assert.NotEmpty(t, resp.LedgerPosting.Id, "Posting ID should be populated")
				assert.NotEmpty(t, resp.LedgerPosting.FinancialBookingLogId, "Booking log ID should be populated")
				assert.NotNil(t, resp.LedgerPosting.PostingAmount, "Amount should be populated")
				assert.NotNil(t, resp.LedgerPosting.CreatedAt, "CreatedAt should be populated")
			}
		})
	}
}

// TestRetrieveLedgerPosting_EdgeCases tests boundary conditions and edge cases.
func TestRetrieveLedgerPosting_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(*gorm.DB) uuid.UUID
		validate  func(*testing.T, *financialaccountingv1.LedgerPosting)
		rationale string
	}{
		{
			name: "posting with zero amount",
			setup: func(db *gorm.DB) uuid.UUID {
				postingID := uuid.New()
				entity := persistence.LedgerPostingEntity{
					ID:                    postingID,
					FinancialBookingLogID: uuid.New(),
					PostingDirection:      "DEBIT",
					AmountMinorUnits:      0, // Zero amount
					Currency:              "GBP",
					AccountID:             "ACC-123",
					ValueDate:             time.Now().UTC(),
					Status:                "PENDING",
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&entity)
				return postingID
			},
			validate: func(t *testing.T, posting *financialaccountingv1.LedgerPosting) {
				assert.NotNil(t, posting.PostingAmount)
				assert.Equal(t, "0", posting.PostingAmount.Amount)
			},
			rationale: "Zero amount postings should be retrieved correctly",
		},
		{
			name: "posting with maximum safe int64 amount",
			setup: func(db *gorm.DB) uuid.UUID {
				postingID := uuid.New()
				entity := persistence.LedgerPostingEntity{
					ID:                    postingID,
					FinancialBookingLogID: uuid.New(),
					PostingDirection:      "CREDIT",
					AmountMinorUnits:      9223372036854775807, // Max int64
					Currency:              "USD",
					AccountID:             "ACC-999",
					ValueDate:             time.Now().UTC(),
					Status:                "POSTED",
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&entity)
				return postingID
			},
			validate: func(t *testing.T, posting *financialaccountingv1.LedgerPosting) {
				assert.NotNil(t, posting.PostingAmount)
				assert.NotEmpty(t, posting.PostingAmount.Amount)
			},
			rationale: "Maximum int64 amounts should be handled without overflow",
		},
		{
			name: "posting with empty posting result",
			setup: func(db *gorm.DB) uuid.UUID {
				postingID := uuid.New()
				entity := persistence.LedgerPostingEntity{
					ID:                    postingID,
					FinancialBookingLogID: uuid.New(),
					PostingDirection:      "DEBIT",
					AmountMinorUnits:      5000,
					Currency:              "EUR",
					AccountID:             "ACC-456",
					ValueDate:             time.Now().UTC(),
					PostingResult:         "", // Empty result
					Status:                "PENDING",
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&entity)
				return postingID
			},
			validate: func(t *testing.T, posting *financialaccountingv1.LedgerPosting) {
				assert.Equal(t, "", posting.PostingResult, "Empty posting result should be preserved")
			},
			rationale: "Empty optional fields should be handled correctly",
		},
		{
			name: "posting with negative amount",
			setup: func(db *gorm.DB) uuid.UUID {
				postingID := uuid.New()
				entity := persistence.LedgerPostingEntity{
					ID:                    postingID,
					FinancialBookingLogID: uuid.New(),
					PostingDirection:      "CREDIT",
					AmountMinorUnits:      -15050, // -$150.50
					Currency:              "USD",
					AccountID:             "ACC-789",
					ValueDate:             time.Now().UTC(),
					Status:                "POSTED",
					CreatedAt:             time.Now().UTC(),
				}
				db.Create(&entity)
				return postingID
			},
			validate: func(t *testing.T, posting *financialaccountingv1.LedgerPosting) {
				assert.NotNil(t, posting.PostingAmount)
				assert.Contains(t, posting.PostingAmount.Amount, "-", "Negative amounts should preserve sign")
			},
			rationale: "Negative amounts (credits) should preserve sign",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			db, ctx, cleanup := setupTestDB(t)
			defer cleanup()
			postingID := tt.setup(db)

			repo := persistence.NewLedgerRepository(db)
			publisher := &mockEventPublisher{}
			idempotencySvc := &mockIdempotencyService{}
			outboxPublisher := events.NewOutboxPublisher("financial-accounting")
			outboxRepo := events.NewPostgresOutboxRepository(db)

			service, err := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
			if err != nil {
				t.Fatalf("failed to create service: %v", err)
			}

			req := &financialaccountingv1.RetrieveLedgerPostingRequest{
				Id: postingID.String(),
			}

			// Act
			resp, err := service.RetrieveLedgerPosting(ctx, req)

			// Assert
			assert.NoError(t, err, tt.rationale)
			assert.NotNil(t, resp)
			assert.NotNil(t, resp.LedgerPosting)

			tt.validate(t, resp.LedgerPosting)
		})
	}
}
