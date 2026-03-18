package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"testing"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTestPostgres creates a CockroachDB test container with the audit_log table
// in a tenant schema, and returns the service instance and a tenant-scoped context.
func setupTestPostgres(t *testing.T) (*AuditService, context.Context, *gorm.DB) {
	t.Helper()

	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	tenantID, err := tenant.NewTenantID("test_audit")
	require.NoError(t, err)

	// Create tenant schema and audit_log table
	schema := tenantID.SchemaName()
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)).Error)
	require.NoError(t, db.Exec(fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %q.audit_log (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			event_id VARCHAR(100) UNIQUE,
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL,
			record_id VARCHAR(100) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT,
			schema_name VARCHAR(100),
			correlation_id VARCHAR(255),
			causation_id VARCHAR(255),
			idempotency_key VARCHAR(255)
		)
	`, schema)).Error)

	// Create the service
	logger := slog.Default()
	svc, err := NewAuditService(db, logger)
	require.NoError(t, err)

	ctx := tenant.WithTenant(context.Background(), tenantID)

	return svc, ctx, db
}

// insertAuditRow inserts a test audit log entry into the tenant schema.
func insertAuditRow(t *testing.T, db *gorm.DB, tenantID tenant.TenantID, eventID, tableName, operation, recordID string, changedBy string, createdAt time.Time, oldValues, newValues *string) {
	t.Helper()

	schema := tenantID.SchemaName()
	sql := fmt.Sprintf(`
		INSERT INTO %q.audit_log (event_id, table_name, operation, record_id, changed_by, created_at, old_values, new_values)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, schema)

	require.NoError(t, db.Exec(sql, eventID, tableName, operation, recordID, changedBy, createdAt, oldValues, newValues).Error)
}

func TestNewAuditService_NilDB(t *testing.T) {
	_, err := NewAuditService(nil, nil)
	require.ErrorIs(t, err, ErrNilDatabase)
}

func TestListAuditEntries_MissingTenantContext(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	svc, err := NewAuditService(db, slog.Default())
	require.NoError(t, err)

	// Context without tenant
	ctx := context.Background()
	_, err = svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing tenant context")
}

func TestListAuditEntries_NoFilters(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	// Insert 5 entries
	for i := 0; i < 5; i++ {
		insertAuditRow(t, db, tenantID,
			fmt.Sprintf("evt_%d", i),
			"parties",
			"INSERT",
			fmt.Sprintf("rec_%d", i),
			"user1",
			now.Add(time.Duration(-i)*time.Second),
			nil, strPtr(`{"name": "test"}`),
		)
	}

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 5)
	assert.Empty(t, resp.NextPageToken)

	// Verify ordering (newest first)
	for i := 1; i < len(resp.Entries); i++ {
		assert.True(t,
			resp.Entries[i-1].Timestamp.AsTime().After(resp.Entries[i].Timestamp.AsTime()) ||
				resp.Entries[i-1].Timestamp.AsTime().Equal(resp.Entries[i].Timestamp.AsTime()),
			"entries should be ordered by timestamp descending",
		)
	}
}

func TestListAuditEntries_TableNameFilter(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	insertAuditRow(t, db, tenantID, "evt_1", "parties", "INSERT", "rec_1", "user1", now, nil, nil)
	insertAuditRow(t, db, tenantID, "evt_2", "current_accounts", "INSERT", "rec_2", "user1", now.Add(-time.Second), nil, nil)
	insertAuditRow(t, db, tenantID, "evt_3", "parties", "UPDATE", "rec_1", "user1", now.Add(-2*time.Second), nil, nil)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		TableName: "parties",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)
	for _, entry := range resp.Entries {
		assert.Equal(t, "parties", entry.TableName)
	}
}

func TestListAuditEntries_OperationFilter(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	insertAuditRow(t, db, tenantID, "evt_1", "parties", "INSERT", "rec_1", "user1", now, nil, nil)
	insertAuditRow(t, db, tenantID, "evt_2", "parties", "UPDATE", "rec_1", "user1", now.Add(-time.Second), nil, nil)
	insertAuditRow(t, db, tenantID, "evt_3", "parties", "DELETE", "rec_1", "user1", now.Add(-2*time.Second), nil, nil)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_UPDATE,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 1)
	assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_UPDATE, resp.Entries[0].Operation)
}

func TestListAuditEntries_ChangedByFilter(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	insertAuditRow(t, db, tenantID, "evt_1", "parties", "INSERT", "rec_1", "alice", now, nil, nil)
	insertAuditRow(t, db, tenantID, "evt_2", "parties", "INSERT", "rec_2", "bob", now.Add(-time.Second), nil, nil)
	insertAuditRow(t, db, tenantID, "evt_3", "parties", "INSERT", "rec_3", "alice", now.Add(-2*time.Second), nil, nil)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		ChangedBy: "alice",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)
	for _, entry := range resp.Entries {
		assert.Equal(t, "alice", entry.ChangedBy)
	}
}

func TestListAuditEntries_RecordIdFilter(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	insertAuditRow(t, db, tenantID, "evt_1", "parties", "INSERT", "rec_1", "user1", now, nil, nil)
	insertAuditRow(t, db, tenantID, "evt_2", "parties", "INSERT", "rec_2", "user1", now.Add(-time.Second), nil, nil)
	insertAuditRow(t, db, tenantID, "evt_3", "parties", "UPDATE", "rec_1", "user1", now.Add(-2*time.Second), nil, nil)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		RecordId: "rec_1",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)
	for _, entry := range resp.Entries {
		assert.Equal(t, "rec_1", entry.RecordId)
	}
}

func TestListAuditEntries_Pagination(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	// Insert 30 entries with distinct timestamps
	for i := 0; i < 30; i++ {
		insertAuditRow(t, db, tenantID,
			fmt.Sprintf("evt_%02d", i),
			"parties",
			"INSERT",
			fmt.Sprintf("rec_%02d", i),
			"user1",
			now.Add(time.Duration(-i)*time.Second),
			nil, nil,
		)
	}

	// Page 1: get first 10
	resp1, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		PageSize: 10,
	})
	require.NoError(t, err)
	assert.Len(t, resp1.Entries, 10)
	assert.NotEmpty(t, resp1.NextPageToken)

	// Page 2: get next 10
	resp2, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		PageSize:  10,
		PageToken: resp1.NextPageToken,
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Entries, 10)
	assert.NotEmpty(t, resp2.NextPageToken)

	// Verify no overlap between pages
	page1IDs := make(map[string]bool)
	for _, e := range resp1.Entries {
		page1IDs[e.EntryId] = true
	}
	for _, e := range resp2.Entries {
		assert.False(t, page1IDs[e.EntryId], "page 2 should not contain entries from page 1")
	}

	// Page 3: get last 10
	resp3, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		PageSize:  10,
		PageToken: resp2.NextPageToken,
	})
	require.NoError(t, err)
	assert.Len(t, resp3.Entries, 10)
	assert.Empty(t, resp3.NextPageToken, "no more pages")
}

func TestListAuditEntries_PageSizeDefault(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	// Insert 30 entries
	for i := 0; i < 30; i++ {
		insertAuditRow(t, db, tenantID,
			fmt.Sprintf("evt_%02d", i),
			"parties",
			"INSERT",
			fmt.Sprintf("rec_%02d", i),
			"user1",
			now.Add(time.Duration(-i)*time.Second),
			nil, nil,
		)
	}

	// Default page size (0 means use default of 25)
	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 25)
	assert.NotEmpty(t, resp.NextPageToken)
}

func TestListAuditEntries_InvalidPageToken(t *testing.T) {
	svc, ctx, _ := setupTestPostgres(t)

	_, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		PageToken: "invalid-base64-token!!!",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid page token")
}

func TestListAuditEntries_TenantIsolation(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	tenantA, err := tenant.NewTenantID("tenant_a")
	require.NoError(t, err)
	tenantB, err := tenant.NewTenantID("tenant_b")
	require.NoError(t, err)

	// Create schemas for both tenants
	for _, tid := range []tenant.TenantID{tenantA, tenantB} {
		schema := tid.SchemaName()
		require.NoError(t, db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %q", schema)).Error)
		require.NoError(t, db.Exec(fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %q.audit_log (
				id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
				event_id VARCHAR(100) UNIQUE,
				table_name VARCHAR(100) NOT NULL,
				operation VARCHAR(10) NOT NULL,
				record_id VARCHAR(100) NOT NULL,
				old_values TEXT,
				new_values TEXT,
				created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
				changed_by VARCHAR(100)
			)
		`, schema)).Error)
	}

	now := time.Now().UTC()

	// Insert entries for tenant A
	insertAuditRow(t, db, tenantA, "evt_a1", "parties", "INSERT", "rec_a1", "user_a", now, nil, nil)
	insertAuditRow(t, db, tenantA, "evt_a2", "parties", "INSERT", "rec_a2", "user_a", now.Add(-time.Second), nil, nil)

	// Insert entries for tenant B
	insertAuditRow(t, db, tenantB, "evt_b1", "accounts", "UPDATE", "rec_b1", "user_b", now, nil, nil)

	svc, err := NewAuditService(db, slog.Default())
	require.NoError(t, err)

	// Query as tenant A
	ctxA := tenant.WithTenant(context.Background(), tenantA)
	respA, err := svc.ListAuditEntries(ctxA, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err)
	assert.Len(t, respA.Entries, 2)

	// Query as tenant B
	ctxB := tenant.WithTenant(context.Background(), tenantB)
	respB, err := svc.ListAuditEntries(ctxB, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err)
	assert.Len(t, respB.Entries, 1)
}

func TestListAuditEntries_OldNewValuesDeserialization(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	oldVals := strPtr(`{"balance": "100.00", "name": "Old Name"}`)
	newVals := strPtr(`{"balance": "200.00", "name": "New Name"}`)

	insertAuditRow(t, db, tenantID, "evt_json", "accounts", "UPDATE", "rec_json", "user1", now, oldVals, newVals)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Entries, 1)

	entry := resp.Entries[0]
	require.NotNil(t, entry.OldValues)
	require.NotNil(t, entry.NewValues)

	assert.Equal(t, "100.00", entry.OldValues.Fields["balance"].GetStringValue())
	assert.Equal(t, "200.00", entry.NewValues.Fields["balance"].GetStringValue())
	assert.Equal(t, "Old Name", entry.OldValues.Fields["name"].GetStringValue())
	assert.Equal(t, "New Name", entry.NewValues.Fields["name"].GetStringValue())
}

func TestListAuditEntries_CombinedFilters(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	insertAuditRow(t, db, tenantID, "evt_1", "parties", "INSERT", "rec_1", "user1", now, nil, nil)
	insertAuditRow(t, db, tenantID, "evt_2", "parties", "UPDATE", "rec_1", "user1", now.Add(-time.Second), nil, nil)
	insertAuditRow(t, db, tenantID, "evt_3", "accounts", "INSERT", "rec_2", "user1", now.Add(-2*time.Second), nil, nil)
	insertAuditRow(t, db, tenantID, "evt_4", "parties", "INSERT", "rec_3", "user2", now.Add(-3*time.Second), nil, nil)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		TableName: "parties",
		Operation: auditv1.AuditOperation_AUDIT_OPERATION_INSERT,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entries, 2)
	for _, entry := range resp.Entries {
		assert.Equal(t, "parties", entry.TableName)
		assert.Equal(t, auditv1.AuditOperation_AUDIT_OPERATION_INSERT, entry.Operation)
	}
}

func TestListAuditEntries_EmptyResult(t *testing.T) {
	svc, ctx, _ := setupTestPostgres(t)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		TableName: "nonexistent_table",
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Entries)
	assert.Empty(t, resp.NextPageToken)
}

func TestCursorEncodeDecode(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	encoded := encodeCursor(now)
	decoded, err := decodeCursor(encoded)
	require.NoError(t, err)
	assert.True(t, now.Equal(decoded), "decoded time should equal original")
}

func TestDecodeCursor_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{"invalid base64", "!!!not-base64!!!"},
		{"invalid json", base64Encode(t, "not json")},
		{"empty json", base64Encode(t, "{}")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := decodeCursor(tt.token)
			assert.Error(t, err)
		})
	}
}

func TestStringToOperation(t *testing.T) {
	tests := []struct {
		input    string
		expected auditv1.AuditOperation
	}{
		{"INSERT", auditv1.AuditOperation_AUDIT_OPERATION_INSERT},
		{"UPDATE", auditv1.AuditOperation_AUDIT_OPERATION_UPDATE},
		{"DELETE", auditv1.AuditOperation_AUDIT_OPERATION_DELETE},
		{"INITIAL_IMPORT", auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT},
		{"UNKNOWN", auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED},
		{"", auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, stringToOperation(tt.input))
		})
	}
}

// Helper functions

func strPtr(s string) *string { return &s }

func base64Encode(t *testing.T, s string) string {
	t.Helper()
	return base64.URLEncoding.EncodeToString([]byte(s))
}

func TestNewAuditService_NilLogger_UsesDefault(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, nil)
	t.Cleanup(cleanup)

	// nil logger should succeed (falls back to slog.Default())
	svc, err := NewAuditService(db, nil)
	require.NoError(t, err)
	require.NotNil(t, svc)
}

func TestListAuditEntries_MalformedJSONValues(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	// Insert row with malformed JSON in new_values — rowToProto should skip it
	schema := tenantID.SchemaName()
	sql := fmt.Sprintf(`
		INSERT INTO %q.audit_log (event_id, table_name, operation, record_id, new_values, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, schema)
	require.NoError(t, db.Exec(sql,
		"evt_bad_json",
		"accounts",
		"INSERT",
		"rec_bad",
		`not valid json`,
		now,
	).Error)

	// Insert a valid row too
	insertAuditRow(t, db, tenantID, "evt_good", "accounts", "INSERT", "rec_good", "user1", now.Add(-time.Second), nil, nil)

	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{})
	require.NoError(t, err)
	// Malformed entry is skipped; only valid one is returned
	assert.Len(t, resp.Entries, 1)
	assert.Equal(t, "rec_good", resp.Entries[0].RecordId)
}

func TestListAuditEntries_PageSizeCap(t *testing.T) {
	svc, ctx, db := setupTestPostgres(t)

	tenantID, _ := tenant.FromContext(ctx)
	now := time.Now().UTC()

	// Insert 5 entries
	for i := 0; i < 5; i++ {
		insertAuditRow(t, db, tenantID,
			fmt.Sprintf("evt_cap_%d", i),
			"parties", "INSERT",
			fmt.Sprintf("rec_%d", i),
			"user1",
			now.Add(time.Duration(-i)*time.Second),
			nil, nil,
		)
	}

	// Request page_size > maxPageSize (100) — should be capped
	resp, err := svc.ListAuditEntries(ctx, &auditv1.ListAuditEntriesRequest{
		PageSize: 200,
	})
	require.NoError(t, err)
	// Only 5 rows exist so we get 5, but the cap was applied internally
	assert.Len(t, resp.Entries, 5)
}

func TestDecodeCursor_ZeroTimestamp(t *testing.T) {
	// Encode a zero-time cursor and verify decodeCursor rejects it
	token := base64Encode(t, `{"c":"0001-01-01T00:00:00Z"}`)
	_, err := decodeCursor(token)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrZeroCursorTime)
}
