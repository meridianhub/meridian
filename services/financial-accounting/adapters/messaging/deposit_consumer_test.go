package messaging

import (
	"context"
	"fmt"
	"testing"
	"time"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const testTenantID = "test_tenant"

func setupTestServices(t *testing.T) (*service.PostingService, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.LedgerPostingEntity{},
		&persistence.FinancialBookingLogEntity{},
		&persistence.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schemaName)).Error
	require.NoError(t, err)

	// Create tables in tenant schema (singular names to match production)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.financial_booking_log (
		id UUID PRIMARY KEY,
		financial_account_type VARCHAR(50) NOT NULL,
		product_service_reference VARCHAR(255) NOT NULL,
		business_unit_reference VARCHAR(255) NOT NULL,
		chart_of_accounts_rules TEXT NOT NULL,
		base_currency VARCHAR(3) NOT NULL,
		status VARCHAR(20) NOT NULL,
		idempotency_key VARCHAR(255) NOT NULL UNIQUE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		version BIGINT NOT NULL DEFAULT 1,
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.ledger_posting (
		id UUID PRIMARY KEY,
		financial_booking_log_id UUID NOT NULL,
		posting_direction VARCHAR(20) NOT NULL,
		amount_cents BIGINT NOT NULL,
		currency VARCHAR(3) NOT NULL,
		account_id VARCHAR(255) NOT NULL,
		value_date TIMESTAMP WITH TIME ZONE NOT NULL,
		posting_result TEXT,
		status VARCHAR(20) NOT NULL,
		correlation_id VARCHAR(255),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		deleted_at TIMESTAMP WITH TIME ZONE
	)`, schemaName)).Error
	require.NoError(t, err)

	// Create audit_outbox table for GORM hooks
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
		record_id UUID NOT NULL,
		old_values JSONB,
		new_values JSONB,
		status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip INET,
		user_agent TEXT
	)`, schemaName)).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %q, public", schemaName)).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewLedgerRepository(db)
	return service.NewPostingService(repo, "BANK-CASH-001"), ctx, cleanup
}

func TestNewDepositConsumer(t *testing.T) {
	postingService, _, cleanup := setupTestServices(t)
	defer cleanup()

	tests := []struct {
		name    string
		config  kafka.ConsumerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
				ClientID:         "test-consumer",
			},
			wantErr: false,
		},
		{
			name: "missing bootstrap servers",
			config: kafka.ConsumerConfig{
				GroupID: "test-group",
			},
			wantErr: true,
		},
		{
			name: "missing group ID",
			config: kafka.ConsumerConfig{
				BootstrapServers: "localhost:9092",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewDepositConsumer(tt.config, postingService)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewDepositConsumer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && consumer != nil {
				defer func() {
					_ = consumer.Close()
				}()
			}
		})
	}
}

func TestDepositConsumer_HandleDepositEvent(t *testing.T) {
	postingService, ctx, cleanup := setupTestServices(t)
	defer cleanup()

	consumer, err := NewDepositConsumer(kafka.ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, postingService)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	tests := []struct {
		name    string
		event   *eventsv1.DepositEvent
		wantErr bool
	}{
		{
			name: "valid deposit event",
			event: &eventsv1.DepositEvent{
				AccountId:     "ACC-123",
				AmountCents:   10000,
				Currency:      commonv1.Currency_CURRENCY_GBP,
				CorrelationId: "deposit-001",
				ValueDate:     timestamppb.Now(),
				Timestamp:     timestamppb.Now(),
			},
			wantErr: false,
		},
		{
			name: "zero amount",
			event: &eventsv1.DepositEvent{
				AccountId:     "ACC-456",
				AmountCents:   0,
				Currency:      commonv1.Currency_CURRENCY_GBP,
				CorrelationId: "deposit-002",
				ValueDate:     timestamppb.Now(),
				Timestamp:     timestamppb.Now(),
			},
			wantErr: true,
		},
		{
			name: "nil value date",
			event: &eventsv1.DepositEvent{
				AccountId:     "ACC-789",
				AmountCents:   5000,
				Currency:      commonv1.Currency_CURRENCY_USD,
				CorrelationId: "deposit-003",
				ValueDate:     nil,
				Timestamp:     timestamppb.Now(),
			},
			wantErr: true,
		},
		{
			name: "unspecified currency",
			event: &eventsv1.DepositEvent{
				AccountId:     "ACC-999",
				AmountCents:   3000,
				Currency:      commonv1.Currency_CURRENCY_UNSPECIFIED,
				CorrelationId: "deposit-004",
				ValueDate:     timestamppb.Now(),
				Timestamp:     timestamppb.Now(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			err := consumer.handleDepositEvent(testCtx, tt.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("handleDepositEvent() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
