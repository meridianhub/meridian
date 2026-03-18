package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// setupControlTestDB extends setupTestDB with the event_outbox table required
// by ControlFinancialBookingLog's transactional outbox pattern.
func setupControlTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, ctx, cleanup := setupTestDB(t)

	schemaName := "org_" + testTenantID
	err := db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.event_outbox (
		id UUID PRIMARY KEY,
		event_type VARCHAR(200) NOT NULL,
		aggregate_id VARCHAR(100) NOT NULL,
		aggregate_type VARCHAR(100) NOT NULL,
		event_payload BYTEA NOT NULL,
		correlation_id VARCHAR(100),
		causation_id VARCHAR(100),
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		topic VARCHAR(200) NOT NULL,
		partition_key VARCHAR(200),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		processed_at TIMESTAMP WITH TIME ZONE,
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		service_name VARCHAR(100) NOT NULL,
		tenant_id VARCHAR(100) NOT NULL DEFAULT ''
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	return db, ctx, cleanup
}

// insertTestBookingLog inserts a FinancialBookingLogEntity directly into the DB
// with the given status and returns its ID.
func insertTestBookingLog(t *testing.T, db *gorm.DB, status string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	entity := persistence.FinancialBookingLogEntity{
		ID:                      id,
		FinancialAccountType:    "CURRENT",
		ProductServiceReference: "PROD-001",
		BusinessUnitReference:   "BU-001",
		ChartOfAccountsRules:    "STANDARD",
		BaseCurrency:            "GBP",
		Status:                  status,
		IdempotencyKey:          uuid.New().String(),
		CreatedAt:               time.Now().UTC(),
		UpdatedAt:               time.Now().UTC(),
		Version:                 1,
	}
	err := db.Create(&entity).Error
	require.NoError(t, err)
	return id
}

// ─── RetrieveFinancialBookingLog ───────────────────────────────────────────────

func TestRetrieveFinancialBookingLog_InvalidArgument(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	cases := []struct {
		name string
		id   string
	}{
		{"empty ID", ""},
		{"non-UUID string", "not-a-uuid"},
		{"partial UUID", "550e8400-e29b-41d4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.RetrieveFinancialBookingLog(ctx, &financialaccountingv1.RetrieveFinancialBookingLogRequest{
				Id: tc.id,
			})
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

func TestRetrieveFinancialBookingLog_NotFound(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.RetrieveFinancialBookingLog(ctx, &financialaccountingv1.RetrieveFinancialBookingLogRequest{
		Id: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRetrieveFinancialBookingLog_Success(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	// Insert a posting for the booking log to verify postings are loaded
	posting := persistence.LedgerPostingEntity{
		ID:                    uuid.New(),
		FinancialBookingLogID: bookingLogID,
		PostingDirection:      "DEBIT",
		AmountMinorUnits:      5000,
		Currency:              "GBP",
		AccountID:             "ACC-001",
		ValueDate:             time.Now().UTC(),
		PostingResult:         "success",
		Status:                "POSTED",
		CreatedAt:             time.Now().UTC(),
		UpdatedAt:             time.Now().UTC(),
	}
	require.NoError(t, db.Create(&posting).Error)

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.RetrieveFinancialBookingLog(ctx, &financialaccountingv1.RetrieveFinancialBookingLogRequest{
		Id: bookingLogID.String(),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	assert.Equal(t, bookingLogID.String(), resp.FinancialBookingLog.Id)
	assert.Len(t, resp.FinancialBookingLog.Postings, 1, "postings should be loaded")
}

func TestRetrieveFinancialBookingLog_Success_NoPostings(t *testing.T) {
	db, ctx, cleanup := setupTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	svc := mustNewFinancialAccountingService(t, repo, &mockEventPublisher{}, &mockIdempotencyService{})

	resp, err := svc.RetrieveFinancialBookingLog(ctx, &financialaccountingv1.RetrieveFinancialBookingLogRequest{
		Id: bookingLogID.String(),
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	assert.Equal(t, bookingLogID.String(), resp.FinancialBookingLog.Id)
	assert.Empty(t, resp.FinancialBookingLog.Postings)
}

// ─── ControlFinancialBookingLog ────────────────────────────────────────────────

func TestControlFinancialBookingLog_MissingIdempotencyKey(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	cases := []struct {
		name string
		req  *financialaccountingv1.ControlFinancialBookingLogRequest
	}{
		{
			name: "nil idempotency key",
			req: &financialaccountingv1.ControlFinancialBookingLogRequest{
				Id:             uuid.New().String(),
				ControlAction:  financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:         "test reason",
				IdempotencyKey: nil,
			},
		},
		{
			name: "empty idempotency key",
			req: &financialaccountingv1.ControlFinancialBookingLogRequest{
				Id:            uuid.New().String(),
				ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
				Reason:        "test reason",
				IdempotencyKey: &commonv1.IdempotencyKey{
					Key: "",
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.ControlFinancialBookingLog(ctx, tc.req)
			require.Error(t, err)
			assert.Nil(t, resp)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, codes.InvalidArgument, st.Code())
		})
	}
}

func TestControlFinancialBookingLog_InvalidBookingLogID(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            "not-a-uuid",
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "test reason",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlFinancialBookingLog_UnspecifiedAction(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            uuid.New().String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_UNSPECIFIED,
		Reason:        "test reason",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlFinancialBookingLog_MissingReason(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            uuid.New().String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestControlFinancialBookingLog_NotFound(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            uuid.New().String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "test reason",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestControlFinancialBookingLog_CannotSuspendTerminal(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// POSTED is a terminal status — cannot suspend
	bookingLogID := insertTestBookingLog(t, db, "POSTED")

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            bookingLogID.String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "admin action",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlFinancialBookingLog_CannotResumePending(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// PENDING cannot be resumed (only FAILED/suspended can be resumed)
	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            bookingLogID.String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_RESUME,
		Reason:        "resuming",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlFinancialBookingLog_CannotTerminateTerminal(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	// CANCELLED is terminal — cannot terminate again
	bookingLogID := insertTestBookingLog(t, db, "CANCELLED")

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            bookingLogID.String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "terminate again",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.Error(t, err)
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlFinancialBookingLog_SuspendSuccess(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            bookingLogID.String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "fraud investigation",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	assert.Equal(t, bookingLogID.String(), resp.FinancialBookingLog.Id)
	// After suspend, status should be FAILED
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_FAILED, resp.FinancialBookingLog.Status)
}

func TestControlFinancialBookingLog_TerminateSuccess(t *testing.T) {
	db, ctx, cleanup := setupControlTestDB(t)
	defer cleanup()

	bookingLogID := insertTestBookingLog(t, db, "PENDING")

	repo := persistence.NewLedgerRepository(db)
	outboxPublisher := events.NewOutboxPublisher("financial-accounting")
	outboxRepo := events.NewPostgresOutboxRepository(db)
	svc, err := NewFinancialAccountingService(repo, &mockEventPublisher{}, &mockIdempotencyService{}, outboxPublisher, outboxRepo)
	require.NoError(t, err)

	resp, err := svc.ControlFinancialBookingLog(ctx, &financialaccountingv1.ControlFinancialBookingLogRequest{
		Id:            bookingLogID.String(),
		ControlAction: financialaccountingv1.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "business decision",
		IdempotencyKey: &commonv1.IdempotencyKey{
			Key: uuid.New().String(),
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.FinancialBookingLog)
	assert.Equal(t, bookingLogID.String(), resp.FinancialBookingLog.Id)
	// After terminate, status should be CANCELLED
	assert.Equal(t, commonv1.TransactionStatus_TRANSACTION_STATUS_CANCELLED, resp.FinancialBookingLog.Status)
}
