package kafka

import (
	"context"
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory SQLite database for testing.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Create audit_log table (singular, matching production schema)
	err = db.Exec(`
		CREATE TABLE audit_log (
			event_id TEXT PRIMARY KEY,
			table_name TEXT NOT NULL,
			operation TEXT NOT NULL,
			record_id TEXT NOT NULL,
			old_values TEXT,
			new_values TEXT,
			created_at TIMESTAMP NOT NULL,
			tenant_id TEXT NOT NULL,
			schema_name TEXT,
			changed_by TEXT,
			transaction_id TEXT,
			client_ip TEXT,
			user_agent TEXT,
			correlation_id TEXT,
			causation_id TEXT,
			idempotency_key TEXT
		)
	`).Error
	require.NoError(t, err)

	return db
}

func TestNewAuditConsumer(t *testing.T) {
	tests := []struct {
		name        string
		config      ConsumerConfig
		wantErr     bool
		expectedErr error
	}{
		{
			name: "valid configuration",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				Topic:            "audit.events.test.v1",
				GroupID:          "test-group",
				ClientID:         "test-client",
				DB:               setupTestDB(t),
				HandlerTimeout:   10 * time.Second,
				MaxRetries:       3,
			},
			wantErr: false,
		},
		{
			name: "empty bootstrap servers",
			config: ConsumerConfig{
				Topic:    "audit.events.test.v1",
				GroupID:  "test-group",
				ClientID: "test-client",
				DB:       setupTestDB(t),
			},
			wantErr:     true,
			expectedErr: ErrEmptyBootstrapServers,
		},
		{
			name: "empty topic",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
				ClientID:         "test-client",
				DB:               setupTestDB(t),
			},
			wantErr:     true,
			expectedErr: ErrEmptyTopic,
		},
		{
			name: "nil database",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				Topic:            "audit.events.test.v1",
				GroupID:          "test-group",
				ClientID:         "test-client",
				DB:               nil,
			},
			wantErr:     true,
			expectedErr: ErrNilDatabase,
		},
		{
			name: "applies default timeout",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				Topic:            "audit.events.test.v1",
				GroupID:          "test-group",
				ClientID:         "test-client",
				DB:               setupTestDB(t),
				// No HandlerTimeout specified
			},
			wantErr: false,
		},
		{
			name: "applies default max retries",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				Topic:            "audit.events.test.v1",
				GroupID:          "test-group",
				ClientID:         "test-client",
				DB:               setupTestDB(t),
				// No MaxRetries specified
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewAuditConsumer(tt.config)

			if tt.wantErr {
				require.Error(t, err)
				if tt.expectedErr != nil {
					assert.ErrorIs(t, err, tt.expectedErr)
				}
				assert.Nil(t, consumer)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, consumer)
				assert.NotNil(t, consumer.consumer)
				assert.NotNil(t, consumer.db)
				assert.NotNil(t, consumer.dlqProducer)

				// Clean up
				_ = consumer.Close()
			}
		})
	}
}

func TestHandleAuditEvent(t *testing.T) {
	db := setupTestDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	consumer := &AuditConsumer{
		db: db,
	}

	tests := []struct {
		name        string
		event       *auditv1.AuditEvent
		tenantID    tenant.TenantID
		wantErr     bool
		validateRow func(t *testing.T, db *gorm.DB)
	}{
		{
			name: "INSERT operation",
			event: &auditv1.AuditEvent{
				EventId:        "evt_123",
				TableName:      "customers",
				Operation:      auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:       "cust_456",
				OldValues:      "",
				NewValues:      `{"name":"John Doe","email":"john@example.com"}`,
				Timestamp:      timestamppb.New(now),
				SchemaName:     "party",
				ChangedBy:      "user_789",
				TransactionId:  "txn_abc",
				ClientIp:       "192.168.1.100",
				UserAgent:      "Mozilla/5.0",
				CorrelationId:  "corr_xyz",
				CausationId:    "cause_123",
				IdempotencyKey: "idem_key_1",
			},
			tenantID: "tenant_001",
			wantErr:  false,
			validateRow: func(t *testing.T, db *gorm.DB) {
				var count int64
				err := db.Table("audit_log").Where("event_id = ?", "evt_123").Count(&count).Error
				require.NoError(t, err)
				assert.Equal(t, int64(1), count)

				// Query specific fields
				var result struct {
					TableName  string
					Operation  string
					RecordID   string
					OldValues  string
					NewValues  string
					TenantID   string
					SchemaName string
					ChangedBy  string
				}
				err = db.Table("audit_log").
					Select("table_name, operation, record_id, old_values, new_values, tenant_id, schema_name, changed_by").
					Where("event_id = ?", "evt_123").
					Scan(&result).Error
				require.NoError(t, err)
				assert.Equal(t, "customers", result.TableName)
				assert.Equal(t, "INSERT", result.Operation)
				assert.Equal(t, "cust_456", result.RecordID)
				assert.Equal(t, "", result.OldValues)
				assert.Contains(t, result.NewValues, "John Doe")
				assert.Equal(t, "tenant_001", result.TenantID)
				assert.Equal(t, "party", result.SchemaName)
				assert.Equal(t, "user_789", result.ChangedBy)
			},
		},
		{
			name: "UPDATE operation",
			event: &auditv1.AuditEvent{
				EventId:   "evt_124",
				TableName: "accounts",
				Operation: auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
				RecordId:  "acc_789",
				OldValues: `{"balance":100}`,
				NewValues: `{"balance":200}`,
				Timestamp: timestamppb.New(now),
			},
			tenantID: "tenant_002",
			wantErr:  false,
			validateRow: func(t *testing.T, db *gorm.DB) {
				var result struct {
					Operation string
					OldValues string
					NewValues string
				}
				err := db.Table("audit_log").
					Select("operation, old_values, new_values").
					Where("event_id = ?", "evt_124").
					Scan(&result).Error
				require.NoError(t, err)
				assert.Equal(t, "UPDATE", result.Operation)
				assert.Contains(t, result.OldValues, "100")
				assert.Contains(t, result.NewValues, "200")
			},
		},
		{
			name: "DELETE operation",
			event: &auditv1.AuditEvent{
				EventId:   "evt_125",
				TableName: "sessions",
				Operation: auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
				RecordId:  "sess_999",
				OldValues: `{"user_id":"usr_123"}`,
				NewValues: "",
				Timestamp: timestamppb.New(now),
			},
			tenantID: "tenant_003",
			wantErr:  false,
			validateRow: func(t *testing.T, db *gorm.DB) {
				var result struct {
					Operation string
					NewValues string
				}
				err := db.Table("audit_log").
					Select("operation, new_values").
					Where("event_id = ?", "evt_125").
					Scan(&result).Error
				require.NoError(t, err)
				assert.Equal(t, "DELETE", result.Operation)
				assert.Equal(t, "", result.NewValues)
			},
		},
		{
			name: "nil timestamp uses current time",
			event: &auditv1.AuditEvent{
				EventId:   "evt_126",
				TableName: "test_table",
				Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
				RecordId:  "rec_1",
				NewValues: "{}",
				Timestamp: nil, // nil timestamp
			},
			tenantID: "tenant_004",
			wantErr:  false,
			validateRow: func(t *testing.T, db *gorm.DB) {
				var result struct {
					CreatedAt time.Time
				}
				err := db.Table("audit_log").
					Select("created_at").
					Where("event_id = ?", "evt_126").
					Scan(&result).Error
				require.NoError(t, err)
				assert.False(t, result.CreatedAt.IsZero())
			},
		},
		{
			name: "unspecified operation returns error",
			event: &auditv1.AuditEvent{
				EventId:   "evt_127",
				TableName: "test_table",
				Operation: auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
				RecordId:  "rec_2",
			},
			tenantID: "tenant_005",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context with tenant
			ctx := tenant.WithTenant(context.Background(), tt.tenantID)

			err := consumer.handleAuditEvent(ctx, tt.event)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				if tt.validateRow != nil {
					tt.validateRow(t, db)
				}
			}
		})
	}
}

func TestHandleAuditEvent_MissingTenantContext(t *testing.T) {
	db := setupTestDB(t)
	consumer := &AuditConsumer{
		db: db,
	}

	event := &auditv1.AuditEvent{
		EventId:   "evt_999",
		TableName: "test",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "rec_1",
	}

	// Context without tenant
	ctx := context.Background()

	err := consumer.handleAuditEvent(ctx, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingTenantContext)
}

func TestProtoToOperation(t *testing.T) {
	tests := []struct {
		name      string
		operation auditv1.AuditOperation
		expected  string
	}{
		{
			name:      "INSERT",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
			expected:  "INSERT",
		},
		{
			name:      "UPDATE",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
			expected:  "UPDATE",
		},
		{
			name:      "DELETE",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_DELETE,
			expected:  "DELETE",
		},
		{
			name:      "INITIAL_IMPORT",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT,
			expected:  "INITIAL_IMPORT",
		},
		{
			name:      "UNSPECIFIED",
			operation: auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED,
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := domain.ProtoToOperation(tt.operation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConsumerStart_EmptyTopic(t *testing.T) {
	db := setupTestDB(t)
	consumer := &AuditConsumer{
		db: db,
	}

	err := consumer.Start("")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyTopic)
}

func TestIsRunning(t *testing.T) {
	t.Run("default false", func(t *testing.T) {
		c := &AuditConsumer{running: false}
		assert.False(t, c.IsRunning())
	})

	t.Run("true after set", func(t *testing.T) {
		c := &AuditConsumer{running: true}
		assert.True(t, c.IsRunning())
	})

	t.Run("concurrent safety", func(t *testing.T) {
		c := &AuditConsumer{running: false}
		// Write and read concurrently should not race
		done := make(chan struct{})
		go func() {
			c.mu.Lock()
			c.running = true
			c.mu.Unlock()
			close(done)
		}()
		<-done
		assert.True(t, c.IsRunning())
	})
}

func TestNewAuditConsumer_MaxRetriesOutOfRange(t *testing.T) {
	db := setupTestDB(t)
	config := ConsumerConfig{
		BootstrapServers: "localhost:9092",
		Topic:            "audit.events.test.v1",
		GroupID:          "test-group",
		ClientID:         "test-client",
		DB:               db,
		MaxRetries:       2147483648, // math.MaxInt32 + 1
	}

	consumer, err := NewAuditConsumer(config)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMaxRetriesOutOfRange)
	assert.Nil(t, consumer)
}

func TestHandleAuditEvent_CancelledContext(t *testing.T) {
	db := setupTestDB(t)
	consumer := &AuditConsumer{db: db}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = tenant.WithTenant(ctx, tenant.TenantID("tenant_cancel"))
	cancel() // cancel before calling

	event := &auditv1.AuditEvent{
		EventId:   "evt_cancel",
		TableName: "test_table",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
		RecordId:  "rec_1",
		Timestamp: timestamppb.New(time.Now().UTC()),
	}

	err := consumer.handleAuditEvent(ctx, event)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}
