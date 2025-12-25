package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// TestListFinancialBookingLogs_DefensiveTests implements ADR-0008 defensive testing.
// Rationale: List operations must handle pagination, filtering, and edge cases gracefully.
func TestListFinancialBookingLogs_DefensiveTests(t *testing.T) {
	tests := []struct {
		name      string
		setupRepo func(*gorm.DB)
		request   *financialaccountingv1.ListFinancialBookingLogsRequest
		wantCode  codes.Code
		wantErr   bool
		wantCount int
		rationale string
	}{
		// Happy path
		{
			name: "list all booking logs with default pagination",
			setupRepo: func(db *gorm.DB) {
				// Create 3 booking logs
				for i := 0; i < 3; i++ {
					entity := persistence.FinancialBookingLogEntity{
						ID:                      uuid.New(),
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
					db.Create(&entity)
				}
			},
			request: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: 50},
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 3,
			rationale: "Default pagination should return all booking logs",
		},
		{
			name: "list with status filter",
			setupRepo: func(db *gorm.DB) {
				// Create 2 PENDING and 1 POSTED
				for i := 0; i < 2; i++ {
					entity := persistence.FinancialBookingLogEntity{
						ID:                      uuid.New(),
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
					db.Create(&entity)
				}
				entity := persistence.FinancialBookingLogEntity{
					ID:                      uuid.New(),
					FinancialAccountType:    "LIABILITY",
					ProductServiceReference: "PROD-002",
					BusinessUnitReference:   "BU-002",
					ChartOfAccountsRules:    "Standard rules",
					BaseCurrency:            "USD",
					Status:                  "POSTED",
					IdempotencyKey:          uuid.New().String(),
					CreatedAt:               time.Now().UTC(),
					UpdatedAt:               time.Now().UTC(),
					Version:                 1,
				}
				db.Create(&entity)
			},
			request: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: 50},
				Status:     commonv1.TransactionStatus_TRANSACTION_STATUS_PENDING,
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 2,
			rationale: "Status filter should return only PENDING booking logs",
		},

		// Unhappy paths - Invalid input
		{
			name:      "invalid page size - too small",
			setupRepo: func(_ *gorm.DB) {},
			request: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: 0},
			},
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Page size of 0 should be rejected",
		},
		{
			name:      "invalid page size - too large",
			setupRepo: func(_ *gorm.DB) {},
			request: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: 1001},
			},
			wantCode:  codes.InvalidArgument,
			wantErr:   true,
			rationale: "Page size over 1000 should be rejected",
		},

		// Edge cases
		{
			name:      "empty result set",
			setupRepo: func(_ *gorm.DB) {},
			request: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: 50},
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 0,
			rationale: "Empty database should return empty list, not error",
		},
		{
			name: "pagination with small page size",
			setupRepo: func(db *gorm.DB) {
				// Create 5 booking logs
				for i := 0; i < 5; i++ {
					entity := persistence.FinancialBookingLogEntity{
						ID:                      uuid.New(),
						FinancialAccountType:    "ASSET",
						ProductServiceReference: "PROD-003",
						BusinessUnitReference:   "BU-003",
						ChartOfAccountsRules:    "Standard rules",
						BaseCurrency:            "EUR",
						Status:                  "PENDING",
						IdempotencyKey:          uuid.New().String(),
						CreatedAt:               time.Now().UTC().Add(time.Duration(-i) * time.Minute),
						UpdatedAt:               time.Now().UTC(),
						Version:                 1,
					}
					db.Create(&entity)
				}
			},
			request: &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: 2},
			},
			wantCode:  codes.OK,
			wantErr:   false,
			wantCount: 2,
			rationale: "Small page size should return correct number of results",
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

			service, svcErr := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
			if svcErr != nil {
				t.Fatalf("failed to create service: %v", svcErr)
			}

			// Act
			resp, err := service.ListFinancialBookingLogs(ctx, tt.request)

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
				assert.NotNil(t, resp.FinancialBookingLogs, "Booking logs array should not be nil")
				assert.Equal(t, tt.wantCount, len(resp.FinancialBookingLogs), tt.rationale)
				assert.NotNil(t, resp.Pagination, "Pagination should be present")
			}
		})
	}
}

// TestListFinancialBookingLogs_PaginationBehavior tests pagination edge cases.
func TestListFinancialBookingLogs_PaginationBehavior(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(*gorm.DB)
		pageSize         int32
		expectNextToken  bool
		expectTotalCount int64
		rationale        string
	}{
		{
			name: "pagination with more results available",
			setup: func(db *gorm.DB) {
				// Create 10 booking logs
				for i := 0; i < 10; i++ {
					entity := persistence.FinancialBookingLogEntity{
						ID:                      uuid.New(),
						FinancialAccountType:    "ASSET",
						ProductServiceReference: "PROD-PAGE",
						BusinessUnitReference:   "BU-PAGE",
						ChartOfAccountsRules:    "Standard rules",
						BaseCurrency:            "GBP",
						Status:                  "PENDING",
						IdempotencyKey:          uuid.New().String(),
						CreatedAt:               time.Now().UTC().Add(time.Duration(-i) * time.Second),
						UpdatedAt:               time.Now().UTC(),
						Version:                 1,
					}
					db.Create(&entity)
				}
			},
			pageSize:         5,
			expectNextToken:  true,
			expectTotalCount: 10,
			rationale:        "Should provide next page token when more results exist",
		},
		{
			name: "pagination with exact page size match",
			setup: func(db *gorm.DB) {
				// Create exactly 5 booking logs
				for i := 0; i < 5; i++ {
					entity := persistence.FinancialBookingLogEntity{
						ID:                      uuid.New(),
						FinancialAccountType:    "ASSET",
						ProductServiceReference: "PROD-EXACT",
						BusinessUnitReference:   "BU-EXACT",
						ChartOfAccountsRules:    "Standard rules",
						BaseCurrency:            "USD",
						Status:                  "PENDING",
						IdempotencyKey:          uuid.New().String(),
						CreatedAt:               time.Now().UTC().Add(time.Duration(-i) * time.Second),
						UpdatedAt:               time.Now().UTC(),
						Version:                 1,
					}
					db.Create(&entity)
				}
			},
			pageSize:         5,
			expectNextToken:  false,
			expectTotalCount: 5,
			rationale:        "Should not provide next page token when results match page size exactly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			db, ctx, cleanup := setupTestDB(t)
			defer cleanup()
			tt.setup(db)

			repo := persistence.NewLedgerRepository(db)
			publisher := &mockEventPublisher{}
			idempotencySvc := &mockIdempotencyService{}
			outboxPublisher := events.NewOutboxPublisher("financial-accounting")
			outboxRepo := events.NewPostgresOutboxRepository(db)
			service, svcErr := NewFinancialAccountingService(repo, publisher, idempotencySvc, outboxPublisher, outboxRepo)
			if svcErr != nil {
				t.Fatalf("failed to create service: %v", svcErr)
			}

			req := &financialaccountingv1.ListFinancialBookingLogsRequest{
				Pagination: &commonv1.Pagination{PageSize: tt.pageSize},
			}

			// Act
			resp, err := service.ListFinancialBookingLogs(ctx, req)

			// Assert
			assert.NoError(t, err, tt.rationale)
			assert.NotNil(t, resp)
			if tt.expectNextToken {
				assert.NotEmpty(t, resp.Pagination.NextPageToken, tt.rationale)
			} else {
				assert.Empty(t, resp.Pagination.NextPageToken, tt.rationale)
			}
			assert.Equal(t, tt.expectTotalCount, resp.Pagination.TotalCount, tt.rationale)
		})
	}
}
